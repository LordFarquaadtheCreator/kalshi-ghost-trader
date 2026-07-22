-- C1: rename strategy_trigger_ranges -> trigger_ranges to match playbook intent.
-- Fresh installs skip this (AutoMigrate creates trigger_ranges directly via the
-- updated TableName). Already-applied DBs rename the existing table in place.
ALTER TABLE IF EXISTS strategy_trigger_ranges RENAME TO trigger_ranges;
