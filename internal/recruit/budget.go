package recruit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Presupuesto en tres unidades con granel y deuda acotada (BLUEPRINT §7).
//
// El pps se compra por granel: un worker pide N y la ventana los entrega
// aunque se pase del objetivo, dejando deuda. La deuda tiene techo
// (recruit_budget_debt, 5000) para que una racha no desboque el enlace.
// Nunca se queda uno sin trabajar por no haber saldo en el tick exacto.

// DeudaMaxima es el techo de pps que se puede deber en una ventana.
const DeudaMaxima = 5000

// ErrSinPresupuesto marca la ventana cerrada o inexistente.
var ErrSinPresupuesto = errors.New("recruit: sin ventana de presupuesto abierta")

// OpenWindow devuelve la ventana abierta, creandola si no existe.
func (s *Store) OpenWindow(ctx context.Context) (time.Time, int, error) {
	// Sellar antes de mirar: una ventana de la hora anterior ya no vale.
	if err := s.CerrarVentanas(ctx); err != nil {
		return time.Time{}, 0, err
	}
	var ws time.Time
	var budget int
	err := s.db.QueryRowContext(ctx, `
		SELECT window_start, pps_budget FROM recruit_windows
		 WHERE closed_at IS NULL ORDER BY window_start DESC LIMIT 1`).Scan(&ws, &budget)
	if err == nil {
		return ws, budget, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ws, 0, err
	}
	class, cerr := s.WindowRateClass(ctx)
	if cerr != nil {
		return ws, 0, cerr
	}
	if class == 0 {
		return ws, 0, ErrSinPresupuesto
	}
	if _, rerr := s.rollWindow(ctx, class); rerr != nil {
		return ws, 0, rerr
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT window_start, pps_budget FROM recruit_windows
		 WHERE closed_at IS NULL ORDER BY window_start DESC LIMIT 1`).Scan(&ws, &budget)
	return ws, budget, err
}

// Grant compra `n` pps de la ventana abierta con deuda acotada. Devuelve lo
// concedido (puede ser menos si toca el techo de deuda).
func (s *Store) Grant(ctx context.Context, n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}
	ws, budget, err := s.OpenWindow(ctx)
	if err != nil {
		return 0, err
	}
	var usado int
	if err := s.db.QueryRowContext(ctx, `
		SELECT pps_used FROM recruit_windows WHERE window_start = $1`, ws).Scan(&usado); err != nil {
		return 0, err
	}
	// Deuda disponible = techo - max(0, usado - budget).
	deuda := usado - budget
	if deuda < 0 {
		deuda = 0
	}
	margen := DeudaMaxima - deuda
	if margen <= 0 {
		return 0, nil
	}
	concedido := n
	if concedido > margen {
		concedido = margen
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE recruit_windows
		   SET pps_used = pps_used + $2, pps_granted = pps_granted + $2,
		       pps_debt = GREATEST(pps_used + $2 - pps_budget, 0)
		 WHERE window_start = $1 AND pps_used - pps_budget + $2 <= $3`,
		ws, concedido, DeudaMaxima)
	if err != nil {
		return 0, err
	}
	if n2, _ := res.RowsAffected(); n2 == 0 {
		return 0, nil
	}
	return concedido, nil
}

// IntentosPorDestino cuenta los intentos de credencial o entrega sobre la
// misma IP de destino en la hora en curso. El tope es compartido por las dos
// etapas: un router aguanta tres golpes por hora, no seis.
const IntentosPorDestino = 3

// PuedeIntentar dice si cabe otro intento contra esa IP en esta hora.
func (s *Store) PuedeIntentar(ctx context.Context, ip string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_jobs j
		  JOIN recruit_candidates c USING (candidate_id)
		 WHERE c.ip = $1::inet AND j.stage IN ('cred','deliver')
		   AND date_trunc('hour', j.claimed_at) = date_trunc('hour', now())`, ip).Scan(&n)
	if err != nil {
		return false, err
	}
	return n < IntentosPorDestino, nil
}

// EffectiveRatio da pps_effective/pps_budget de la ventana (0 si no hay).
func (s *Store) EffectiveRatio(ctx context.Context) (float64, error) {
	var usado, budget int
	err := s.db.QueryRowContext(ctx, `
		SELECT pps_used, pps_budget FROM recruit_windows
		 WHERE closed_at IS NULL ORDER BY window_start DESC LIMIT 1`).Scan(&usado, &budget)
	if err != nil {
		return 0, err
	}
	if budget == 0 {
		return 0, nil
	}
	r := float64(usado) / float64(budget)
	if r > 1 {
		r = 1
	}
	return r, nil
}

// CerrarVentanas vencidas: deja una sola abierta y sella las anteriores con
// hora posterior a su inicio (el CHECK del gate).
func (s *Store) CerrarVentanas(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE recruit_windows SET closed_at = now()
		 WHERE closed_at IS NULL AND window_start < date_trunc('hour', now())`)
	return err
}

// CheckVentanas valida las invariantes de la tabla (alineacion y cierre).
func (s *Store) CheckVentanas(ctx context.Context) (desalineadas, cerradasMal int, err error) {
	if err = s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_windows
		 WHERE date_trunc('hour', window_start) <> window_start`).Scan(&desalineadas); err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_windows
		 WHERE closed_at IS NOT NULL AND closed_at <= window_start`).Scan(&cerradasMal)
	return
}

var _ = fmt.Sprintf
