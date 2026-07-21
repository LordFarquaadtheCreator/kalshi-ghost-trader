package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/farquaad/kalshi-ghost-trader/internal/store"
)

func main() {
	var (
		dbPath string
		dryRun bool
		apply  string
		list   bool
		only   string
	)
	flag.StringVar(&dbPath, "db", "kalshi_tennis.db", "path to SQLite database")
	flag.BoolVar(&dryRun, "dry-run", false, "show what would be applied without executing")
	flag.StringVar(&apply, "apply", "", "apply specific migration by filename (e.g. 0001_initial_indexes_triggers.sql)")
	flag.BoolVar(&list, "list", false, "list all migrations and their status")
	flag.StringVar(&only, "only", "", "apply only this migration filename (skip all others)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, err := store.New(context.TODO(), dbPath, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if list {
		all, err := store.ListAllMigrations()
		if err != nil {
			fmt.Fprintf(os.Stderr, "list all migrations: %v\n", err)
			os.Exit(1)
		}
		appliedSet, err := db.ListAllDBMigrations()
		if err != nil {
			fmt.Fprintf(os.Stderr, "list db migrations: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%-50s  %s\n", "MIGRATION", "STATUS")
		fmt.Println(strings.Repeat("-", 70))
		for _, m := range all {
			status := "pending"
			if appliedSet[m.Name] {
				status = "applied"
			}
			fmt.Printf("%-50s  %s\n", m.Name, status)
		}
		return
	}

	unapplied, err := db.ListUnappliedMigrations()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list unapplied migrations: %v\n", err)
		os.Exit(1)
	}

	switch {
	case apply != "":
		for _, m := range unapplied {
			if m.Name == apply {
				if dryRun {
					fmt.Printf("DRY RUN — would apply %s:\n\n%s\n", m.Name, m.SQL)
					return
				}
				if err := db.RunMigration(m.Name, m.SQL); err != nil {
					fmt.Fprintf(os.Stderr, "apply %s: %v\n", m.Name, err)
					os.Exit(1)
				}
				fmt.Printf("applied %s\n", m.Name)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "migration %s not found or already applied\n", apply)
		os.Exit(1)

	case only != "":
		for _, m := range unapplied {
			if m.Name != only {
				continue
			}
			if dryRun {
				fmt.Printf("DRY RUN — would apply %s:\n\n%s\n", m.Name, m.SQL)
				return
			}
			if err := db.RunMigration(m.Name, m.SQL); err != nil {
				fmt.Fprintf(os.Stderr, "apply %s: %v\n", m.Name, err)
				os.Exit(1)
			}
			fmt.Printf("applied %s\n", m.Name)
			return
		}
		fmt.Fprintf(os.Stderr, "migration %s not found or already applied\n", only)
		os.Exit(1)

	default:
		if len(unapplied) == 0 {
			fmt.Println("all migrations applied — nothing to do")
			return
		}
		if dryRun {
			fmt.Printf("DRY RUN — %d pending migration(s):\n\n", len(unapplied))
			for _, m := range unapplied {
				fmt.Printf("=== %s ===\n\n%s\n\n", m.Name, m.SQL)
			}
			return
		}
		for _, m := range unapplied {
			if err := db.RunMigration(m.Name, m.SQL); err != nil {
				fmt.Fprintf(os.Stderr, "apply %s: %v\n", m.Name, err)
				os.Exit(1)
			}
			fmt.Printf("applied %s\n", m.Name)
		}
		fmt.Printf("\n%d migration(s) applied\n", len(unapplied))
	}
}
