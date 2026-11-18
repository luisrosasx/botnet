// Prueba de la 0003/0004 (T-0801): aplica el DDL del blueprint sobre una
// base scratch del Postgres portable (DATABASE_URL) y verifica las
// preguntas contables con sus CHECK/UNIQUE. Sin DATABASE_URL se omite
// (el build global pasa sin motor); el runbook documenta el comando.
package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/migrations"
)

func TestApplyOnCleanDatabase(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		// Gate G4: omitir daba verde mudo; una migracion que nunca se
		// aplica en el test tiene que doler.
		t.Fatal("DATABASE_URL sin definir: la prueba de DDL no se omite")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	defer admin.Close()

	// El Postgres portable del host tiene ventanas de dial-timeout
	// transitorias; sin reintento el test se pone rojo por el motor y no
	// por el DDL.
	exec := func(q string) error {
		var e error
		for i := 0; i < 5; i++ {
			if _, e = admin.ExecContext(ctx, q); e == nil || strings.Contains(e.Error(), "already exists") {
				return e
			}
			time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
		}
		return e
	}

	scratch := fmt.Sprintf("botnet_ep08_ddl_%d", os.Getpid())
	if err := exec("DROP DATABASE IF EXISTS " + scratch + " WITH (FORCE)"); err != nil {
		t.Fatalf("drop scratch: %v", err)
	}
	defer admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+scratch+" WITH (FORCE)")
	if err := exec("CREATE DATABASE " + scratch); err != nil {
		t.Fatalf("create scratch: %v", err)
	}

	clean, err := sql.Open("pgx", replaceDB(dsn, scratch))
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	defer clean.Close()
	// Apply tambien se reintenta: el reset del motor portable llega al leer
	// el fichero de migracion a mitad y aborta la transaccion. Es el motor,
	// no el DDL (visto como `wsarecv: forcibly closed` en 0001_init.up.sql).
	apply := func() error {
		var e error
		for i := 0; i < 5; i++ {
			if e = migrations.Apply(clean); e == nil {
				return nil
			}
			if !esResetDeTransporte(e) {
				return e
			}
			time.Sleep(time.Duration(i+1) * 400 * time.Millisecond)
		}
		return e
	}
	if err := apply(); err != nil {
		t.Fatalf("Apply en base limpia: %v", err)
	}
	// Idempotencia por version: un segundo Apply no vuelve a aplicar.
	if err := apply(); err != nil {
		t.Fatalf("Apply idempotente: %v", err)
	}

	// AC1: las ocho tablas del nucleo existen.
	for _, tbl := range []string{"tenants", "api_keys", "nodes", "node_secrets", "sessions", "usage", "quotas", "leaves"} {
		var ok bool
		if err := clean.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+tbl).Scan(&ok); err != nil || !ok {
			t.Errorf("tabla %s: existe=%v err=%v", tbl, ok, err)
		}
	}

	// AC1: nodes.slug char(12) UNIQUE y device_token_hash char(64) UNIQUE.
	var char12, char64 int
	if err := clean.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='nodes'
		   AND column_name='slug' AND data_type='character'
		   AND character_maximum_length=12`).Scan(&char12); err != nil || char12 != 1 {
		t.Errorf("nodes.slug char(12): n=%d err=%v", char12, err)
	}
	if err := clean.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_schema='public' AND table_name='nodes'
		   AND column_name='device_token_hash' AND data_type='character'
		   AND character_maximum_length=64`).Scan(&char64); err != nil || char64 != 1 {
		t.Errorf("nodes.device_token_hash char(64): n=%d err=%v", char64, err)
	}
	for _, col := range []string{"slug", "device_token_hash"} {
		var has bool
		if err := clean.QueryRowContext(ctx, `
			SELECT (SELECT count(*) FROM pg_constraint c
			          JOIN pg_attribute a ON a.attrelid=c.conrelid AND a.attnum = ANY(c.conkey)
			         WHERE c.conrelid='public.nodes'::regclass AND c.contype='u' AND a.attname=$1) > 0`, col).Scan(&has); err != nil || !has {
			t.Errorf("nodes.%s UNIQUE: %v (has=%v)", col, err, has)
		}
	}

	// AC2: octet_length(secret)=32 respetado y un unico secreto vivo por nodo.
	var nodeID string
	if _, err := clean.ExecContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, consent_at)
		VALUES (gen_random_uuid(), 'ep08ddl00001', repeat('a',64), 'mipsbe', 'B', now())`); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	if err := clean.QueryRowContext(ctx, `SELECT node_id::text FROM nodes WHERE slug='ep08ddl00001'`).Scan(&nodeID); err != nil {
		t.Fatalf("node_id: %v", err)
	}
	if _, err := clean.ExecContext(ctx, `
		INSERT INTO node_secrets (node_id, secret, expires_at)
		VALUES ($1, decode(repeat('ab',31),'hex'), now() + interval '90 days')`, nodeID); err == nil {
		t.Error("secreto de 31 B debe violar CHECK octet_length(secret)=32")
	}
	if _, err := clean.ExecContext(ctx, `
		INSERT INTO node_secrets (node_id, secret, expires_at)
		VALUES ($1, decode(repeat('ab',32),'hex'), now() + interval '90 days')`, nodeID); err != nil {
		t.Fatalf("insert secreto vivo: %v", err)
	}
	_, err = clean.ExecContext(ctx, `
		INSERT INTO node_secrets (node_id, secret, expires_at)
		VALUES ($1, decode(repeat('cd',32),'hex'), now() + interval '90 days')`, nodeID)
	if err == nil || !strings.Contains(err.Error(), "unique") {
		t.Errorf("segundo secreto vivo del mismo nodo debe fallar por UNIQUE parcial; err=%v", err)
	}

	// AC3: sin *_down.sql — el runner solo conoce .up.sql.
	for _, name := range mustFiles(t) {
		if strings.HasSuffix(name, "_down.sql") {
			t.Errorf("migracion hacia atras prohibida: %s", name)
		}
	}
	for _, want := range []string{"0003_core.up.sql", "0004_indexes.up.sql"} {
		var applied bool
		if err := clean.QueryRowContext(ctx, `SELECT true FROM schema_migrations WHERE version=$1`, want).Scan(&applied); err != nil || !applied {
			t.Errorf("%s registrada en schema_migrations: %v", want, err)
		}
	}
}

func mustFiles(t *testing.T) []string {
	t.Helper()
	names, err := migrations.Files()
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	return names
}

func replaceDB(dsn, newDB string) string {
	if i := strings.LastIndex(dsn, "/"); i >= 0 && !strings.Contains(dsn[i:], "?") {
		return dsn[:i] + "/" + newDB
	}
	// forma postgres://user@host:port/db?params
	i := strings.LastIndex(dsn, "/")
	j := strings.Index(dsn[i:], "?")
	if j < 0 {
		return dsn[:i+len("/"+newDB)]
	}
	return dsn[:i] + "/" + newDB + dsn[i+j:]
}

// esResetDeTransporte detecta el corte de conexion del motor portable (no
// del DDL): reset del socket, cierre forzado o timeout de dial.
func esResetDeTransporte(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, frag := range []string{"wsarecv", "forcibly closed", "connection reset", "broken pipe", "unexpected EOF", "connect: connection refused"} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}
