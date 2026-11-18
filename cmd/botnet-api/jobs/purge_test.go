// Test T-1103 (EP-11): el job purge borra identidad del retirado de
// hace 13 meses y conserva la contabilidad (A3-X5).
package jobs

import (
	"context"
	"testing"

	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestPurgeRGPDFixture(t *testing.T) {
	db := testenv.PG(t)
	if err := RGPDCheck(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	// El mismo fixture dos veces: idempotencia del check (la segunda
	// carrera re-siembra y vuelve a purgar sin quejarse).
	if err := RGPDCheck(context.Background(), db); err != nil {
		t.Fatalf("rgpd-check no idempotente: %v", err)
	}
}

// TestPurgePublicIP (T-1904 AC4): la IP se anula solo pasado el plazo.
func TestPurgePublicIP(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	nodo := func(slug string, meses int) string {
		var id string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, consent_at, public_ip, optout_at, status)
			VALUES (gen_random_uuid(), $1::varchar, md5($1::varchar), 'armv7', 'B', now() - make_interval(months => $2),
			        '198.51.100.9', now() - make_interval(months => $2), 'retired')
			RETURNING node_id::text`, slug, meses).Scan(&id); err != nil {
			t.Fatalf("nodo %s: %v", slug, err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO leaves (node_id, left_at, method, reprobe_blocked_until) VALUES ($1::uuid, now() - make_interval(months => $2), 'panel', now() + interval '12 months')`,
			id, meses); err != nil {
			t.Fatalf("leave %s: %v", slug, err)
		}
		return id
	}
	reciente := nodo("purge12m", 12)
	viejo := nodo("purge14m", 14)

	job := &PurgeJob{DB: db}
	if _, err := job.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	var nulaReciente, nulaVieja bool
	db.QueryRowContext(ctx, `SELECT public_ip IS NULL FROM nodes WHERE node_id=$1::uuid`, reciente).Scan(&nulaReciente)
	db.QueryRowContext(ctx, `SELECT public_ip IS NULL FROM nodes WHERE node_id=$1::uuid`, viejo).Scan(&nulaVieja)
	if nulaReciente {
		t.Error("a los 12 meses la IP todavia no debe anularse")
	}
	if !nulaVieja {
		t.Error("a los 14 meses la IP debe quedar a NULL")
	}
}
