package store

// RuntimeConfig is a single key-value pair from app_config.
type RuntimeConfig struct {
	Key       string `gorm:"primaryKey;column:key"`
	Value     string `gorm:"column:value"`
	UpdatedTS int64  `gorm:"column:updated_ts"`
}

func (RuntimeConfig) TableName() string { return "app_config" }

// RuntimeConfigHistory records every change to an app_config key.
type RuntimeConfigHistory struct {
	ID        int64  `gorm:"primaryKey;autoIncrement;column:id"`
	Key       string `gorm:"column:key;index"`
	OldValue  string `gorm:"column:old_value"`
	NewValue  string `gorm:"column:new_value"`
	Action    string `gorm:"column:action"` // "set" or "delete"
	ChangedTS int64  `gorm:"column:changed_ts"`
}

func (RuntimeConfigHistory) TableName() string { return "app_config_history" }

// LiquidityPool is the singleton row from liquidity_pool.
type LiquidityPool struct {
	ID                  int64 `gorm:"primaryKey;column:id"`
	BalanceCents        int64 `gorm:"column:balance_cents"`
	InitialBalanceCents int64 `gorm:"column:initial_balance_cents"`
	TotalSpentCents     int64 `gorm:"column:total_spent_cents"`
	TotalPNLCents       int64 `gorm:"column:total_pnl_cents"`
	UpdatedTS           int64 `gorm:"column:updated_ts"`
}

func (LiquidityPool) TableName() string { return "liquidity_pool" }

// StrategyConfigEntry is one row from strategy_config.
type StrategyConfigEntry struct {
	Strategy  string `gorm:"primaryKey;column:strategy"`
	Enabled   bool   `gorm:"column:enabled"`
	UpdatedTS int64  `gorm:"column:updated_ts"`
}

func (StrategyConfigEntry) TableName() string { return "strategy_config" }

// TriggerRange is a price band for a strategy.
type TriggerRange struct {
	ID        int64   `gorm:"primaryKey;autoIncrement;column:id" json:"id,omitempty"`
	Strategy  string  `gorm:"column:strategy" json:"strategy,omitempty"`
	MinPrice  float64 `gorm:"column:min_price" json:"min_price"`
	MaxPrice  float64 `gorm:"column:max_price" json:"max_price"`
	Source    string  `gorm:"column:source" json:"source,omitempty"`
	Enabled   bool    `gorm:"column:enabled" json:"enabled"`
	CreatedTS int64   `gorm:"column:created_ts" json:"created_ts,omitempty"`
}

func (TriggerRange) TableName() string { return "strategy_trigger_ranges" }
