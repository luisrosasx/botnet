package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Politicas de retencion declaradas en BLUEPRINT §11 y el runbook
// docs/runbooks/retention.md: el grano minuto vive solo donde se paga
// (sessions, 30 dias); la tabla contable `usage` es diaria y dura 12
// meses (el cobro disputado a 60 dias se responde con holgura).
const (
	SessionsRetention = 30 * 24 * time.Hour
	UsageRetention    = 12 * 30 * 24 * time.Hour // 12 meses en dias
)

// RetentionJob purga por SQL plano (sin pg_partman en EP-10: cron
// diario del backend). Las edades se pasan como interval a Postgres
// para que el reloj autoritativo sea el de la BD cuando Now no se
// inyecta; los tests traen reloj propio.
type RetentionJob struct {
	DB       *sql.DB
	Sessions time.Duration // 0 => SessionsRetention
	Usage    time.Duration // 0 => UsageRetention
	Now      func() time.Time
}

func (j *RetentionJob) now() time.Time {
	if j.Now == nil {
		return time.Now()
	}
	return j.Now()
}

func (j *RetentionJob) sessionsAge() time.Duration {
	if j.Sessions <= 0 {
		return SessionsRetention
	}
	return j.Sessions
}

func (j *RetentionJob) usageAge() time.Duration {
	if j.Usage <= 0 {
		return UsageRetention
	}
	return j.Usage
}

// RetentionResult: filas purgadas por tabla, para la metrica y el
// assert del fixture (31 dias en sessions aparece purgado).
type RetentionResult struct {
	SessionsDeleted int
	UsageDeleted    int
}

// Run ejecuta la purga. Solo cierra sesiones terminadas: una sesion
// viva de hace 31 dias es anomalia operativa, no basura, y sessions
// sin ended_at no se toca (el dueno la esta pagando).
func (j *RetentionJob) Run(ctx context.Context) (RetentionResult, error) {
	var res RetentionResult
	now := j.now().UTC()

	cut := now.Add(-j.sessionsAge())
	r, err := j.DB.ExecContext(ctx, `
		DELETE FROM sessions
		 WHERE ended_at IS NOT NULL AND started_at < $1`, cut)
	if err != nil {
		return res, fmt.Errorf("retention: sessions: %w", err)
	}
	n, _ := r.RowsAffected()
	res.SessionsDeleted = int(n)

	ucut := now.Add(-j.usageAge()).UTC().Format("2006-01-02")
	r, err = j.DB.ExecContext(ctx, `
		DELETE FROM usage WHERE day < $1::date`, ucut)
	if err != nil {
		return res, fmt.Errorf("retention: usage: %w", err)
	}
	n, _ = r.RowsAffected()
	res.UsageDeleted = int(n)

	return res, nil
}

// RebuildQuery es la consulta con la que se responde un cobro
// disputado a 60 dias: el dia pedido por (node_id, started_at) — el
// indice sessions_node_started (node_id, started_at) la sirve en
// index scan bajo 1 s a 100k filas (criterio 3 T-1003).
func RebuildQuery() string {
	return `SELECT session_id, gw_id, bytes_in, bytes_out, closed_reason
    FROM sessions
   WHERE node_id = $1::uuid AND started_at >= $2::timestamptz
     AND started_at < $3::timestamptz
   ORDER BY started_at`
}
