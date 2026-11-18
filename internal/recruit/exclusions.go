package recruit

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Exclusión doble (T-1703, A1-12).
//
// La lista se aplica dos veces en el ciclo de vida: al nacer el candidato
// (no se gasta ni un SYN en un banco) y otra vez antes de `assessed` (un
// PTR puede casar despues de saber el modelo). El honeypot no se descarta en
// generico: muere con `honeypot:<patron>` para poder contarlo.

// Exclusion es una fila de la lista.
type Exclusion struct {
	Kind     string
	Pattern  string
	Category string
	Note     string
}

// CargarExclusions lee un CSV (kind,pattern,category,note) y hace upsert.
func (s *Store) CargarExclusions(ctx context.Context, ruta string) (int, error) {
	f, err := os.Open(ruta)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	filas, err := r.ReadAll()
	if err != nil {
		return 0, err
	}
	n := 0
	for i, fila := range filas {
		if len(fila) == 0 || strings.HasPrefix(strings.TrimSpace(fila[0]), "#") {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(fila[0]), "kind") {
			continue // cabecera
		}
		if len(fila) < 3 {
			return n, fmt.Errorf("exclusions: fila %d con menos de 3 columnas", i+1)
		}
		ex := Exclusion{
			Kind:     strings.TrimSpace(fila[0]),
			Pattern:  strings.TrimSpace(fila[1]),
			Category: strings.TrimSpace(fila[2]),
		}
		if len(fila) > 3 {
			ex.Note = strings.TrimSpace(fila[3])
		}
		if err := s.AgregarExclusion(ctx, ex); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// AgregarExclusion hace upsert de una fila.
func (s *Store) AgregarExclusion(ctx context.Context, ex Exclusion) error {
	if ex.Kind == "" || ex.Pattern == "" || ex.Category == "" {
		return fmt.Errorf("exclusions: kind, pattern y category son obligatorios")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recruit_exclusions (kind, pattern, category, note)
		VALUES ($1,$2,$3,NULLIF($4,''))
		ON CONFLICT (kind, pattern) DO UPDATE SET category = EXCLUDED.category, note = EXCLUDED.note`,
		ex.Kind, ex.Pattern, ex.Category, ex.Note)
	return err
}

// Coincide busca si la IP o el PTR estan excluidos. Devuelve el motivo
// escrito (`categoria:patron`) o vacio.
func (s *Store) Coincide(ctx context.Context, ip, ptr string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT kind, pattern, category FROM recruit_exclusions WHERE active`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, pattern, category string
		if err := rows.Scan(&kind, &pattern, &category); err != nil {
			return "", err
		}
		switch kind {
		case "prefix":
			var dentro bool
			if err := s.db.QueryRowContext(ctx, `SELECT $1::inet <<= $2::cidr`, ip, pattern).Scan(&dentro); err != nil {
				continue
			}
			if dentro {
				return category + ":" + pattern, nil
			}
		case "ptr_regex":
			re, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			if ptr != "" && re.MatchString(ptr) {
				return category + ":" + pattern, nil
			}
		case "honeypot":
			if ptr != "" && strings.Contains(strings.ToLower(ptr), strings.ToLower(pattern)) {
				return category + ":" + pattern, nil
			}
		case "country":
			var pais sql.NullString
			if err := s.db.QueryRowContext(ctx, `
				SELECT country FROM recruit_candidates WHERE ip = $1::inet LIMIT 1`, ip).Scan(&pais); err == nil {
				if pais.Valid && strings.EqualFold(pais.String, pattern) {
					return category + ":" + pattern, nil
				}
			}
		}
	}
	return "", rows.Err()
}

// Excluir deja el candidato fuera con motivo escrito y su evento.
func (s *Store) Excluir(ctx context.Context, id int64, motivo, actor string) error {
	var desde string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM recruit_candidates WHERE candidate_id=$1`, id).Scan(&desde); err != nil {
		return err
	}
	if desde == "excluded" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE recruit_candidates
		   SET state='excluded', fail_reason='excluded', lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		 WHERE candidate_id = $1`, id); err != nil {
		return err
	}
	var seq int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(max(seq),0)+1 FROM recruit_candidate_events WHERE candidate_id=$1`, id).Scan(&seq); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recruit_candidate_events (candidate_id, seq, from_state, to_state, reason, actor)
		VALUES ($1,$2,$3,'excluded',$4,$5)`, id, seq, desde, motivo, actor)
	return err
}
