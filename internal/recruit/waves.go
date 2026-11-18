package recruit

import (
	"context"
	"database/sql"
	"errors"
)

// Oleada es una ventana de trabajo del reclutamiento con las tres cifras que
// la puerta exige (promoted>0, verified>=1, closed_at) — T-2101.
type Oleada struct {
	No       int
	State    string
	Promoted int
	Enrolled int
	Verified int
}

// oleadaAbierta devuelve la oleada abierta (0 si no hay ninguna).
func (s *Store) oleadaAbierta(ctx context.Context) (int, error) {
	var no int
	err := s.db.QueryRowContext(ctx, `
		SELECT wave_no FROM recruit_waves WHERE state = 'abierta'
		 ORDER BY wave_no LIMIT 1`).Scan(&no)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return no, err
}

// AbrirOleada devuelve la oleada abierta y, si no hay, abre la siguiente.
// La numeracion no se escribe a mano: sale de max(wave_no)+1.
func (s *Store) AbrirOleada(ctx context.Context) (int, error) {
	no, err := s.oleadaAbierta(ctx)
	if err != nil || no > 0 {
		return no, err
	}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO recruit_waves (wave_no)
		VALUES (COALESCE((SELECT max(wave_no) FROM recruit_waves), 0) + 1)
		RETURNING wave_no`).Scan(&no)
	return no, err
}

// CerrarOleada cierra la oleada abierta con las cifras que la base puede
// probar. Sin oleada abierta no hace nada y no es error: cerrar dos veces la
// misma oleada es idempotente (T-2101 AC1). `cerrada` dice si esta llamada
// cerro algo.
func (s *Store) CerrarOleada(ctx context.Context, nota string) (no int, cerrada bool, err error) {
	// La nota es la identidad de la oleada: cerrar dos veces con la misma
	// nota no crea una segunda fila (T-2101 AC1, idempotencia). Si ya estaba
	// cerrada, se refrescan sus cifras y se dice que no se cerro nada nuevo.
	yaCerrada := false
	if nota != "" {
		var ya int
		err = s.db.QueryRowContext(ctx, `
			SELECT wave_no FROM recruit_waves
			 WHERE note = $1 AND state = 'cerrada' LIMIT 1`, nota).Scan(&ya)
		switch {
		case err == nil:
			no, yaCerrada = ya, true
		case errors.Is(err, sql.ErrNoRows):
			err = nil
		default:
			return 0, false, err
		}
	}
	if !yaCerrada {
		no, err = s.oleadaAbierta(ctx)
		if err == nil && no == 0 {
			no, err = s.AbrirOleada(ctx)
		}
		if err != nil || no == 0 {
			return no, false, err
		}
	}
	var promoted, enrolled, verified int
	// promoted: candidatos que han llegado al final del embudo.
	if err = s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_candidates WHERE state IN ('verified','enrolled')`).
		Scan(&promoted); err != nil {
		return no, false, err
	}
	// enrolled: aparatos dados de alta durante la oleada.
	if err = s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM nodes n
		  JOIN recruit_waves w ON w.wave_no = $1
		 WHERE n.enrolled_at >= w.started_at`, no).Scan(&enrolled); err != nil {
		return no, false, err
	}
	// verified: aparatos distintos con una salida SOCKS5 probada.
	if err = s.db.QueryRowContext(ctx, `
		SELECT count(DISTINCT node_id) FROM recruit_socks5_checks WHERE ok`).
		Scan(&verified); err != nil {
		return no, false, err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE recruit_waves
		   SET state = 'cerrada', closed_at = COALESCE(closed_at, now()),
		       promoted = $2, enrolled = $3, verified = $4, note = $5
		 WHERE wave_no = $1`, no, promoted, enrolled, verified, nota)
	if err != nil {
		return no, false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return no, false, nil
	}
	return no, !yaCerrada, nil
}

// Oleadas devuelve todas las oleadas con sus cifras (para el estado y la
// puerta: lo que se publica se lee de aqui).
func (s *Store) Oleadas(ctx context.Context) ([]Oleada, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT wave_no, state, promoted, enrolled, verified
		  FROM recruit_waves ORDER BY wave_no`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Oleada
	for rows.Next() {
		var o Oleada
		if err := rows.Scan(&o.No, &o.State, &o.Promoted, &o.Enrolled, &o.Verified); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
