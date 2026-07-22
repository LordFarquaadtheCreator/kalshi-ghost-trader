package store

// AllModels is the complete list of table structs managed by AutoMigrate.
// Add new tables here — Migrate() iterates this slice.
var AllModels = []any{
	&Event{}, &Market{}, &Tick{}, &OrderbookEvent{},
	&LifecycleEvent{}, &EventLifecycleEvent{}, &Order{}, &Position{},
	&ScanRun{}, &FiredEvent{}, &Point{}, &KalshiScore{},
	&RuntimeConfig{}, &RuntimeConfigHistory{}, &LiquidityPool{}, &StrategyConfigEntry{},
	&TriggerRange{}, &FlashscoreMatch{}, &PriceBandResultRow{},
	&BacktestResultRow{},
	&SchemaMigration{},
}
