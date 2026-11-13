// leave.go — SQL de la puerta legal (EP-07 T-0706, A3-13). Fichero
// nuevo: la 0006 anadio nodes.leave_code; leaves ya existia en 0003.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LeaveCode: el codigo char(8) del nodo (RTRIM: char rellena a
// espacios). ErrNotFound si el nodo no existe; "" si operations todavia
// no le emite codigo — sin codigo no hay puerta http (todo codigo
// casa a 404).
func (d *Data) LeaveCode(ctx context.Context, nodeID string) (string, error) {
	code, _, err := d.LeaveCodeIssued(ctx, nodeID)
	return code, err
}

// LeaveCodeIssued devuelve además cuándo se emitió el código (el alta):
// el handler lo compara con la caducidad configurable (S-18).
func (d *Data) LeaveCodeIssued(ctx context.Context, nodeID string) (string, time.Time, error) {
	var (
		code   string
		issued time.Time
	)
	err := d.Q.QueryRowContext(ctx, `
		SELECT COALESCE(RTRIM(leave_code::text), ''), enrolled_at
		FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&code, &issued)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrNotFound
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("repo: leave code: %w", err)
	}
	return code, issued, nil
}

// MarkOptout estampa la fecha de baja en la fila del nodo (EP-11
// T-1102): optout_at solo se pone la primera vez (COALESCE), y 0 filas
// significa nodo inexistente → ErrNotFound.
func (d *Data) MarkOptout(ctx context.Context, nodeID string) error {
	res, err := d.Q.ExecContext(ctx, `
		UPDATE nodes SET optout_at = COALESCE(optout_at, now())
		 WHERE node_id = $1::uuid`, nodeID)
	if err != nil {
		return fmt.Errorf("repo: mark optout: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertLeave: historia de la baja (leaves, 0003). method CHECK admite
// 'http' — la puerta del dueño por API. reprobe_blocked_until a +12
// meses: el opt-out no se re-cluta (0005 deja el indice para la
// consulta de descubrimiento).
func (d *Data) InsertLeave(ctx context.Context, nodeID, method, reason, code string) error {
	res, err := d.Q.ExecContext(ctx, `
		INSERT INTO leaves (node_id, method, reason, leave_code, reprobe_blocked_until)
		VALUES ($1::uuid, $2, $3, $4::char(8), now() + interval '12 months')`, nodeID, method, reason, code)
	if err != nil {
		return fmt.Errorf("repo: insert leave: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
