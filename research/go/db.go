// Package main implements exploratory research modules that query the live
// kalshi_tennis.db read-only. Each subcommand runs a fresh analysis on
// whatever data is present — safe to re-run daily as the scraper accumulates.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// openDB opens the SQLite database read-only. Tries repo-root kalshi_tennis.db
// by default, overridable via -db flag.
func openDB(path string) *sql.DB {
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve db path: %v\n", err)
		os.Exit(1)
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=temp_store(MEMORY)", abs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "ping db: %v\n", err)
		os.Exit(1)
	}
	return db
}

// seriesPrefix extracts the series ticker prefix from an event/match ticker.
// e.g. "KXATPMATCH-26JUL15BUBHAL" -> "KXATPMATCH".
func seriesPrefix(ticker string) string {
	for i := 0; i < len(ticker)-3; i++ {
		if ticker[i] == '-' && i+2 < len(ticker) && ticker[i+1] == '2' && ticker[i+2] == '6' {
			return ticker[:i]
		}
	}
	return ticker
}

// isDoubles reports whether the ticker is a doubles series.
func isDoubles(ticker string) bool {
	p := seriesPrefix(ticker)
	return p == "KXATPDOUBLES" || p == "KXWTADOUBLES" || p == "KXITFDOUBLES" || p == "KXITFWDOUBLES"
}

// pct formats a fraction as a percentage string with one decimal.
func pct(num, den int) string {
	if den == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", 100.0*float64(num)/float64(den))
}

// pctF formats a float ratio as a percentage string with one decimal.
func pctF(ratio float64) string {
	return fmt.Sprintf("%.1f%%", 100.0*ratio)
}

// cents formats a price delta as cents.
func cents(p float64) string {
	return fmt.Sprintf("%.1fc", p*100)
}
