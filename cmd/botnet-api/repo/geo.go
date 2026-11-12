// geo.go — SQL de la sonda de geo del exit (EP-07 T-0704,
// migracion 0006). Fichero nuevo: repo.go no se toca.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NodeCountry: pais autorreportado del inventario — la ESPERANZA con
// la que casa la sonda. Cadenas vacias cuando el inventario no lo
// fijo (el veredicto cae a verified=false: sin esperanza no hay
// residencial que vender).
func (d *Data) NodeCountry(ctx context.Context, nodeID string) (string, error) {
	var country string
	err := d.Q.QueryRowContext(ctx, `
		SELECT COALESCE(country::text, '') FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&country)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("repo: node country: %w", err)
	}
	return country, nil
}

// ApplyGeoProbe escribe el veredicto en nodes (la puerta del pool) y
// deja la fila de historia en geo_probes. Dos sentencias, sin
// transaccion: nodes manda (el pool lee de ahi); geo_probes es
// auditoria y una fila perdida no rompe el contrato.
func (d *Data) ApplyGeoProbe(ctx context.Context, nodeID, country string, asn int, verified bool, bytesTX int) error {
	res, err := d.Q.ExecContext(ctx, `
		UPDATE nodes
		   SET country_probed = $2::char(2), asn_probed = $3,
		       geo_checked_at = now(), geo_verified = $4
		 WHERE node_id = $1::uuid`, nodeID, country, asn, verified)
	if err != nil {
		return fmt.Errorf("repo: apply geo nodes: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = d.Q.ExecContext(ctx, `
		INSERT INTO geo_probes (node_id, country, asn, verified, bytes_tx)
		VALUES ($1::uuid, $2::char(2), $3, $4, $5)`, nodeID, country, asn, verified, bytesTX)
	if err != nil {
		return fmt.Errorf("repo: apply geo history: %w", err)
	}
	return nil
}

// PickResidential: seleccion del pool «residencial» (AC3): SOLO nodos
// con geo_verified y pais SONDEADO igual al pedido — el autorreporte
// ya no se vende. Como PickNode: ("", nil) si el pool no tiene
// donde casar la sesion. El mas recientemente verificado primero:
// la cadencia semanal mantiene el orden vivo.
func (d *Data) PickResidential(ctx context.Context, country string) (string, error) {
	var id string
	err := d.Q.QueryRowContext(ctx, `
		SELECT node_id::text FROM nodes
		 WHERE geo_verified AND country_probed = $1::char(2) AND status = 'live'
		 ORDER BY geo_checked_at DESC NULLS LAST LIMIT 1`, country).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: pick residential: %w", err)
	}
	return id, nil
}

// ProbeStats: sondas y bytes tx de la sonda desde `since` (AC2:
// 7 dias -> 7 sondas; bytes ≤ 50 KB/nodo/mes).
func (d *Data) ProbeStats(ctx context.Context, nodeID string, since time.Time) (probes int, bytesTX int, err error) {
	err = d.Q.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(sum(bytes_tx), 0)::int FROM geo_probes
		 WHERE node_id = $1::uuid AND checked_at >= $2`, nodeID, since.UTC()).Scan(&probes, &bytesTX)
	if err != nil {
		return 0, 0, fmt.Errorf("repo: probe stats: %w", err)
	}
	return probes, bytesTX, nil
}
