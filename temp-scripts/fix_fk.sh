#!/bin/bash
set -euo pipefail
DB="${1:-kalshi_tennis.db}"

if [ ! -f "$DB" ]; then echo "DB not found: $DB"; exit 1; fi

echo "==> Backing up $DB"
cp "$DB" "${DB}.bak"

echo "==> Stripping FK constraints from events + markets (GORM manages schema)"
sqlite3 "$DB" << 'SQL'
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
DROP TRIGGER IF EXISTS trg_events_delete_cascade;
CREATE TABLE events_new (
  event_ticker TEXT PRIMARY KEY, series_ticker TEXT, title TEXT, sub_title TEXT,
  competition TEXT, competition_scope TEXT, mutually_exclusive NUMERIC,
  first_seen_ts INTEGER, last_updated_ts INTEGER, coverage TEXT
);
INSERT OR IGNORE INTO events_new SELECT * FROM events;
DROP TABLE events;
ALTER TABLE events_new RENAME TO events;
CREATE TABLE markets_new (
  market_ticker TEXT PRIMARY KEY, event_ticker TEXT,
  series_ticker TEXT, player_name TEXT, tennis_competitor TEXT,
  status TEXT, occurrence_ts INTEGER, open_ts INTEGER, close_ts INTEGER,
  result TEXT, settlement_ts INTEGER, settlement_value TEXT,
  first_seen_ts INTEGER, last_updated_ts INTEGER
);
INSERT OR IGNORE INTO markets_new SELECT * FROM markets;
DROP TABLE markets;
ALTER TABLE markets_new RENAME TO markets;
CREATE INDEX IF NOT EXISTS idx_markets_event ON markets(event_ticker);
CREATE INDEX IF NOT EXISTS idx_markets_series ON markets(series_ticker);
CREATE INDEX IF NOT EXISTS idx_markets_status ON markets(status);
CREATE INDEX IF NOT EXISTS idx_markets_occurrence ON markets(occurrence_ts);
CREATE TRIGGER IF NOT EXISTS trg_markets_delete_cascade
AFTER DELETE ON markets BEGIN
  DELETE FROM ticks WHERE market_ticker = OLD.market_ticker;
  DELETE FROM orderbook_events WHERE market_ticker = OLD.market_ticker;
  DELETE FROM lifecycle_events WHERE market_ticker = OLD.market_ticker;
END;
CREATE TRIGGER IF NOT EXISTS trg_events_delete_cascade
AFTER DELETE ON events BEGIN
  DELETE FROM markets WHERE event_ticker = OLD.event_ticker;
  DELETE FROM event_lifecycle_events WHERE event_ticker = OLD.event_ticker;
  DELETE FROM orders WHERE match_ticker = OLD.event_ticker;
  DELETE FROM fired_events WHERE event_ticker = OLD.event_ticker;
  DELETE FROM points WHERE match_ticker = OLD.event_ticker;
END;
COMMIT;
PRAGMA foreign_keys=ON;
SQL

echo "==> Verifying"
echo "  events schema:"
sqlite3 "$DB" ".schema events" | head -5
echo "  markets schema:"
sqlite3 "$DB" ".schema markets" | head -5
echo "==> Done. Removing backup."
rm -f "${DB}.bak"
