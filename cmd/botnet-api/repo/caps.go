// caps.go — metodos SQL de la sonda de ancho (EP-07 T-0703). Fichero
// nuevo del paquete repo: no se toca repo.go (otra epica lo owns en
// paralelo por contrato de la fase).
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SetBandwidthCap siembra el tope medido (kbps) en nodes. ErrNotFound
// si el nodo no existe: la sonda no crea inventario.
func (d *Data) SetBandwidthCap(ctx context.Context, nodeID string, kbps int) error {
	res, err := d.Q.ExecContext(ctx, `
		UPDATE nodes SET bandwidth_cap_kbps = $2::smallint
		 WHERE node_id = $1::uuid`, nodeID, kbps)
	if err != nil {
		return fmt.Errorf("repo: set bandwidth_cap: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// BandwidthCapKbps lee el tope vigente del nodo (assert del test y
// lectura del agente en el heartbeat).
func (d *Data) BandwidthCapKbps(ctx context.Context, nodeID string) (int, error) {
	var kbps int
	err := d.Q.QueryRowContext(ctx, `
		SELECT bandwidth_cap_kbps::int FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&kbps)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("repo: read bandwidth_cap: %w", err)
	}
	return kbps, nil
}

// CountSessionsForNode: numero de sesiones registradas del nodo. La
// sonda iperf3 no debe mover este contador ni una unidad (AC3: el
// delta durante la sonda es 0; es la misma pregunta que
// botnet_sessions_total en el runbook).
func (d *Data) CountSessionsForNode(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := d.Q.QueryRowContext(ctx, `
		SELECT count(*) FROM sessions WHERE node_id = $1::uuid`, nodeID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repo: count sessions: %w", err)
	}
	return n, nil
}
