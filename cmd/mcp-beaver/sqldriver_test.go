package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// umbra imports no database/sql driver on purpose, so which databases an image
// can reach is decided by what THIS binary links. Without this registration a
// `sql` grant parses, lints, and then fails every call.
func TestPostgresDriverIsRegistered(t *testing.T) {
	if drivers := sql.Drivers(); !slices.Contains(drivers, "pgx") {
		t.Fatalf("sql.Drivers() = %v, want it to include pgx", drivers)
	}
}

const sqlSpec = `wrap ward mcp analytics {
    database pgx { value literal "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1" }

    can list orders {
        sql {
            statement "SELECT id FROM orders WHERE customer = $1"
            param "customer" type="string" required=#true
        }
    }
}`

// A sql guardfile lints in a binary that links no driver, which is why umbra
// checks registration on first use rather than at parse.
func TestSQLGuardfileLints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "analytics.mcp.kdl")
	if err := os.WriteFile(path, []byte(sqlSpec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	var out strings.Builder
	if err := runLint(&out, []string{path}); err != nil {
		t.Fatalf("lint: %v", err)
	}
	if !strings.Contains(out.String(), "list_orders") {
		t.Fatalf("lint output = %q, want the sql-backed tool", out.String())
	}
}
