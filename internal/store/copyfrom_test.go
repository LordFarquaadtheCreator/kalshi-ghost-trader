package store

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// makeTicks builds n tick rows with deterministic values for benchmarks and
// integration tests.
func makeTicks(n int) []Tick {
	ticks := make([]Tick, n)
	for i := 0; i < n; i++ {
		ticks[i] = Tick{
			TS:            int64(1700000000000 + i),
			RecvTS:        int64(1700000000001 + i),
			MarketTicker:  fmt.Sprintf("COPYBENCH-%d", i%100),
			MsgType:       "ticker",
			SID:           int64(i % 10),
			Seq:           int64(i),
			Price:         0.55,
			YesBid:        0.54,
			YesAsk:        0.56,
			YesBidSize:    100,
			YesAskSize:    200,
			Volume:        500,
			OpenInterest:  1000,
			Payload:       `{"price":"0.55"}`,
		}
	}
	return ticks
}

// BenchmarkCopyVsCreateInBatches compares COPY-based ingest against the old
// CreateInBatches path at 1k and 10k rows. COPY must win or the task closes
// as no-op per playbook R.6 DoD.
func BenchmarkCopyVsCreateInBatches(b *testing.B) {
	if os.Getenv("TEST_DB_DSN") == "" {
		b.Skip("TEST_DB_DSN not set; skipping Postgres-backed benchmark")
	}

	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("COPY/%d", n), func(b *testing.B) {
			db := testDB(b)
			ticks := makeTicks(n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				db.GormDB().Exec("DELETE FROM ticks")
				b.StartTimer()
				if err := db.CopyFromTicks(context.Background(), ticks); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(fmt.Sprintf("CreateInBatches/%d", n), func(b *testing.B) {
			db := testDB(b)
			ticks := makeTicks(n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				db.GormDB().Exec("DELETE FROM ticks")
				b.StartTimer()
				// Chunk at 1000 to stay under pgx's 65535 parameter limit
				// (22 cols × 1000 = 22000 params). InsertTickBatch passes
				// len(ticks) as chunk size, which blows the limit at 10k.
				if err := db.GormDB().WithContext(context.Background()).
					CreateInBatches(&ticks, 1000).Error; err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestCopyFromTicksRoundTrip inserts ticks via COPY and reads them back,
// verifying column values match.
func TestCopyFromTicksRoundTrip(t *testing.T) {
	db := testDB(t)
	ticks := makeTicks(10)
	if err := db.CopyFromTicks(context.Background(), ticks); err != nil {
		t.Fatalf("CopyFromTicks: %v", err)
	}

	var got []Tick
	if err := db.GormDB().Where("market_ticker LIKE 'COPYBENCH-%'").
		Order("ts ASC").Find(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("row count: got %d want 10", len(got))
	}
	for i, want := range ticks {
		if got[i].MarketTicker != want.MarketTicker {
			t.Fatalf("row %d market: got %s want %s", i, got[i].MarketTicker, want.MarketTicker)
		}
		if got[i].Price != want.Price {
			t.Fatalf("row %d price: got %v want %v", i, got[i].Price, want.Price)
		}
		if got[i].Payload != want.Payload {
			t.Fatalf("row %d payload: got %s want %s", i, got[i].Payload, want.Payload)
		}
	}
}

// TestCopyFromOrderbookRoundTrip inserts orderbook events via COPY and
// reads them back.
func TestCopyFromOrderbookRoundTrip(t *testing.T) {
	db := testDB(t)
	events := make([]OrderbookEvent, 10)
	for i := 0; i < 10; i++ {
		events[i] = OrderbookEvent{
			TS:           int64(1700000000000 + i),
			RecvTS:       int64(1700000000001 + i),
			MarketTicker: "OB-COPYBENCH",
			MsgType:      "orderbook_delta",
			SID:          int64(i % 5),
			Seq:          int64(i),
			Price:        0.60,
			Delta:        -5,
			Side:         "buy",
			Payload:      `{"delta":"-5"}`,
		}
	}
	if err := db.CopyFromOrderbook(context.Background(), events); err != nil {
		t.Fatalf("CopyFromOrderbook: %v", err)
	}

	var got []OrderbookEvent
	if err := db.GormDB().Where("market_ticker = 'OB-COPYBENCH'").
		Order("ts ASC").Find(&got).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("row count: got %d want 10", len(got))
	}
	if got[0].Side != "buy" || got[0].Price != 0.60 {
		t.Fatalf("row 0: side=%s price=%v", got[0].Side, got[0].Price)
	}
}
