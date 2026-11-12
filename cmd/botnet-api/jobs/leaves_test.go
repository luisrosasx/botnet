// Test de la memoria del opt-out (T-1007): el leave rellena
// reprobe_blocked_until a optout_at + 12 meses, la consulta de
// descubrimiento (T-0106) excluye al nodo que se bajo durante 11
// meses y lo devuelve al mes 13, y la guardia de CI cuenta 0
// re-clutajes dentro de la ventana. Requiere Postgres (skip si falta).
package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/backend/testenv"
	"github.com/botnet/botnet/internal/discovery"
)

func leavesSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var typ, isNull string
	if err := db.QueryRowContext(context.Background(), `
		SELECT format_type(atttypid, atttypmod), attnotnull
		  FROM pg_attribute
		 WHERE attrelid = 'leaves'::regclass AND attname = 'leave_code'`).
		Scan(&typ, &isNull); err != nil {
		t.Fatal(err)
	}
	if typ != "character(8)" {
		t.Fatalf("leave_code = %s, want char(8)", typ)
	}
	var len int
	db.QueryRowContext(context.Background(), `
		SELECT character_maximum_length FROM information_schema.columns
		 WHERE table_name = 'leaves' AND column_name = 'leave_code'`).Scan(&len)
	if len != 8 {
		t.Fatalf("leave_code char(%d), want char(8)", len)
	}
	db.QueryRowContext(context.Background(), `
		SELECT attnotnull FROM pg_attribute
		 WHERE attrelid = 'leaves'::regclass AND attname = 'reprobe_blocked_until'`).
		Scan(&isNull)
	if isNull != "true" {
		t.Fatal("reprobe_blocked_until debe ser NOT NULL (el opt-out siempre trae fecha)")
	}
	var cons string
	db.QueryRowContext(context.Background(), `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		 WHERE conrelid = 'leaves'::regclass AND contype = 'c'
		   AND pg_get_constraintdef(oid) LIKE '%method%'`).Scan(&cons)
	for _, m := range []string{"http", "heartbeat_bye", "panel", "owner_url"} {
		if !contains(cons, "'"+m+"'") {
			t.Fatalf("CHECK de method sin %q: %s", m, cons)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestOptoutMemoryYDescubrimiento: opt-out ayer → job rellena +12
// meses; el barrido da 0 candidatos durante los 11 meses y devuelve
// el IP al mes 13.
func TestOptoutMemoryYDescubrimiento(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	leavesSchema(t, db)

	node := testenv.SeedNode(t, db, "ES", "Madrid", 64500)
	if _, err := db.ExecContext(ctx, `
		UPDATE nodes SET optout_at = now() - interval '1 day',
		       public_ip = '203.0.113.77'::inet
		 WHERE node_id = $1::uuid`, node); err != nil {
		t.Fatal(err)
	}
	// Candidato del barrido sobre la IP del nodo que se bajo.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO discovered (ip, port, service, asn, country, last_seen_at)
		VALUES ('203.0.113.77'::inet, 23, 'telnet', 64500, 'ES', now())`); err != nil {
		t.Fatal(err)
	}

	job := &OptoutMemoryJob{DB: db, Now: func() time.Time { return time.Now() }}
	res, err := job.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Inserted != 1 {
		t.Fatalf("leave insertado: %d, want 1", res.Inserted)
	}

	// El opt-out rellena el bloqueo a +12 meses (aserto de la job).
	var exacto bool
	if err := db.QueryRowContext(ctx, `
		SELECT reprobe_blocked_until = optout_at + interval '12 months'
		  FROM leaves l JOIN nodes n USING (node_id) WHERE n.node_id = $1::uuid`,
		node).Scan(&exacto); err != nil {
		t.Fatal(err)
	}
	if !exacto {
		t.Fatal("reprobe_blocked_until no es optout_at + 12 meses")
	}

	// El barrido (T-0106) no lo devuelve: 11 meses de memoria.
	q := discovery.SelectQueryWithOptout()
	candidatos := func() int {
		rows, err := db.QueryContext(ctx, q, 50)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var n int
		for rows.Next() {
			n++
		}
		return n
	}
	if n := candidatos(); n != 0 {
		t.Fatalf("mes 1 tras el opt-out: %d candidatos, want 0", n)
	}

	// Mes 13: la memoria vencio y el IP vuelve al barrido.
	if _, err := db.ExecContext(ctx, `
		UPDATE nodes SET optout_at = now() - interval '13 months' WHERE node_id = $1::uuid`,
		node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE leaves l SET left_at = n.optout_at,
		       reprobe_blocked_until = n.optout_at + interval '12 months'
		  FROM nodes n WHERE l.node_id = n.node_id AND n.node_id = $1::uuid`,
		node); err != nil {
		t.Fatal(err)
	}
	if n := candidatos(); n != 1 {
		t.Fatalf("mes 13 tras el opt-out: %d candidatos, want 1", n)
	}

	// La guardia de CI: sin re-clutaje, 0 violaciones; con el nodo
	// re-enrolado a 1 mes del opt-out, la job no borra nada pero el
	// guard lo ve (aserta el SQL, no el script).
	var viol int
	if err := db.QueryRowContext(ctx, ReprobeGuardCount).Scan(&viol); err != nil {
		t.Fatal(err)
	}
	if viol != 0 {
		t.Fatalf("violaciones sin re-clutaje: %d, want 0", viol)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE nodes SET enrolled_at = optout_at + interval '1 month'
		 WHERE node_id = $1::uuid`, node); err != nil {
		t.Fatal(err)
	}
	db.QueryRowContext(ctx, ReprobeGuardCount).Scan(&viol)
	if viol != 1 {
		t.Fatalf("guard ciego ante re-clutaje a 1 mes: %d, want 1", viol)
	}
}

// TestOptoutMemoryIdempotente: correr la job dos veces no duplica
// leaves ni estira bloqueos que ya cumplen los 12 meses.
func TestOptoutMemoryIdempotente(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	node := testenv.SeedNode(t, db, "ES", "Madrid", 64501)
	db.ExecContext(ctx, `UPDATE nodes SET optout_at = now() - interval '2 days' WHERE node_id = $1::uuid`, node)
	job := &OptoutMemoryJob{DB: db}
	if _, err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	seg, err := job.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seg.Inserted != 0 || seg.Extended != 0 {
		t.Fatalf("segunda pasada: %+v, want 0/0", seg)
	}
	var n int
	db.QueryRowContext(ctx, `SELECT count(*) FROM leaves WHERE node_id = $1::uuid`, node).Scan(&n)
	if n != 1 {
		t.Fatalf("%d leaves para un opt-out", n)
	}
}
