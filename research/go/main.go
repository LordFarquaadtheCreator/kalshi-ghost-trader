package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

// Module is a research subcommand. Each RQ registers one.
type Module interface {
	Name() string
	Desc() string
	Run(db *sql.DB, args []string)
}

var modules = map[string]Module{}

func register(m Module) {
	modules[m.Name()] = m
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: research <module> [flags]\n\nmodules:\n")
	names := make([]string, 0, len(modules))
	for n := range modules {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(os.Stderr, "  %-28s %s\n", n, modules[n].Desc())
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]
	m, ok := modules[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown module %q\n", name)
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet(name, flag.ExitOnError)
	dsn := fs.String("dsn", "", "PostgreSQL DSN (defaults to dev config)")
	fs.Parse(os.Args[2:])

	db := openDB(*dsn)
	defer db.Close()

	t0 := time.Now()
	m.Run(db, fs.Args())
	fmt.Fprintf(os.Stderr, "\n[done in %s]\n", time.Since(t0).Round(time.Millisecond))
}
