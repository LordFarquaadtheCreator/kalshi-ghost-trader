-- Fix DBs where AutoMigrate created trigger_ranges before 0017 could run.
-- Both strategy_trigger_ranges (old, has data) and trigger_ranges (new, empty)
-- exist. Copy data from old to new, drop old.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_tables WHERE tablename = 'strategy_trigger_ranges') THEN
    INSERT INTO trigger_ranges
    SELECT * FROM strategy_trigger_ranges
    ON CONFLICT DO NOTHING;
    DROP TABLE strategy_trigger_ranges;
  END IF;
END $$;
