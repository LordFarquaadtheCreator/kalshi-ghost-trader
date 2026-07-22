package insights

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDBInsights opens a GORM connection to a per-test schema, creates
// orders_v2 + pool_ledger + pool_balance, and the insights materialized views.
func testDBInsights(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DB_DSN")
	if dsn == "" {
		t.Skip("TEST_DB_DSN not set; skipping insights tests")
	}

	var b [8]byte
	_, _ = rand.Read(b[:])
	schema := "test_insights_" + hex.EncodeToString(b[:])

	{
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		if _, err := conn.Exec(context.Background(), fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
			_ = conn.Close(context.Background())
			t.Fatalf("create schema: %v", err)
		}
		_ = conn.Close(context.Background())
	}

	db, err := gorm.Open(postgres.Open(dsn+"&search_path="+schema), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("gorm open: %v", err)
	}

	sqlDB, _ := db.DB()

	// Create orders_v2.
	_, err = sqlDB.Exec(`
		CREATE TABLE orders_v2 (
			id bigserial PRIMARY KEY,
			client_order_id uuid NOT NULL DEFAULT gen_random_uuid(),
			ts_intent bigint NOT NULL,
			ts_submitted bigint, ts_acked bigint,
			event_ticker text NOT NULL,
			market_ticker text NOT NULL,
			strategy text NOT NULL,
			action text NOT NULL,
			contracts int NOT NULL,
			price_cents int NOT NULL,
			conv_prob_bps int NOT NULL,
			reason text,
			status text NOT NULL DEFAULT 'intent',
			gate_reason text,
			is_paper boolean NOT NULL DEFAULT true,
			kalshi_order_id text,
			fill_count int,
			fill_price_cents int,
			created_ts bigint NOT NULL DEFAULT 0,
			updated_ts bigint NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("create orders_v2: %v", err)
	}

	// Create pool_ledger + pool_balance.
	_, err = sqlDB.Exec(`
		CREATE TABLE pool_ledger (
			id bigserial PRIMARY KEY,
			ts bigint NOT NULL,
			entry_type text NOT NULL,
			amount_cents bigint NOT NULL,
			order_id bigint,
			note text
		);
		CREATE TABLE pool_balance (
			id int PRIMARY KEY,
			balance_cents bigint NOT NULL,
			updated_ts bigint NOT NULL
		);
	`)
	if err != nil {
		t.Fatalf("create pool tables: %v", err)
	}

	// Create insights schema + materialized views.
	_, err = sqlDB.Exec(`
		CREATE SCHEMA IF NOT EXISTS insights;

		CREATE MATERIALIZED VIEW insights.strategy_daily AS
		SELECT
			o.strategy,
			date_trunc('day', to_timestamp(o.ts_intent / 1000.0))::date AS day,
			o.gate_reason,
			count(*) FILTER (WHERE o.status = 'gated') AS gated_count,
			count(*) FILTER (WHERE o.status = 'accepted') AS accepted_count,
			count(*) FILTER (WHERE o.status IN ('submitted', 'held')) AS submitted_count,
			count(*) FILTER (WHERE o.status IN ('filled', 'partial')) AS filled_count,
			count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0) AS won_count,
			count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents = 0) AS lost_count,
			COALESCE(sum(o.fill_count * o.fill_price_cents) FILTER (WHERE o.status = 'filled'), 0) AS realized_pnl_cents,
			COALESCE(sum(o.fill_count * o.price_cents) FILTER (WHERE o.status IN ('filled', 'partial')), 0) AS invested_cents,
			CASE
				WHEN count(*) FILTER (WHERE o.status IN ('filled', 'partial')) > 0
				THEN count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0)::numeric /
				     count(*) FILTER (WHERE o.status IN ('filled', 'partial'))
				ELSE NULL::numeric
			END::numeric(6,4) AS win_rate,
			count(*) AS total_intents
		FROM orders_v2 o
		GROUP BY o.strategy, date_trunc('day', to_timestamp(o.ts_intent / 1000.0))::date, o.gate_reason
		WITH DATA;

		CREATE UNIQUE INDEX idx_strategy_daily_key
			ON insights.strategy_daily (strategy, day, COALESCE(gate_reason, ''));

		CREATE MATERIALIZED VIEW insights.pool_equity_curve AS
		SELECT
			date_trunc('day', to_timestamp(ts / 1000.0))::date AS day,
			sum(amount_cents) AS delta_cents,
			sum(sum(amount_cents)) OVER (ORDER BY date_trunc('day', to_timestamp(ts / 1000.0))::date) AS cumulative_cents
		FROM pool_ledger
		GROUP BY date_trunc('day', to_timestamp(ts / 1000.0))::date
		WITH DATA;

		CREATE UNIQUE INDEX idx_pool_equity_curve_key
			ON insights.pool_equity_curve (day);

		CREATE MATERIALIZED VIEW insights.band_performance AS
		SELECT
			o.strategy,
			(o.price_cents / 10) * 10 AS band_cents,
			count(*) FILTER (WHERE o.status IN ('filled', 'partial')) AS fills,
			count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0) AS wins,
			CASE
				WHEN count(*) FILTER (WHERE o.status IN ('filled', 'partial')) > 0
				THEN count(*) FILTER (WHERE o.status = 'filled' AND o.fill_price_cents > 0)::numeric /
				     count(*) FILTER (WHERE o.status IN ('filled', 'partial'))
				ELSE NULL::numeric
			END::numeric(6,4) AS hit_rate,
			COALESCE(sum(o.fill_count * o.fill_price_cents) FILTER (WHERE o.status = 'filled'), 0) AS pnl_cents,
			COALESCE(sum(o.fill_count * o.price_cents) FILTER (WHERE o.status IN ('filled', 'partial')), 0) AS invested_cents
		FROM orders_v2 o
		GROUP BY o.strategy, (o.price_cents / 10) * 10
		WITH DATA;

		CREATE UNIQUE INDEX idx_band_performance_key
			ON insights.band_performance (strategy, band_cents);

		CREATE MATERIALIZED VIEW insights.match_summary AS
		SELECT
			o.event_ticker,
			count(DISTINCT o.market_ticker) AS market_count,
			count(*) AS total_orders,
			count(*) FILTER (WHERE o.status = 'gated') AS gated_orders,
			count(*) FILTER (WHERE o.status IN ('filled', 'partial')) AS filled_orders,
			COALESCE(sum(o.fill_count * o.fill_price_cents) FILTER (WHERE o.status = 'filled'), 0) AS realized_pnl_cents,
			min(o.ts_intent) AS first_order_ts,
			max(o.ts_intent) AS last_order_ts
		FROM orders_v2 o
		GROUP BY o.event_ticker
		WITH DATA;

		CREATE UNIQUE INDEX idx_match_summary_key
			ON insights.match_summary (event_ticker);
	`)
	if err != nil {
		t.Fatalf("create views: %v", err)
	}

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		conn, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Errorf("cleanup: %v", err)
			return
		}
		_, _ = conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
		_ = conn.Close(context.Background())
	})

	return db
}

func TestInsightsRefreshAndQuery(t *testing.T) {
	db := testDBInsights(t)
	ctx := context.Background()
	now := time.Now().UnixMilli()

	// Seed 3 orders: one filled-won, one filled-lost, one gated.
	sqlDB, _ := db.DB()
	_, err := sqlDB.ExecContext(ctx, `
		INSERT INTO orders_v2 (ts_intent, event_ticker, market_ticker, strategy, action, contracts, price_cents, conv_prob_bps, status, gate_reason, fill_count, fill_price_cents, is_paper)
		VALUES
			($1, 'E1', 'E1-H', 'matchpoint', 'buy', 10, 50, 6500, 'filled', NULL, 10, 100, false),
			($2, 'E1', 'E1-A', 'matchpoint', 'buy', 5, 40, 5500, 'filled', NULL, 5, 0, false),
			($3, 'E1', 'E1-H', 'matchpoint', 'buy', 8, 60, 7000, 'gated', 'price_band', 0, 0, false)
	`, now, now, now)
	if err != nil {
		t.Fatalf("seed orders: %v", err)
	}

	// Seed ledger entries.
	_, err = sqlDB.ExecContext(ctx, `
		INSERT INTO pool_ledger (ts, entry_type, amount_cents, note)
		VALUES
			($1, 'deposit', 10000, 'initial'),
			($2, 'fill_cost', -500, 'fill'),
			($3, 'settlement_payout', 1000, 'win')
	`, now, now+1000, now+2000)
	if err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	// Refresh views.
	refresher := NewRefresher(db, 300, nil)
	if err := refresher.refreshAll(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Query strategy_daily — should have 2 rows (one for NULL gate_reason, one for 'price_band').
	type strategyDailyRow struct {
		Strategy        string
		Day             time.Time
		GateReason      *string
		GatedCount      int64
		FilledCount     int64
		WonCount        int64
		LostCount       int64
		RealizedPnlCents int64
		TotalIntents    int64
	}
	var rows []strategyDailyRow
	db.Raw("SELECT strategy, day, gate_reason, gated_count, filled_count, won_count, lost_count, realized_pnl_cents, total_intents FROM insights.strategy_daily ORDER BY gate_reason").Scan(&rows)

	if len(rows) != 2 {
		t.Fatalf("strategy_daily rows = %d, want 2", len(rows))
	}

	// Row with NULL gate_reason (filled orders).
	var nullGateRow, priceBandRow strategyDailyRow
	for _, r := range rows {
		if r.GateReason == nil {
			nullGateRow = r
		} else if *r.GateReason == "price_band" {
			priceBandRow = r
		}
	}

	if nullGateRow.FilledCount != 2 {
		t.Errorf("null gate filled_count = %d, want 2", nullGateRow.FilledCount)
	}
	if nullGateRow.WonCount != 1 {
		t.Errorf("null gate won_count = %d, want 1", nullGateRow.WonCount)
	}
	if nullGateRow.LostCount != 1 {
		t.Errorf("null gate lost_count = %d, want 1", nullGateRow.LostCount)
	}
	// realized_pnl: won order has fill_price_cents=100, fill_count=10 → 10*100=1000
	// lost order has fill_price_cents=0 → 0. Total = 1000.
	if nullGateRow.RealizedPnlCents != 1000 {
		t.Errorf("null gate realized_pnl = %d, want 1000", nullGateRow.RealizedPnlCents)
	}
	if nullGateRow.TotalIntents != 2 {
		t.Errorf("null gate total_intents = %d, want 2", nullGateRow.TotalIntents)
	}

	// Row with 'price_band' gate_reason (gated order).
	if priceBandRow.GatedCount != 1 {
		t.Errorf("price_band gated_count = %d, want 1", priceBandRow.GatedCount)
	}
	if priceBandRow.TotalIntents != 1 {
		t.Errorf("price_band total_intents = %d, want 1", priceBandRow.TotalIntents)
	}

	// Query pool_equity_curve — should have 1 row (all same day).
	type equityRow struct {
		Day            time.Time
		DeltaCents     int64
		CumulativeCents int64
	}
	var equity []equityRow
	db.Raw("SELECT day, delta_cents, cumulative_cents FROM insights.pool_equity_curve ORDER BY day").Scan(&equity)

	if len(equity) < 1 {
		t.Fatalf("pool_equity_curve rows = %d, want >= 1", len(equity))
	}
	// delta = 10000 - 500 + 1000 = 10500
	if equity[0].DeltaCents != 10500 {
		t.Errorf("delta_cents = %d, want 10500", equity[0].DeltaCents)
	}
	if equity[0].CumulativeCents != 10500 {
		t.Errorf("cumulative_cents = %d, want 10500", equity[0].CumulativeCents)
	}
}

func TestInsightsRefreshEmpty(t *testing.T) {
	db := testDBInsights(t)
	ctx := context.Background()

	// No data — refresh should still succeed.
	refresher := NewRefresher(db, 300, nil)
	if err := refresher.refreshAll(ctx); err != nil {
		t.Fatalf("refresh on empty: %v", err)
	}
}
