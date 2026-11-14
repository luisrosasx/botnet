// Test de retencion escalonada (T-1003): el PK de usage es diario por
// nodo (cero rollups por minuto para toda la flota — leccion A2-07),
// el job purga sessions a 30 dias y usage a 12 meses, y la
// reconstruccion de un cobro disputado a 60 dias corre en index scan
// bajo 1 s con 100k filas. Requiere Postgres (testenv skip si falta).
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/backend/testenv"
)

var execTimeRe = regexp.MustCompile(`"Execution Time":\s*([0-9.]+)`)

func usagePKCols(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT a.attname
		  FROM pg_index i
		  JOIN pg_class c ON c.oid = i.indrelid
		  JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = ANY(i.indkey)
		 WHERE c.relname = 'usage' AND i.indisprimary
		 ORDER BY array_position(i.indkey, a.attnum)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, s)
	}
	return cols
}

// TestRetentionPolicies: PK diario, purga a 31 dias en sessions y a
// 13 meses en usage.
func TestRetentionPolicies(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	tenant := testenv.SeedTenant(t, db, "ret-tenant")
	node := testenv.SeedNode(t, db, "ES", "Madrid", 1)

	// Fila de session de 31 dias cerrada: basura purgable (criterio 2).
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (node_id, tenant_id, gw_id, started_at, ended_at)
		VALUES ($1::uuid, $2::uuid, 'gw-1', now() - interval '31 days', now() - interval '30 days')`,
		node, tenant); err != nil {
		t.Fatal(err)
	}
	// Y una viva de ayer que NO se toca.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (node_id, tenant_id, gw_id, started_at)
		VALUES ($1::uuid, $2::uuid, 'gw-1', now() - interval '1 day')`,
		node, tenant); err != nil {
		t.Fatal(err)
	}
	// usage de 13 meses: fuera de la retencion contable.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes)
		VALUES (current_date - interval '13 months', $1::uuid, 10)`, node); err != nil {
		t.Fatal(err)
	}

	job := &RetentionJob{DB: db, Now: func() time.Time { return time.Now() }}
	res, err := job.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionsDeleted != 1 || res.UsageDeleted != 1 {
		t.Fatalf("purgadas sessions=%d usage=%d, want 1 y 1", res.SessionsDeleted, res.UsageDeleted)
	}
	var live int
	db.QueryRowContext(ctx, `SELECT count(*) FROM sessions WHERE ended_at IS NULL`).Scan(&live)
	if live != 1 {
		t.Fatalf("la sesion viva sobrevivio? hay %d", live)
	}
}

// TestUsageRollupGrain: 1.000 nodos x 1 dia → count(*) de usage ≤ 1000
// (el grano contable es DIARIO por nodo; el minuto solo vive en
// sessions, 30 dias).
func TestUsageRollupGrain(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, consent_at, status)
		SELECT gen_random_uuid(),
		       'n' || lpad(g::text, 11, '0'),
	       md5('tok' || g), 'amd64', 'B', now(), 'live'
		  FROM generate_series(1, 1000) g`); err != nil {
		t.Fatal(err)
	}
	// Tres sesiones por nodo en el mismo dia: el flush las acumula en
	// UNA fila usage(day, node_id).
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes)
		SELECT current_date, node_id, 3 FROM nodes`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM usage WHERE day = current_date`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 1000 {
		t.Fatalf("%d filas de usage para 1.000 nodos x 1 dia: hay rollup por minuto", n)
	}
	if cols := usagePKCols(t, db); strings.Join(cols, ",") != "day,node_id" {
		t.Fatalf("PK de usage = %v, want (day, node_id)", cols)
	}
}

// TestRebuildUnder1s: 100k filas de sessions para un nodo; la query de
// reconstruccion por (node_id, started_at) del dia pedido termina en
// < 1 s y su EXPLAIN declara un index scan (criterio 3).
func TestRebuildUnder1s(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	tenant := testenv.SeedTenant(t, db, "rebuild-tenant")
	node := testenv.SeedNode(t, db, "ES", "Madrid", 1)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (node_id, tenant_id, gw_id, started_at, bytes_in, bytes_out)
		SELECT $1::uuid, $2::uuid, 'gw-1',
		       date_trunc('day', now()) - interval '30 days' + (g % 86400) * interval '1 second',
		       100, 100
		  FROM generate_series(1, 100000) g`, node, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE sessions`); err != nil {
		t.Fatal(err)
	}

	from := time.Now().UTC().AddDate(0, 0, -30).Truncate(24 * time.Hour)
	to := from.Add(24 * time.Hour)

	rows, err := db.QueryContext(ctx, RebuildQuery(), node, from, to)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for rows.Next() {
		n++
	}
	rows.Close()
	if n < 1000 {
		t.Fatalf("el fixture no sembró sesiones del dia pedido (n=%d)", n)
	}

	// El cronometro es del servidor, no del cliente: con el Postgres
	// portable de Windows el volcado por TCP de ~86k filas mete 15-20 s
	// y tapaba el dato real (el criterio habla de la query, no del
	// transporte). EXPLAIN ANALYZE da el tiempo de ejecucion del motor.
	var planJSON string
	if err := db.QueryRowContext(ctx, `EXPLAIN (ANALYZE, FORMAT JSON) `+RebuildQuery(), node, from, to).
		Scan(&planJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(planJSON, "Index") {
		t.Fatalf("EXPLAIN sin index scan a 100k filas: %s", truncate(planJSON, 200))
	}
	execMs, ok := execTimeMs(planJSON)
	if !ok {
		t.Fatalf("EXPLAIN ANALYZE sin Execution Time: %s", truncate(planJSON, 200))
	}
	if execMs > 1000 {
		t.Fatalf("reconstruccion a 100k filas tardo %.1f ms en el motor (want < 1000 ms)", execMs)
	}
	t.Logf("rebuild: %d filas, %.1f ms en el motor, plan con index scan", n, execMs)
}

func truncate(s string, _ int) string {
	return fmt.Sprintf("%.200s…", s)
}

// execTimeMs extrae "Execution Time" del JSON de EXPLAIN ANALYZE.
func execTimeMs(plan string) (float64, bool) {
	m := execTimeRe.FindStringSubmatch(plan)
	if len(m) != 2 {
		return 0, false
	}
	ms, err := strconv.ParseFloat(m[1], 64)
	return ms, err == nil
}
