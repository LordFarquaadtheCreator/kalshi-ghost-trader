package orders

import (
	"sync"
	"time"
)

// GateConfig holds the per-strategy gate configuration loaded from DB.
type GateConfig struct {
	StrategyEnabled  map[string]bool       // strategy → real trading enabled
	TriggerRanges    map[string]PriceBand  // strategy → allowed price band
	PerMarketLimit   int                   // max concurrent orders per market
	CooldownSeconds  int                   // per-strategy cooldown after fill
	QuotaRemaining   int                   // global quota remaining
	PreMatchWindow   int                   // seconds before match start to allow trading
}

// PriceBand is the min/max price cents for a strategy.
type PriceBand struct {
	MinCents int
	MaxCents int
}

// GateCache holds gate configuration in memory, invalidated by config updates.
// Zero DB reads on the hot path.
type GateCache struct {
	mu     sync.RWMutex
	config GateConfig
	// per-market active order counts (for per_market_limit gate)
	activePerMarket map[string]int
	// per-strategy last-fill timestamps (for cooldown gate)
	lastFill map[string]time.Time
}

// NewGateCache creates a gate cache with the given initial config.
func NewGateCache(cfg GateConfig) *GateCache {
	return &GateCache{
		config:          cfg,
		activePerMarket: make(map[string]int),
		lastFill:        make(map[string]time.Time),
	}
}

// Update replaces the gate configuration atomically.
func (g *GateCache) Update(cfg GateConfig) {
	g.mu.Lock()
	g.config = cfg
	g.mu.Unlock()
}

// Evaluate checks all gates for an intent. Returns the first failing gate
// reason, or "" if all gates pass.
func (g *GateCache) Evaluate(strategy string, priceCents int, marketTicker string, isPaper bool, now time.Time) string {
	if isPaper {
		return "" // paper trades skip all gates
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// Gate 1: real trading globally enabled
	if !g.config.StrategyEnabled[strategy] {
		return GateStrategyDisabled
	}

	// Gate 2: price band
	band, ok := g.config.TriggerRanges[strategy]
	if ok {
		if priceCents < band.MinCents || priceCents > band.MaxCents {
			return GatePriceBand
		}
	}

	// Gate 3: per-market limit
	if g.config.PerMarketLimit > 0 && g.activePerMarket[marketTicker] >= g.config.PerMarketLimit {
		return GatePerMarketLimit
	}

	// Gate 4: quota
	if g.config.QuotaRemaining <= 0 {
		return GateQuota
	}

	// Gate 5: cooldown
	if g.config.CooldownSeconds > 0 {
		if last, ok := g.lastFill[strategy]; ok {
			if now.Sub(last) < time.Duration(g.config.CooldownSeconds)*time.Second {
				return GateCooldown
			}
		}
	}

	return ""
}

// OnOrderAccepted increments the per-market active count.
func (g *GateCache) OnOrderAccepted(marketTicker string) {
	g.mu.Lock()
	g.activePerMarket[marketTicker]++
	g.mu.Unlock()
}

// OnOrderTerminal decrements the per-market active count and records fill time.
func (g *GateCache) OnOrderTerminal(strategy, marketTicker string, filled bool, now time.Time) {
	g.mu.Lock()
	if g.activePerMarket[marketTicker] > 0 {
		g.activePerMarket[marketTicker]--
	}
	if filled {
		g.lastFill[strategy] = now
	}
	g.mu.Unlock()
}
