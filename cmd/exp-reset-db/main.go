package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

func main() {
	var dsn string
	flag.StringVar(&dsn, "db-dsn", "", "PostgreSQL DSN to reset")
	flag.Parse()

	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "db-dsn is required")
		os.Exit(2)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db failed: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "ping db failed: %v\n", err)
		os.Exit(1)
	}

	stmts := []string{
		"DROP TABLE IF EXISTS task_results CASCADE",
		"DROP TABLE IF EXISTS task_events CASCADE",
		"DROP TABLE IF EXISTS user_data_files CASCADE",
		"DROP TABLE IF EXISTS snapshots CASCADE",
		"DROP TABLE IF EXISTS containers CASCADE",
		"DROP TABLE IF EXISTS tasks CASCADE",
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			fmt.Fprintf(os.Stderr, "exec %q failed: %v\n", stmt, err)
			os.Exit(1)
		}
	}
}
