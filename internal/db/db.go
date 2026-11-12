// Package db abre el Postgres del backend con SQL plano (sin ORM,
// BLUEPRINT §9) y ofrece un contador de queries: la prueba de T-0804
// exige "exactamente 1 query" por validacion fria, y el contador del
// wrapper es la medida determinista (pg_stat_statements es la
// corroboracion cuando el cluster la tiene cargada).
package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open abre un pool pgx sobre el DSN y hace ping con deadline.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}
	conn.SetMaxOpenConns(8)
	conn.SetMaxIdleConns(2)
	conn.SetConnMaxLifetime(10 * time.Minute)
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.PingContext(pctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return conn, nil
}

// Querier es el minimo que consumen los repos del backend; Counted lo
// instrumenta sin que el repo cambie.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Counted envuelve un Querier contando cada ida a Postgres.
type Counted struct {
	Q Querier
	N atomic.Int64
}

func (c *Counted) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	c.N.Add(1)
	return c.Q.ExecContext(ctx, query, args...)
}

func (c *Counted) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.N.Add(1)
	return c.Q.QueryContext(ctx, query, args...)
}

func (c *Counted) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.N.Add(1)
	return c.Q.QueryRowContext(ctx, query, args...)
}

// Hits devuelve el numero de queries contadas hasta ahora.
func (c *Counted) Hits() int64 { return c.N.Load() }

// Reset deja el contador a cero; el delta entre dos snapshots es la
// medida que exigen los criterios (delta = 1 en T-0804).
func (c *Counted) Reset() { c.N.Store(0) }
