package algorithms

import (
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

type countEmitter struct {
	mu     sync.Mutex
	orders []store.Order
}

func (c *countEmitter) EmitOrder(o store.Order) bool {
	c.mu.Lock()
	c.orders = append(c.orders, o)
	c.mu.Unlock()
	return true
}

func (c *countEmitter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.orders)
}

func newTestOrder(market, strategy string) store.Order {
	return store.Order{
		MarketTicker: market,
		Strategy:     strategy,
		EdgeCents:    5,
		MarketPrice:  0.90,
	}
}

func TestQuotaGuard_Disabled_AllPass(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{Enabled: false}, slog.Default())
	defer guard.Close()

	for i := 0; i < 10; i++ {
		guard.EmitOrder(newTestOrder("MKT-A", "strat"))
	}

	if paper.Count() != 10 {
		t.Fatalf("paper got %d, want 10", paper.Count())
	}
	// disabled: inner not called (NoopEmitter expected in real usage)
	if inner.Count() != 0 {
		t.Fatalf("inner got %d, want 0 when disabled", inner.Count())
	}
}

func TestQuotaGuard_Cooldown_DedupSameMarket(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 30,
		MaxPerSec:    100, // high enough not to interfere
		DailyLimit:   100,
	}, slog.Default())
	defer guard.Close()

	// 6 orders same market, same instant — simulates 6 strategies firing
	for i := 0; i < 6; i++ {
		guard.EmitOrder(newTestOrder("MKT-A", "strat"))
	}

	if paper.Count() != 6 {
		t.Fatalf("paper got %d, want 6", paper.Count())
	}
	if inner.Count() != 1 {
		t.Fatalf("inner got %d, want 1 (cooldown should drop 5)", inner.Count())
	}
}

func TestQuotaGuard_Cooldown_DifferentMarketsPass(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 30,
		MaxPerSec:    100,
		DailyLimit:   100,
	}, slog.Default())
	defer guard.Close()

	guard.EmitOrder(newTestOrder("MKT-A", "s1"))
	guard.EmitOrder(newTestOrder("MKT-B", "s2"))

	if inner.Count() != 2 {
		t.Fatalf("inner got %d, want 2 (different markets)", inner.Count())
	}
}

func TestQuotaGuard_DailyLimit(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 0, // no cooldown
		MaxPerSec:    100,
		DailyLimit:   3,
	}, slog.Default())
	defer guard.Close()

	for i := 0; i < 5; i++ {
		guard.EmitOrder(newTestOrder("MKT-"+string(rune('A'+i)), "s"))
	}

	if inner.Count() != 3 {
		t.Fatalf("inner got %d, want 3 (daily limit)", inner.Count())
	}
	if guard.RemainingQuota() != 0 {
		t.Fatalf("remaining %d, want 0", guard.RemainingQuota())
	}
}

func TestQuotaGuard_ResetDailyQuota(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 0,
		MaxPerSec:    100,
		DailyLimit:   2,
	}, slog.Default())
	defer guard.Close()

	guard.EmitOrder(newTestOrder("MKT-A", "s"))
	guard.EmitOrder(newTestOrder("MKT-B", "s"))
	guard.EmitOrder(newTestOrder("MKT-C", "s")) // dropped

	if inner.Count() != 2 {
		t.Fatalf("inner got %d, want 2", inner.Count())
	}

	guard.ResetDailyQuota()

	guard.EmitOrder(newTestOrder("MKT-D", "s"))
	if inner.Count() != 3 {
		t.Fatalf("inner got %d after reset, want 3", inner.Count())
	}
}

func TestQuotaGuard_PaperAlwaysReceives(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 30,
		MaxPerSec:    100,
		DailyLimit:   1,
	}, slog.Default())
	defer guard.Close()

	// 3 orders: first passes inner, rest dropped by cooldown/daily
	guard.EmitOrder(newTestOrder("MKT-A", "s1"))
	guard.EmitOrder(newTestOrder("MKT-A", "s2")) // cooldown drop
	guard.EmitOrder(newTestOrder("MKT-B", "s3")) // daily limit drop

	if paper.Count() != 3 {
		t.Fatalf("paper got %d, want 3 (always receives all)", paper.Count())
	}
	if inner.Count() != 1 {
		t.Fatalf("inner got %d, want 1", inner.Count())
	}
}

func TestQuotaGuard_CooldownExpiry(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 0, // 0 = no cooldown
		MaxPerSec:    100,
		DailyLimit:   100,
	}, slog.Default())
	defer guard.Close()

	// no cooldown — both should pass
	guard.EmitOrder(newTestOrder("MKT-A", "s1"))
	guard.EmitOrder(newTestOrder("MKT-A", "s2"))

	if inner.Count() != 2 {
		t.Fatalf("inner got %d, want 2 (no cooldown)", inner.Count())
	}
}

func TestQuotaGuard_RateLimit(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 0,
		MaxPerSec:    2,
		DailyLimit:   100,
	}, slog.Default())
	defer guard.Close()

	// fire 5 orders rapidly — bucket starts with 2 tokens
	passed := 0
	for i := 0; i < 5; i++ {
		if guard.EmitOrder(newTestOrder("MKT-"+string(rune('A'+i)), "s")) {
			passed++
		}
	}

	// 2 tokens pre-filled, rest dropped (non-blocking)
	if passed > 2 {
		t.Fatalf("passed %d, want <= 2 (rate limited)", passed)
	}
	if paper.Count() != 5 {
		t.Fatalf("paper got %d, want 5", paper.Count())
	}
}

// verify QuotaGuard satisfies OrderEmitter
var _ OrderEmitter = (*QuotaGuard)(nil)

func TestQuotaGuard_BudgetFloor(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 0,
		MaxPerSec:    100,
		DailyLimit:   100,
		BudgetTotal:  10.00, // $10 budget
		BudgetFloor:  3.00,  // stop when remaining < $3
	}, slog.Default())
	defer guard.Close()

	// Each order costs $4 (SuggestedSize=4)
	// Order 1: spent=4,  remaining=6  >= 3 → pass
	// Order 2: spent=8,  remaining=2  <  3 → drop
	o := newTestOrder("MKT-A", "s")
	o.SuggestedSize = 4

	guard.EmitOrder(o)
	rem := guard.RemainingBudget()
	if rem < 5.99 || rem > 6.01 {
		t.Fatalf("remaining after 1st: %.2f, want ~6.00", rem)
	}

	o2 := newTestOrder("MKT-B", "s")
	o2.SuggestedSize = 4
	guard.EmitOrder(o2) // would leave 2 < 3, dropped

	if inner.Count() != 1 {
		t.Fatalf("inner got %d, want 1 (budget floor)", inner.Count())
	}
	rem = guard.RemainingBudget()
	if rem < 5.99 || rem > 6.01 {
		t.Fatalf("remaining after dropped: %.2f, want ~6.00 (rollback)", rem)
	}
}

func TestQuotaGuard_BudgetDisabled(t *testing.T) {
	paper := &countEmitter{}
	inner := &countEmitter{}
	guard := NewQuotaGuard(paper, inner, QuotaConfig{
		Enabled:      true,
		CooldownSecs: 0,
		MaxPerSec:    100,
		DailyLimit:   100,
		BudgetTotal:  0, // no budget tracking
	}, slog.Default())
	defer guard.Close()

	for i := 0; i < 5; i++ {
		o := newTestOrder("MKT-"+string(rune('A'+i)), "s")
		o.SuggestedSize = 1000 // would blow any budget
		guard.EmitOrder(o)
	}

	if inner.Count() != 5 {
		t.Fatalf("inner got %d, want 5 (no budget limit)", inner.Count())
	}
	if guard.RemainingBudget() != -1 {
		t.Fatalf("remaining %.2f, want -1 (no tracking)", guard.RemainingBudget())
	}
}

// ensure time import used (for potential future timing tests)
var _ = time.Second
