package orders

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/domain/match"
	"github.com/LordFarquaadtheCreator/kalshi-ghost-trader/v2/internal/ports"
)

// fakeExchange implements ports.Exchange for testing.
type fakeExchange struct {
	mu        sync.Mutex
	orders    map[string]*ports.CreateOrderResponse
	createErr error
	callCount atomic.Int64
}

func newFakeExchange() *fakeExchange {
	return &fakeExchange{orders: make(map[string]*ports.CreateOrderResponse)}
}

func (f *fakeExchange) CreateOrder(ctx context.Context, req ports.CreateOrderRequest) (*ports.CreateOrderResponse, error) {
	f.callCount.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return nil, f.createErr
	}

	// Default: fill everything.
	resp := &ports.CreateOrderResponse{
		OrderID:        "fake-" + req.ClientOrderID,
		Status:         ports.OrderStatusFilled,
		FillCount:      req.Contracts,
		FillPriceCents: req.PriceCents,
	}
	f.orders[req.ClientOrderID] = resp
	return resp, nil
}

func (f *fakeExchange) setError(err error) {
	f.createErr = err
}

// fakeRepo implements ports.OrderRepo for testing.
type fakeRepo struct {
	mu      sync.Mutex
	orders  map[int64]*ports.OrderRecord
	nextID  atomic.Int64
	inserts atomic.Int64
	updates atomic.Int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{orders: make(map[int64]*ports.OrderRecord)}
}

func (r *fakeRepo) Insert(ctx context.Context, o ports.OrderRecord) (int64, error) {
	r.inserts.Add(1)
	id := r.nextID.Add(1)
	o.ID = id
	r.mu.Lock()
	r.orders[id] = &o
	r.mu.Unlock()
	return id, nil
}

func (r *fakeRepo) UpdateStatus(ctx context.Context, id int64, status string, opts ports.UpdateOpts) error {
	r.updates.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil
	}
	if !IsLegalTransition(o.Status, status) {
		return ErrIllegalTransition
	}
	o.Status = status
	if opts.GateReason != "" {
		o.GateReason = opts.GateReason
	}
	if opts.KalshiOrderID != "" {
		o.KalshiOrderID = opts.KalshiOrderID
	}
	if opts.FillCount > 0 {
		o.FillCount = opts.FillCount
	}
	if opts.FillPriceCents > 0 {
		o.FillPriceCents = opts.FillPriceCents
	}
	return nil
}

func (r *fakeRepo) GetByID(ctx context.Context, id int64) (*ports.OrderRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.orders[id]
	if !ok {
		return nil, nil
	}
	cp := *o
	return &cp, nil
}

func (r *fakeRepo) GetByClientOrderID(ctx context.Context, coid string) (*ports.OrderRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, o := range r.orders {
		if o.ClientOrderID == coid {
			cp := *o
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) GetStaleOrders(ctx context.Context, statuses []string, olderThan time.Duration) ([]ports.OrderRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []ports.OrderRecord
	cutoff := time.Now().Add(-olderThan).UnixMilli()
	for _, o := range r.orders {
		for _, s := range statuses {
			if o.Status == s && o.TSIntent < cutoff {
				result = append(result, *o)
				break
			}
		}
	}
	return result, nil
}

// AllOrders returns a snapshot of all orders (thread-safe).
func (r *fakeRepo) AllOrders() []ports.OrderRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]ports.OrderRecord, 0, len(r.orders))
	for _, o := range r.orders {
		result = append(result, *o)
	}
	return result
}

// fakeLedger implements ports.LedgerRepo for testing.
type fakeLedger struct {
	mu       sync.Mutex
	balance  int64
	holds    map[int64]int64
	releases map[int64]int64
	fills    map[int64]int64
}

func newFakeLedger(balance int64) *fakeLedger {
	return &fakeLedger{
		balance:  balance,
		holds:    make(map[int64]int64),
		releases: make(map[int64]int64),
		fills:    make(map[int64]int64),
	}
}

func (l *fakeLedger) HoldForOrder(ctx context.Context, orderID int64, spendCents int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.balance < spendCents {
		return errInsufficient
	}
	l.balance -= spendCents
	l.holds[orderID] = spendCents
	return nil
}

func (l *fakeLedger) ReleaseHold(ctx context.Context, orderID int64, releaseCents int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balance += releaseCents
	l.releases[orderID] += releaseCents
	return nil
}

func (l *fakeLedger) RecordFill(ctx context.Context, orderID int64, fillCostCents int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balance -= fillCostCents
	l.fills[orderID] += fillCostCents
	return nil
}

func (l *fakeLedger) RecordSettlement(ctx context.Context, orderID int64, payoutCents int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balance += payoutCents
	return nil
}

func (l *fakeLedger) GetBalance(ctx context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balance, nil
}

func (l *fakeLedger) CheckInvariants(ctx context.Context) error {
	return nil
}

var errInsufficient = &insufficientErr{}

type insufficientErr struct{}

func (e *insufficientErr) Error() string { return "insufficient balance for hold" }

// --- Tests ---

func TestWorkerPaperHappyPath(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000})
	ledger := newFakeLedger(100000)

	w := NewWorker(gates, ledger, nil, repo, nil, slog.Default(), 100000, 0.25, false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	w.Submit([]match.Intent{{
		MarketTicker: "M1",
		Strategy:     "test",
		Action:       "buy",
		PriceCents:   50,
		ConvProbBps:  6500,
		Reason:       "test",
	}})

	time.Sleep(100 * time.Millisecond)
	cancel()

	if repo.inserts.Load() != 1 {
		t.Errorf("inserts = %d, want 1", repo.inserts.Load())
	}

	// Find the order and check it's filled (paper path).
	for _, o := range repo.AllOrders() {
		if o.Status != StatusFilled {
			t.Errorf("status = %s, want filled (paper path)", o.Status)
		}
		if o.IsPaper {
			if o.FillCount == 0 {
				t.Errorf("paper fill count = 0, want > 0")
			}
		}
	}
}

func TestWorkerRealHappyPath(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000})
	ledger := newFakeLedger(100000)
	exchange := newFakeExchange()

	w := NewWorker(gates, ledger, exchange, repo, nil, slog.Default(), 100000, 0.25, false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	w.Submit([]match.Intent{{
		MarketTicker: "M1",
		Strategy:     "test",
		Action:       "buy",
		PriceCents:   50,
		ConvProbBps:  6500,
		Reason:       "test",
	}})

	time.Sleep(100 * time.Millisecond)
	cancel()

	if exchange.callCount.Load() != 1 {
		t.Errorf("exchange calls = %d, want 1", exchange.callCount.Load())
	}

	for _, o := range repo.AllOrders() {
		if o.Status != StatusFilled {
			t.Errorf("status = %s, want filled", o.Status)
		}
		if o.KalshiOrderID == "" {
			t.Errorf("KalshiOrderID empty")
		}
	}
}

func TestWorkerGateRejection(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{
		StrategyEnabled: map[string]bool{"test": false}, // disabled
	})
	ledger := newFakeLedger(100000)
	exchange := newFakeExchange()

	w := NewWorker(gates, ledger, exchange, repo, nil, slog.Default(), 100000, 0.25, false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	w.Submit([]match.Intent{{
		MarketTicker: "M1",
		Strategy:     "test",
		Action:       "buy",
		PriceCents:   50,
		ConvProbBps:  6500,
	}})

	time.Sleep(100 * time.Millisecond)
	cancel()

	if exchange.callCount.Load() != 0 {
		t.Errorf("exchange calls = %d, want 0 (gated)", exchange.callCount.Load())
	}

	for _, o := range repo.AllOrders() {
		if o.Status != StatusGated {
			t.Errorf("status = %s, want gated", o.Status)
		}
		if o.GateReason != GateStrategyDisabled {
			t.Errorf("gate_reason = %s, want %s", o.GateReason, GateStrategyDisabled)
		}
	}
}

func TestWorkerInsufficientBalance(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000})
	ledger := newFakeLedger(10) // very low balance
	exchange := newFakeExchange()

	w := NewWorker(gates, ledger, exchange, repo, nil, slog.Default(), 10, 0.25, false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	w.Submit([]match.Intent{{
		MarketTicker: "M1",
		Strategy:     "test",
		Action:       "buy",
		PriceCents:   50,
		ConvProbBps:  6500,
	}})

	time.Sleep(100 * time.Millisecond)
	cancel()

	if exchange.callCount.Load() != 0 {
		t.Errorf("exchange calls = %d, want 0 (insufficient balance)", exchange.callCount.Load())
	}

	for _, o := range repo.AllOrders() {
		// Kelly sizing with 10 cents bankroll → 0 contracts → gated.
		if o.Status != StatusGated {
			t.Errorf("status = %s, want gated", o.Status)
		}
	}
}

func TestWorkerExchangeError(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000})
	ledger := newFakeLedger(100000)
	exchange := newFakeExchange()
	exchange.setError(errExchangeDown)

	w := NewWorker(gates, ledger, exchange, repo, nil, slog.Default(), 100000, 0.25, false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	w.Submit([]match.Intent{{
		MarketTicker: "M1",
		Strategy:     "test",
		Action:       "buy",
		PriceCents:   50,
		ConvProbBps:  6500,
	}})

	time.Sleep(100 * time.Millisecond)
	cancel()

	for _, o := range repo.AllOrders() {
		if o.Status != StatusFailed {
			t.Errorf("status = %s, want failed", o.Status)
		}
	}

	// Balance should be restored (hold released).
	bal, _ := ledger.GetBalance(context.Background())
	if bal != 100000 {
		t.Errorf("balance = %d, want 100000 (hold released on failure)", bal)
	}
}

func TestWorkerPartialFill(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000})
	ledger := newFakeLedger(100000)

	// Partial fill — 5 of N contracts filled.
	exchange := &partialExchange{fillCount: 5, fillPrice: 50}

	w := NewWorker(gates, ledger, exchange, repo, nil, slog.Default(), 100000, 0.25, false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = w.Run(ctx) }()

	w.Submit([]match.Intent{{
		MarketTicker: "M1",
		Strategy:     "test",
		Action:       "buy",
		PriceCents:   50,
		ConvProbBps:  6500,
	}})

	time.Sleep(100 * time.Millisecond)
	cancel()

	for _, o := range repo.AllOrders() {
		if o.Status != StatusPartial {
			t.Errorf("status = %s, want partial", o.Status)
		}
		if o.FillCount != 5 {
			t.Errorf("fill count = %d, want 5", o.FillCount)
		}
	}

	// Balance: hold was contracts*50, fill cost 5*50=250, remainder released.
	// contracts = KellyContracts(6500, 50, 100000, 0.25, false)
	// edge = (0.65 - 0.50) / (1 - 0.50) = 0.30
	// dollarsCents = 0.25 * 0.30 * 100000 = 7500
	// contracts = floor(7500 / 50) = 150
	// hold = 150 * 50 = 7500
	// fill cost = 5 * 50 = 250
	// remainder = 7500 - 250 = 7250 released
	// balance = 100000 - 7500 + 7250 - 250 = 99500
	bal, _ := ledger.GetBalance(context.Background())
	expected := int64(100000 - 7500 + 7250 - 250)
	if bal != expected {
		t.Errorf("balance = %d, want %d", bal, expected)
	}
}

// partialExchange returns a partial fill.
type partialExchange struct {
	fillCount int
	fillPrice int
}

func (p *partialExchange) CreateOrder(ctx context.Context, req ports.CreateOrderRequest) (*ports.CreateOrderResponse, error) {
	return &ports.CreateOrderResponse{
		OrderID:        "fake-partial",
		Status:         ports.OrderStatusPartial,
		FillCount:      p.fillCount,
		FillPriceCents: p.fillPrice,
	}, nil
}

var errExchangeDown = &exchangeErr{}

type exchangeErr struct{}

func (e *exchangeErr) Error() string { return "exchange down" }

func TestGateCacheEvaluatePaper(t *testing.T) {
	gates := NewGateCache(GateConfig{
		StrategyEnabled: map[string]bool{"test": false},
	})

	// Paper trades skip all gates.
	if reason := gates.Evaluate("test", 50, "M1", true, time.Now()); reason != "" {
		t.Errorf("paper gate reason = %q, want empty", reason)
	}
}

func TestGateCacheEvaluateStrategyDisabled(t *testing.T) {
	gates := NewGateCache(GateConfig{
		StrategyEnabled: map[string]bool{"test": false},
	})

	if reason := gates.Evaluate("test", 50, "M1", false, time.Now()); reason != GateStrategyDisabled {
		t.Errorf("gate reason = %q, want %s", reason, GateStrategyDisabled)
	}
}

func TestGateCacheEvaluatePriceBand(t *testing.T) {
	gates := NewGateCache(GateConfig{
		StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000,
		TriggerRanges:   map[string]PriceBand{"test": {MinCents: 30, MaxCents: 70}},
	})

	if reason := gates.Evaluate("test", 80, "M1", false, time.Now()); reason != GatePriceBand {
		t.Errorf("gate reason = %q, want %s", reason, GatePriceBand)
	}
	if reason := gates.Evaluate("test", 50, "M1", false, time.Now()); reason != "" {
		t.Errorf("gate reason = %q, want empty (in band)", reason)
	}
}

func TestGateCacheEvaluatePerMarketLimit(t *testing.T) {
	gates := NewGateCache(GateConfig{
		StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000,
		PerMarketLimit:   2,
	})

	gates.OnOrderAccepted("M1")
	gates.OnOrderAccepted("M1")

	if reason := gates.Evaluate("test", 50, "M1", false, time.Now()); reason != GatePerMarketLimit {
		t.Errorf("gate reason = %q, want %s", reason, GatePerMarketLimit)
	}
}

func TestGateCacheEvaluateCooldown(t *testing.T) {
	gates := NewGateCache(GateConfig{
		StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000,
		CooldownSeconds:  60,
	})

	now := time.Now()
	gates.OnOrderTerminal("test", "M1", true, now)

	if reason := gates.Evaluate("test", 50, "M1", false, now.Add(30*time.Second)); reason != GateCooldown {
		t.Errorf("gate reason = %q, want %s", reason, GateCooldown)
	}
	// After cooldown expires.
	if reason := gates.Evaluate("test", 50, "M1", false, now.Add(61*time.Second)); reason != "" {
		t.Errorf("gate reason = %q, want empty (cooldown expired)", reason)
	}
}

func TestWorkerDroppedCount(t *testing.T) {
	repo := newFakeRepo()
	gates := NewGateCache(GateConfig{StrategyEnabled: map[string]bool{"test": true}, QuotaRemaining: 1000})
	ledger := newFakeLedger(100000)

	w := NewWorker(gates, ledger, nil, repo, nil, slog.Default(), 100000, 0.25, false)

	// Don't run the worker — fill the queue.
	for i := 0; i < 1024; i++ {
		w.in <- match.Intent{MarketTicker: "M", Strategy: "test", Action: "buy", PriceCents: 50, ConvProbBps: 6500}
	}

	// Now queue is full — submit more, should drop.
	w.Submit([]match.Intent{{MarketTicker: "M", Strategy: "test", Action: "buy", PriceCents: 50, ConvProbBps: 6500}})

	if w.Dropped() != 1 {
		t.Errorf("dropped = %d, want 1", w.Dropped())
	}
}
