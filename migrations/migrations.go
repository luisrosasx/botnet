// Package migrations: DDL Postgres solo hacia adelante, 0001 -> 000N, sin
// ORM (BLUEPRINT §3, §9). Runner frugal embebido: quien abra un *sql.DB
// llama a Apply; la sustitución por la herramienta de EP-08 es compatible
// mientras respete schema_migrations.
package migrations

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.sql
var FS embed.FS

// Files devuelve los nombres .up.sql ordenados (0001_init.up.sql, ...).
func Files() ([]string, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations: read embed: %w", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Apply aplica en orden las migraciones .up.sql pendientes, cada una en su
// transaccion y registrada en schema_migrations. Idempotente por version.
func Apply(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("migrations: schema_migrations: %w", err)
	}
	names, err := Files()
	if err != nil {
		return err
	}
	for _, name := range names {
		var applied bool
		err := db.QueryRow(`SELECT true FROM schema_migrations WHERE version = $1`, name).Scan(&applied)
		if err == sql.ErrNoRows {
			applied = false
		} else if err != nil {
			return fmt.Errorf("migrations: probe %s: %w", name, err)
		}
		if applied {
			continue
		}
		stmt, err := FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("migrations: read %s: %w", name, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migrations: begin %s: %w", name, err)
		}
		if _, err := tx.Exec(string(stmt)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrations: apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrations: record %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrations: commit %s: %w", name, err)
		}
	}
	return nil
}
