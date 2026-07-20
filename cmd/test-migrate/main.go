package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func main() {
	gdb, err := gorm.Open(sqlite.Open("/tmp/test_migrate3.db"), &gorm.Config{})
	if err != nil { panic(err) }
	gdb.Exec("CREATE TABLE IF NOT EXISTS events (event_ticker TEXT PRIMARY KEY, series_ticker TEXT, title TEXT, sub_title TEXT, competition TEXT, competition_scope TEXT, mutually_exclusive INTEGER, first_seen_ts INTEGER, last_updated_ts INTEGER, coverage TEXT)")
	gdb.Exec(`CREATE TABLE IF NOT EXISTS markets (
		market_ticker TEXT PRIMARY KEY,
		event_ticker TEXT NOT NULL,
		series_ticker TEXT NOT NULL,
		player_name TEXT NOT NULL,
		tennis_competitor TEXT,
		status TEXT NOT NULL,
		occurrence_ts INTEGER,
		open_ts INTEGER,
		close_ts INTEGER,
		result TEXT,
		settlement_ts INTEGER,
		settlement_value TEXT,
		first_seen_ts INTEGER NOT NULL,
		last_updated_ts INTEGER NOT NULL,
		FOREIGN KEY (event_ticker) REFERENCES events(event_ticker)
	)`)
	gdb.Exec("INSERT OR IGNORE INTO events VALUES ('E1','S','T','','','',0,0,0,'')")
	gdb.Exec("INSERT OR IGNORE INTO markets VALUES ('M1','E1','S','P','','active',0,0,0,'',0,'',0,0)")
	
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	_, err = store.New(context.Background(), "/tmp/test_migrate3.db", log)
	if err != nil { fmt.Println("store.New err:", err); return }
	fmt.Println("MIGRATE OK (FK in DB + FK tag in struct)")
}
