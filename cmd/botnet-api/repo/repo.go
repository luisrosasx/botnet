// Package repo: SQL plano sobre Postgres (BLUEPRINT §9: handler →
// service → repo, sin ORM). Cada metodo es UNA consulta o una transacion
// minima; las queries del contrato frio (apikey, warmup) estan pensadas
// para que el test de T-0804 cuente ida exacta contra db.Counted.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/db"
)

// ErrNotFound: sonda generica para filas ausentes (node, session, key).
var ErrNotFound = errors.New("repo: not found")

// Data agrupa los repos del backend sobre un unico Querier medible.
type Data struct{ Q db.Querier }

func New(q db.Querier) *Data { return &Data{Q: q} }

// --- nodes ---------------------------------------------------------------

// ErrTicketTaken: el sha256 del ticket ya lo tiene un nodo vivo
// (conflicto con nodes_ticket_hash_live_uniq, T-0305 AC2/AC3). El alta
// no se rehace: es 409 con el node_id del dueno del ticket.
var ErrTicketTaken = errors.New("repo: ticket de intrusion ya tomado")

// InsertNode refleja el alta: node_id lo genera el backend (uuid v4).
// device_class default 'C' (la mas conservadora); operations la corrige
// cuando el inventario de EP-01 la clasifica por banner. El ticket
// consumido viaja en la MISMA fila (ticket_hash): el ON CONFLICT contra
// el indice unico parcial es la ganadora unica del contrato — cero
// filas afectadas = ErrTicketTaken.
func (d *Data) InsertNode(ctx context.Context, nodeID, slug, tokenHash, ticketHash, arch, model, fw string, macHash []byte, consentAt time.Time) error {
	var inserted string
	err := d.Q.QueryRowContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, ticket_hash, arch, device_class, model, firmware, mac_hash, consent_at, status)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, 'C', $6, $7, $8, $9, 'enrolled')
		ON CONFLICT (ticket_hash) WHERE deleted_at IS NULL AND ticket_hash IS NOT NULL
		DO NOTHING
		RETURNING node_id::text`,
		nodeID, slug, tokenHash, ticketHash, arch, model, fw, macHash, consentAt).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTicketTaken
	}
	if err != nil {
		return fmt.Errorf("repo: insert node: %w", err)
	}
	return nil
}

// NodeByTicketHash devuelve el dueno vivo del ticket ("" si ninguno).
// Con nodes_ticket_hash_live_uniq es a lo sumo una fila; el vacio tras
// el conflicto de indice es transitorio (la ganadora aun no commitea):
// el service reintenta acotado.
func (d *Data) NodeByTicketHash(ctx context.Context, hash string) (string, error) {
	var id string
	err := d.Q.QueryRowContext(ctx, `
		SELECT node_id::text FROM nodes
		 WHERE ticket_hash = $1 AND deleted_at IS NULL`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: node by ticket: %w", err)
	}
	return id, nil
}

// NodeByTokenHash es el gate A3-01 del enroll: token conocido = nodo vivo
// en la flota. Devuelve ("", nil) si no existe.
func (d *Data) NodeByTokenHash(ctx context.Context, hash string) (string, error) {
	var id string
	err := d.Q.QueryRowContext(ctx, `SELECT node_id::text FROM nodes WHERE device_token_hash = $1`, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: node by token: %w", err)
	}
	return id, nil
}

// NodePublic es la pagina de transparencia del dueño (GET /t/{slug}):
// solo lo publicable; el device_token jamas asoma aqui (A3-01).
func (d *Data) NodePublic(ctx context.Context, slug string) (map[string]any, error) {
	row := d.Q.QueryRowContext(ctx, `
		SELECT slug, status, COALESCE(country, ''), COALESCE(device_class, ''), COALESCE(model, ''),
		       COALESCE(monthly_gb_cap, 150),
		       COALESCE(bandwidth_cap_kbps, 2048),
		       COALESCE(RTRIM(leave_code::text), ''),
		       to_char(consent_at, 'YYYY-MM-DD'),
		       node_id::text
		  FROM nodes WHERE slug = $1 AND deleted_at IS NULL`, slug)
	var out = map[string]any{}
	var s, st, co, dc, mo, consent, code, nodeID string
	var gbCap, bwKbps int
	if err := row.Scan(&s, &st, &co, &dc, &mo, &gbCap, &bwKbps, &code, &consent, &nodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("repo: node public: %w", err)
	}
	// gb_month: GB del mes en curso servidos por el nodo (tabla
	// contable `usage`; la pagina del dueño muestra lo consumido,
	// T-1101). Cero si el nodo aun no tiene filas del mes.
	var gbMonth float64
	if err := d.Q.QueryRowContext(ctx, `
		SELECT COALESCE(sum(bytes) / 1000000000.0, 0)
		  FROM usage
		 WHERE node_id = $1::uuid
		   AND date_trunc('month', day) = date_trunc('month', now())`, nodeID).
		Scan(&gbMonth); err != nil {
		return nil, fmt.Errorf("repo: node public gb mes: %w", err)
	}
	// monthly_gb_cap: la cuota del dueno es de VOLUMEN y va publicada
	// en la pagina (A2-20) — el dueno ve el tope, no solo la velocidad.
	out["slug"], out["status"], out["country"], out["device_class"] = s, st, co, dc
	out["model"], out["monthly_gb_cap"], out["consent_date"] = mo, gbCap, consent
	out["bandwidth_cap_kbps"], out["gb_month"], out["leave_code"] = bwKbps, gbMonth, code
	out["node_id"] = nodeID
	return out, nil
}

// NodesDirectory es el inventario publicable para GET /v1/nodes
// (EP-11 T-1107): el directorio /t enlaza slugs sin exponer nunca el
// device_token. status vacio = flota viva (live/enrolled/down).
func (d *Data) NodesDirectory(ctx context.Context, status string) ([]map[string]any, error) {
	rows, err := d.Q.QueryContext(ctx, `
		SELECT slug, status, COALESCE(country, ''), COALESCE(device_class, ''), COALESCE(model, ''),
		       COALESCE(monthly_gb_cap, 150)
		  FROM nodes
		 WHERE deleted_at IS NULL
		   AND ($1 <> '' AND status = $1 OR status IN ('live','enrolled','down','retiring'))
		 ORDER BY slug`, status)
	if err != nil {
		return nil, fmt.Errorf("repo: nodes directory: %w", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var s, st, co, dc, mo string
		var gbCap int
		if err := rows.Scan(&s, &st, &co, &dc, &mo, &gbCap); err != nil {
			return nil, fmt.Errorf("repo: nodes directory scan: %w", err)
		}
		out = append(out, map[string]any{
			"slug": s, "status": st, "country": co,
			"device_class": dc, "model": mo, "monthly_gb_cap": gbCap,
		})
	}
	return out, rows.Err()
}

// NodeStatus para el 410 del heartbeat y el estado del 200 de revoke.
func (d *Data) NodeStatus(ctx context.Context, nodeID string) (string, error) {
	var s string
	err := d.Q.QueryRowContext(ctx, `SELECT status FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return s, err
}

// MarkLive: el heartbeat convierte enrolled/down en live y estampa
// last_seen_at (la presencia caliente ademas deja node:{id}:live en Redis).
func (d *Data) MarkLive(ctx context.Context, nodeID string) error {
	_, err := d.Q.ExecContext(ctx, `
		UPDATE nodes
		   SET last_seen_at = now(),
		       status = CASE WHEN status IN ('enrolled','down') THEN 'live' ELSE status END
		 WHERE node_id = $1::uuid`, nodeID)
	if err != nil {
		return fmt.Errorf("repo: mark live: %w", err)
	}
	return nil
}

// IngestHeartbeat (EP-12 T-1202): vierte el latido decodificado en el
// historico node_heartbeats y pone al dia nodes.agent_version con la ver
// del MISMO frame — el inventario del panel sale de la fila real, nunca
// de una identidad prestada. agentVer vacio (latido sin telemetria) no
// borra la version conocida; rss_kib y streams quedan igualmente en la
// fila historica.
func (d *Data) IngestHeartbeat(ctx context.Context, nodeID string, seq uint32, rssKiB uint32, streams uint16, lossEstimate uint16, agentVer string) error {
	if _, err := d.Q.ExecContext(ctx, `
		INSERT INTO node_heartbeats (node_id, seq, rss_kib, streams, loss_estimate, agent_version)
		VALUES ($1::uuid, $2, $3, $4, $5, NULLIF($6, ''))`,
		nodeID, int64(seq), int64(rssKiB), int(streams), int(lossEstimate), agentVer); err != nil {
		return fmt.Errorf("repo: ingest heartbeat: %w", err)
	}
	if agentVer == "" {
		return nil
	}
	if _, err := d.Q.ExecContext(ctx, `
		UPDATE nodes SET agent_version = $2 WHERE node_id = $1::uuid`,
		nodeID, agentVer); err != nil {
		return fmt.Errorf("repo: ingest heartbeat ver: %w", err)
	}
	return nil
}

// SetNodeStatus (revoke -> burned, baja -> retired).
func (d *Data) SetNodeStatus(ctx context.Context, nodeID, status string) error {
	res, err := d.Q.ExecContext(ctx, `UPDATE nodes SET status = $2 WHERE node_id = $1::uuid`, nodeID, status)
	if err != nil {
		return fmt.Errorf("repo: set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PickNode: seleccion de exit del cliente (§10): pais exacto, ciudad/ASN
// si las pidio, solo nodos live con <8 tenants activos (densidad A1-06).
// Devuelve ("", nil) si el pool no tiene donde casar la sesion.
func (d *Data) PickNode(ctx context.Context, country, city string, asn int) (string, error) {
	var id string
	err := d.Q.QueryRowContext(ctx, `
		SELECT n.node_id::text
		  FROM nodes n
		 WHERE n.status = 'live'
		   AND n.country = $1
		   AND ($2 = '' OR n.city = $2)
		   AND ($3 = 0 OR n.asn = $3)
		   AND (SELECT count(DISTINCT s.tenant_id) FROM sessions s
		         WHERE s.node_id = n.node_id AND s.ended_at IS NULL) < 8
		 ORDER BY n.last_seen_at DESC NULLS LAST
		 LIMIT 1`, country, city, asn).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: pick node: %w", err)
	}
	return id, nil
}

// --- node_secrets ---------------------------------------------------------

// InsertSecret: el node_secret (32 B) nace aqui y rota con el
// certificado (expires_at = +90 d). El UNIQUE parcial del esquema
// garantiza el secreto vivo unico (T-0801 AC2).
func (d *Data) InsertSecret(ctx context.Context, nodeID string, secret []byte, expiresAt time.Time) error {
	_, err := d.Q.ExecContext(ctx, `
		INSERT INTO node_secrets (node_id, secret, issued_at, expires_at)
		VALUES ($1::uuid, $2, now(), $3)`, nodeID, secret, expiresAt.UTC())
	if err != nil {
		return fmt.Errorf("repo: insert secret: %w", err)
	}
	return nil
}

// RevokeSecrets: la baja revoca cert y node_secret a la vez (T-0803).
// Devuelve cuantos secretos vivos quedron revocados (0 o 1 por invariante).
func (d *Data) RevokeSecrets(ctx context.Context, nodeID string) (int, error) {
	res, err := d.Q.ExecContext(ctx, `
		UPDATE node_secrets SET revoked_at = now()
		 WHERE node_id = $1::uuid AND revoked_at IS NULL`, nodeID)
	if err != nil {
		return 0, fmt.Errorf("repo: revoke secrets: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SecretExpiresAt: assert de T-0802 (expires_at = +90 d en el alta).
func (d *Data) SecretExpiresAt(ctx context.Context, nodeID string) (time.Time, error) {
	var t time.Time
	err := d.Q.QueryRowContext(ctx, `
		SELECT expires_at FROM node_secrets
		 WHERE node_id = $1::uuid AND revoked_at IS NULL`, nodeID).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// --- api_keys (backend.KeyStore) ------------------------------------------

// LookupKey: exactamente una ida por validacion fria (T-0804 AC1).
func (d *Data) LookupKey(ctx context.Context, hash string) (backend.KeyInfo, error) {
	var k backend.KeyInfo
	err := d.Q.QueryRowContext(ctx, `
		SELECT api_key_id, key_hash, tenant_id::text, mode, sticky_ttl_s, status
		  FROM api_keys WHERE key_hash = $1`, hash).
		Scan(&k.KeyID, &k.KeyHash, &k.TenantID, &k.Mode, &k.StickyTTL, &k.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return k, backend.ErrKeyUnknown
	}
	if err != nil {
		return k, fmt.Errorf("repo: lookup key: %w", err)
	}
	return k, nil
}

// ListActiveKeys alimenta el warmup del gateway (T-0804 AC2).
func (d *Data) ListActiveKeys(ctx context.Context) ([]backend.KeyInfo, error) {
	rows, err := d.Q.QueryContext(ctx, `
		SELECT api_key_id, key_hash, tenant_id::text, mode, sticky_ttl_s, status
		  FROM api_keys WHERE status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("repo: list keys: %w", err)
	}
	defer rows.Close()
	var out []backend.KeyInfo
	for rows.Next() {
		var k backend.KeyInfo
		if err := rows.Scan(&k.KeyID, &k.KeyHash, &k.TenantID, &k.Mode, &k.StickyTTL, &k.Status); err != nil {
			return nil, fmt.Errorf("repo: scan key: %w", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// TouchKeyLastUsed: el heartbeat del gateway marca la key usada.
func (d *Data) TouchKeyLastUsed(ctx context.Context, keyID int64) error {
	_, err := d.Q.ExecContext(ctx, `UPDATE api_keys SET last_used_at = now() WHERE api_key_id = $1`, keyID)
	return err
}

// --- tenants / quotas ------------------------------------------------------

func (d *Data) TenantStatus(ctx context.Context, tenantID string) (string, error) {
	var s string
	err := d.Q.QueryRowContext(ctx, `SELECT status FROM tenants WHERE tenant_id = $1::uuid`, tenantID).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return s, err
}

// ActiveTenants para la reconstruccion de contadores post-chaos (T-0806).
func (d *Data) ActiveTenants(ctx context.Context) ([]string, error) {
	rows, err := d.Q.QueryContext(ctx, `SELECT tenant_id::text FROM tenants WHERE status = 'active'`)
	if err != nil {
		return nil, fmt.Errorf("repo: active tenants: %w", err)
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

// QuotaFor: la cuota vigente del tenant (periodo mas reciente).
type Quota struct {
	GBAllowance     float64
	ConcurrentLimit int
	PeriodStart     string // YYYY-MM-DD
}

func (d *Data) QuotaFor(ctx context.Context, tenantID string) (Quota, error) {
	var q Quota
	err := d.Q.QueryRowContext(ctx, `
		SELECT gb_allowance, concurrent_limit, to_char(period_start, 'YYYY-MM-DD')
		  FROM quotas WHERE tenant_id = $1::uuid
		 ORDER BY period_start DESC LIMIT 1`, tenantID).
		Scan(&q.GBAllowance, &q.ConcurrentLimit, &q.PeriodStart)
	if errors.Is(err, sql.ErrNoRows) {
		return q, ErrNotFound
	}
	return q, err
}

// --- sessions --------------------------------------------------------------

func (d *Data) InsertSession(ctx context.Context, nodeID, tenantID string, apiKeyID int64, clientRef, gwID string) (int64, error) {
	var id int64
	err := d.Q.QueryRowContext(ctx, `
		INSERT INTO sessions (node_id, tenant_id, api_key_id, client_ref, gw_id)
		VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, ''), $5)
		RETURNING session_id`, nodeID, tenantID, apiKeyID, clientRef, gwID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("repo: insert session: %w", err)
	}
	return id, nil
}

// CloseSession cierra con closed_reason tipada (§9). ErrNotFound si ya
// estaba cerrada o no existe.
func (d *Data) CloseSession(ctx context.Context, sessionID int64, reason string, tenantID string) error {
	res, err := d.Q.ExecContext(ctx, `
		UPDATE sessions SET ended_at = now(), closed_reason = $3
		 WHERE session_id = $1 AND ended_at IS NULL
		   AND ($2 = '' OR tenant_id::text = $2)`,
		sessionID, tenantID, reason)
	if err != nil {
		return fmt.Errorf("repo: close session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CloseOpenForNode: el revoke drena las sesiones vivas del nodo quemado
// con closed_reason tipada (T-0803 AC2).
func (d *Data) CloseOpenForNode(ctx context.Context, nodeID, reason string) (int, error) {
	res, err := d.Q.ExecContext(ctx, `
		UPDATE sessions SET ended_at = now(), closed_reason = $2
		 WHERE node_id = $1::uuid AND ended_at IS NULL`, nodeID, reason)
	if err != nil {
		return 0, fmt.Errorf("repo: close node sessions: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CountOpenForTenant: concurrencia viva del tenant (cuota concurrente).
func (d *Data) CountOpenForTenant(ctx context.Context, tenantID string) (int, error) {
	var n int
	err := d.Q.QueryRowContext(ctx, `
		SELECT count(*) FROM sessions WHERE tenant_id = $1::uuid AND ended_at IS NULL`, tenantID).Scan(&n)
	return n, err
}

// SumTenantBytes: fuente fria autoritativa para reconstruir
// tenant:{t}:gb_used tras caos Redis (T-0806 AC2).
func (d *Data) SumTenantBytes(ctx context.Context, tenantID string, periodStart string) (int64, error) {
	var sum sql.NullInt64
	err := d.Q.QueryRowContext(ctx, `
		SELECT SUM(bytes_in + bytes_out) FROM sessions
		 WHERE tenant_id = $1::uuid AND started_at >= $2::date`, tenantID, periodStart).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("repo: sum tenant bytes: %w", err)
	}
	return sum.Int64, nil
}

// AddSessionBytes: el gateway flusha contadores del stream (la factura
// fina vive aqui; usage sigue siendo el medidor diario).
func (d *Data) AddSessionBytes(ctx context.Context, sessionID int64, bytesIn, bytesOut int64) error {
	_, err := d.Q.ExecContext(ctx, `
		UPDATE sessions SET bytes_in = bytes_in + $2, bytes_out = bytes_out + $3
		 WHERE session_id = $1`, sessionID, bytesIn, bytesOut)
	return err
}

// UpsertUsage acumula el total de un flush del nodo en la fila diaria
// usage(day, node_id) — el rollup diario del PK (day, node_id), nunca
// por minuto (leccion A2-07, T-1003).
func (d *Data) UpsertUsage(ctx context.Context, day, nodeID string, addBytes int64) error {
	_, err := d.Q.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes) VALUES ($1::date, $2::uuid, $3)
		ON CONFLICT (day, node_id) DO UPDATE SET bytes = usage.bytes + EXCLUDED.bytes`,
		day, nodeID, addBytes)
	if err != nil {
		return fmt.Errorf("repo: upsert usage: %w", err)
	}
	return nil
}

// MarkUsageReconciled marca reconciliadas las filas del dia para los
// nodos dados (webhook invoice.paid tras la firma valida, T-1004).
// Devuelve el numero de filas marcadas.
func (d *Data) MarkUsageReconciled(ctx context.Context, day string, nodeIDs []string) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	res, err := d.Q.ExecContext(ctx, `
		UPDATE usage SET reconciled = true
		 WHERE day = $1::date AND node_id = ANY($2::uuid[])`, day, nodeIDs)
	if err != nil {
		return 0, fmt.Errorf("repo: mark usage reconciled: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SessionTenant para autorizar el DELETE /api/v1/sessions/{id}.
func (d *Data) SessionTenant(ctx context.Context, sessionID int64) (string, error) {
	var t string
	err := d.Q.QueryRowContext(ctx, `
		SELECT tenant_id::text FROM sessions WHERE session_id = $1`, sessionID).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return t, err
}
