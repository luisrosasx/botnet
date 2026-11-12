package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// OptoutWindowSQL es la memoria del opt-out (A3-14, BLUEPRINT §13):
// el dueno que se bajo no vuelve a ver legba por su panel en un ano.
// Se deja como texto SQL para que la cuenta la lleve Postgres (calendario
// real, no 12x30 dias).
const OptoutWindowSQL = "interval '12 months'"

// OptoutMemoryJob rellena la memoria fechada del opt-out:
//  1. nodos con optout_at y sin leave → fila leaves con
//     reprobe_blocked_until = optout_at + 12 meses (baja por la
//     pagina: method 'owner_url');
//  2. leaves con bloqueo mas corto de 12 meses → se estira a
//     left_at + 12 meses.
//
// Asi el barrido de descubrimiento (T-0106) no re-cluta sin
// consentimiento y `make reprobe-guard` puede auditar con 0 filas.
type OptoutMemoryJob struct {
	DB  *sql.DB
	Now func() time.Time
}

// OptoutMemoryResult: leaves insertadas y estiradas.
type OptoutMemoryResult struct {
	Inserted int
	Extended int
}

// Run aplica las dos patas. Idempotente: insert solo para nodos sin
// leave previo; update solo para bloqueos cortos.
func (j *OptoutMemoryJob) Run(ctx context.Context) (OptoutMemoryResult, error) {
	var res OptoutMemoryResult

	r, err := j.DB.ExecContext(ctx, `
		INSERT INTO leaves (node_id, method, left_at, reprobe_blocked_until)
		SELECT n.node_id, 'owner_url', n.optout_at,
		       n.optout_at + `+OptoutWindowSQL+`
		  FROM nodes n
		 WHERE n.optout_at IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM leaves l WHERE l.node_id = n.node_id)`)
	if err != nil {
		return res, fmt.Errorf("leaves: backfill: %w", err)
	}
	n, _ := r.RowsAffected()
	res.Inserted = int(n)

	r, err = j.DB.ExecContext(ctx, `
		UPDATE leaves
		   SET reprobe_blocked_until = left_at + `+OptoutWindowSQL+`
		 WHERE reprobe_blocked_until < left_at + `+OptoutWindowSQL)
	if err != nil {
		return res, fmt.Errorf("leaves: extender: %w", err)
	}
	n, _ = r.RowsAffected()
	res.Extended = int(n)

	return res, nil
}

// ReprobeGuardCount es la pregunta de CI (criterio 3 T-1007): 0 filas
// de nodes con optout_at no nulo y enrolled_at > optout_at dentro de
// los 12 meses posteriores — nadie re-cluto sin consentimiento.
const ReprobeGuardCount = `
	SELECT count(*) FROM nodes n
	 WHERE n.optout_at IS NOT NULL
	   AND n.enrolled_at > n.optout_at
	   AND n.enrolled_at < n.optout_at + interval '12 months'`
