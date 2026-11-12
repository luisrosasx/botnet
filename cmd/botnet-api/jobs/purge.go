// purge.go — borrado RGPD con retencion contable (EP-11 T-1103,
// cierra A3-X5): retirarse borra identidad, conserva contabilidad.
// El suelo legal del blueprint §13: `mac`, `device_token` y
// `public_ip` con fecha de borrado; `device_token` jamas existio en
// claro (char(64) hash), `mac_hash` y `device_token_hash` sobreviven
// como hashes y `deleted_at` sella la fila. La tabla `usage` (12
// meses de retencion, jobs/retention.go) NO se toca: borrar persona
// no borra factura.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RGPDAfterOptoutSQL: la identidad del retirado se purga 13 meses
// despues de su baja (SQL de calendario real, no 13x30 dias): los 12
// meses de retencion contable + 1 de margen de disputa.
const RGPDAfterOptoutSQL = "interval '13 months'"

type PurgeJob struct {
	DB  *sql.DB
	Now func() time.Time // reloj inyectable para el fixture (nil = now() de Postgres)
}

// PurgeResult: filas de nodo purgadas — el assert del fixture.
type PurgeResult struct {
	NodesPurged int
}

// Run: public_ip a NULL, deleted_at sellado, hashes INTACTOS (la
// consulta ni los nombra). Solo nodos con baja (leaves.left_at) mas
// antigua que la retencion; la fila contable `usage` queda presente
// para su propio ciclo de 12 meses.
func (j *PurgeJob) Run(ctx context.Context) (PurgeResult, error) {
	var res PurgeResult
	r, err := j.DB.ExecContext(ctx, `
		UPDATE nodes
		   SET public_ip = NULL,
		       deleted_at = COALESCE(deleted_at, now())
		 WHERE deleted_at IS NULL
		   AND EXISTS (SELECT 1 FROM leaves l
		                WHERE l.node_id = nodes.node_id
		                  AND l.left_at < now() - `+RGPDAfterOptoutSQL+`)`)
	if err != nil {
		return res, fmt.Errorf("jobs: purge rgpd: %w", err)
	}
	n, err := r.RowsAffected()
	res.NodesPurged = int(n)
	return res, err
}

// RGPDCheck es el doble de `make rgpd-check`: siembra el fixture (un
// retirado con baja hace 13 meses, public_ip puesta y facturas de su
// ultimo ano), corre el job y audita los 5 asserts con SQL directo.
// Idempotente: el fixture se reescribe en cada carrera.
func RGPDCheck(ctx context.Context, db *sql.DB) error {
	const slug = "rgpdcheck001"
	var nodeID string
	err := db.QueryRowContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, status, public_ip, mac_hash, consent_at, enrolled_at)
		VALUES (gen_random_uuid(), $1::char(12), repeat('a',64), 'armv7', 'B', 'retired', '1.2.3.4'::inet, decode('aabb','hex'), now() - interval '14 months', now() - interval '14 months')
		ON CONFLICT (slug) DO UPDATE SET public_ip = '1.2.3.4'::inet, deleted_at = NULL, status = 'retired'
		RETURNING node_id::text`, slug).Scan(&nodeID)
	if err != nil {
		return fmt.Errorf("rgpd-check: fixture nodo: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		DELETE FROM leaves WHERE node_id = $1::uuid AND leave_code = 'RGPD0001'`, nodeID); err != nil {
		return fmt.Errorf("rgpd-check: fixture leave (limpieza): %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO leaves (node_id, method, leave_code, left_at, reprobe_blocked_until)
		VALUES ($1::uuid, 'http', 'RGPD0001', now() - interval '13 months' - interval '1 day', now())`, nodeID); err != nil {
		return fmt.Errorf("rgpd-check: fixture leave: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes)
		VALUES (CURRENT_DATE - interval '13 months', $1::uuid, 1000000000)
		ON CONFLICT (day, node_id) DO NOTHING`, nodeID); err != nil {
		return fmt.Errorf("rgpd-check: fixture usage: %w", err)
	}

	if _, err := (&PurgeJob{DB: db}).Run(ctx); err != nil {
		return err
	}

	var publicIPNull, macNotNull, tokenHashNotNull, deletedNotNull bool
	if err := db.QueryRowContext(ctx, `
		SELECT public_ip IS NULL,
		       mac_hash IS NOT NULL,
		       device_token_hash IS NOT NULL,
		       deleted_at IS NOT NULL
		  FROM nodes WHERE slug = $1::char(12)`, slug).
		Scan(&publicIPNull, &macNotNull, &tokenHashNotNull, &deletedNotNull); err != nil {
		return fmt.Errorf("rgpd-check: auditoria fila: %w", err)
	}
	if !(publicIPNull && macNotNull && tokenHashNotNull && deletedNotNull) {
		return fmt.Errorf("rgpd-check: asserts de identidad rotos: public_ip NULL=%v mac_hash=%v token_hash=%v deleted_at=%v",
			publicIPNull, macNotNull, tokenHashNotNull, deletedNotNull)
	}
	var usageRows int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM usage WHERE node_id = $1::uuid`, nodeID).Scan(&usageRows); err != nil {
		return fmt.Errorf("rgpd-check: auditoria usage: %w", err)
	}
	if usageRows < 1 {
		return fmt.Errorf("rgpd-check: la fila contable del retiro no sobrevivio (%d filas)", usageRows)
	}
	var clearCols int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM information_schema.columns
		 WHERE table_name = 'nodes' AND column_name = 'device_token'`).Scan(&clearCols); err != nil {
		return fmt.Errorf("rgpd-check: auditoria columnas: %w", err)
	}
	if clearCols != 0 {
		return fmt.Errorf("rgpd-check: el device_token en claro sigue teniendo columna en nodes")
	}
	fmt.Printf("rgpd-check: OK (public_ip NULL, hashes intactos, deleted_at sellado, usage del retiro presente, 0 columnas en claro)\n")
	return nil
}
