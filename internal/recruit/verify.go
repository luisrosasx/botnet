package recruit

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Verificacion SOCKS5 por nodo (T-1804). Una fila por intento y `ok` solo
// con evidencia dura: 200, bytes de verdad, latencia plausible e IP de
// salida coincidente. Fallar no mata al candidato: acumula intentos y a
// partir del tercero queda `deferred` con `socks_fail`.

// Evidencia es el resultado de un intento.
type Evidencia struct {
	IP         string `json:"ip"`
	Port       int    `json:"port"`
	NodeID     string `json:"node_id"`
	Target     string `json:"target"`
	HTTPStatus int    `json:"http_status"`
	BytesIn    int    `json:"bytes_in"`
	LatencyMS  int    `json:"latency_ms"`
	EgressIP   string `json:"egress_ip"`
	IPMatch    bool   `json:"ip_match"`
}

// EsEvidenciaDura decide el `ok` sin confiar en el llamante.
func EsEvidenciaDura(e Evidencia) bool {
	return e.HTTPStatus == 200 && e.BytesIn > 0 && e.LatencyMS >= 0 &&
		e.LatencyMS <= 1500 && e.IPMatch
}

// Verificar guarda el intento y mueve al candidato. Devuelve la fila escrita.
func (s *Store) Verificar(ctx context.Context, e Evidencia) (int, bool, error) {
	id, err := s.CandidatoPorIP(ctx, e.IP, e.Port)
	if err != nil {
		return 0, false, fmt.Errorf("verify: candidato %s:%d: %w", e.IP, e.Port, err)
	}
	var attempt int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(max(attempt),0)+1 FROM recruit_socks5_checks WHERE candidate_id=$1`, id).Scan(&attempt); err != nil {
		return 0, false, err
	}
	ok := EsEvidenciaDura(e)
	// Un solo ganador por candidato (indice parcial): si ya gano antes, el
	// intento se guarda igual como evidencia pero no reclama el indice.
	var yaGano bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM recruit_socks5_checks WHERE candidate_id=$1 AND ok)`, id).Scan(&yaGano); err != nil {
		return 0, false, err
	}
	okFila := ok && !yaGano
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO recruit_socks5_checks
		  (candidate_id, node_id, target, http_status, bytes_in, latency_ms, egress_ip, ip_match, ok, attempt)
		VALUES ($1,$2::uuid,$3,$4,$5,$6,NULLIF($7,'')::inet,$8,$9,$10)`,
		id, e.NodeID, e.Target, e.HTTPStatus, e.BytesIn, e.LatencyMS, e.EgressIP, e.IPMatch, okFila, attempt); err != nil {
		return attempt, ok, err
	}
	if ok {
		_, err := s.db.ExecContext(ctx, `
			UPDATE recruit_candidates
			   SET state='verified', node_id=$2::uuid, verified_at=now(), updated_at=now()
			 WHERE candidate_id=$1`, id, e.NodeID)
		return attempt, true, err
	}
	// Fallo: no se descarta en la primera oleada, solo acumula.
	if _, err := s.db.ExecContext(ctx, `
		UPDATE recruit_candidates
		   SET stage_attempts = LEAST(stage_attempts+1, 8),
		       state = CASE WHEN state='verified' THEN state
		                    WHEN stage_attempts+1 >= 3 THEN 'deferred' ELSE state END,
		       fail_reason = 'socks_fail', updated_at=now()
		 WHERE candidate_id=$1`, id); err != nil {
		return attempt, false, err
	}
	return attempt, false, nil
}

// UltimoIntento lee la ultima fila de un candidato (para el JSON de salida).
func (s *Store) UltimoIntento(ctx context.Context, ip string, port int) (Evidencia, bool, error) {
	var e Evidencia
	var ok bool
	err := s.db.QueryRowContext(ctx, `
		SELECT c.ip::text, c.port, s.node_id::text, s.target, COALESCE(s.http_status,0),
		       s.bytes_in, COALESCE(s.latency_ms,0), COALESCE(s.egress_ip::text,''), s.ip_match, s.ok
		  FROM recruit_socks5_checks s JOIN recruit_candidates c USING (candidate_id)
		 WHERE c.ip=$1::inet AND c.port=$2
		 ORDER BY s.check_id DESC LIMIT 1`, ip, port).
		Scan(&e.IP, &e.Port, &e.NodeID, &e.Target, &e.HTTPStatus, &e.BytesIn, &e.LatencyMS, &e.EgressIP, &e.IPMatch, &ok)
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	return e, ok, err
}

var _ = time.Now

// NodosEnrolados lista hasta n nodos enrolados (modo --live).
func (s *Store) NodosEnrolados(ctx context.Context, n int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id::text FROM nodes WHERE status='enrolled' AND deleted_at IS NULL LIMIT $1`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
