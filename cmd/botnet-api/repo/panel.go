// panel.go — consultas del panel del tenant (EP-11 T-1105): api keys,
// cuotas editables, uso historico e facturas del periodo. La lectura
// en vivo no pasa por aqui: es el contador Redis del periodo
// (services.GBUsedKey), y el panel lo pide en < 2 s.
package repo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// CreateAPIKey emite una api key nueva: la en claro (base64 de 32 B)
// solo viaja en esta respuesta — en Postgres vive el hash sha256
// (contrato apikey:{sha256} del gateway). Devuelve id y en claro.
func (d *Data) CreateAPIKey(ctx context.Context, tenantID, label, mode string, stickyTTL int) (int64, string, error) {
	if mode == "" {
		mode = "rotating"
	}
	if stickyTTL <= 0 {
		stickyTTL = 300
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return 0, "", fmt.Errorf("repo: api key rand: %w", err)
	}
	plain := base64.RawStdEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plain))
	hash := hex.EncodeToString(sum[:])
	var id int64
	err := d.Q.QueryRowContext(ctx, `
		INSERT INTO api_keys (tenant_id, key_hash, label, mode, sticky_ttl_s)
		VALUES ($1::uuid, $2, $3, $4, $5) RETURNING api_key_id`,
		tenantID, hash, label, mode, stickyTTL).Scan(&id)
	if err != nil {
		return 0, "", fmt.Errorf("repo: create api key: %w", err)
	}
	return id, plain, nil
}

// ListTenantKeys: inventario sin secretos — el hash jamas asoma.
func (d *Data) ListTenantKeys(ctx context.Context, tenantID string) ([]map[string]any, error) {
	rows, err := d.Q.QueryContext(ctx, `
		SELECT api_key_id, COALESCE(label, ''), mode, sticky_ttl_s, status,
		       to_char(created_at, 'YYYY-MM-DD'),
		       COALESCE(to_char(last_used_at, 'YYYY-MM-DD'), '')
		  FROM api_keys
		 WHERE tenant_id = $1::uuid
		 ORDER BY api_key_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("repo: list tenant keys: %w", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var label, mode, created, lastUsed, status string
		var sticky int
		if err := rows.Scan(&id, &label, &mode, &sticky, &status, &created, &lastUsed); err != nil {
			return nil, fmt.Errorf("repo: list tenant keys scan: %w", err)
		}
		out = append(out, map[string]any{
			"api_key_id": id, "label": label, "mode": mode, "sticky_ttl_s": sticky,
			"status": status, "created_at": created, "last_used_at": lastUsed,
		})
	}
	return out, rows.Err()
}

// UpdateQuota toca SOLO los campos dados (los tres POST-ables del
// panel: gb_allowance, concurrent_limit, mode) sobre la fila del
// periodo dado. Devuelve la cuota resultante.
func (d *Data) UpdateQuota(ctx context.Context, tenantID, periodStart string, gbAllowance *float64, concurrentLimit *int, mode *string) (Quota, error) {
	var q Quota
	if mode != nil && *mode != "sticky" && *mode != "rotating" {
		return q, fmt.Errorf("repo: mode desconocido: %q", *mode)
	}
	sets := []string{}
	args := []any{tenantID, periodStart}
	if gbAllowance != nil {
		sets = append(sets, fmt.Sprintf("gb_allowance = $%d", len(args)+1))
		args = append(args, *gbAllowance)
	}
	if concurrentLimit != nil {
		sets = append(sets, fmt.Sprintf("concurrent_limit = $%d", len(args)+1))
		args = append(args, *concurrentLimit)
	}
	if mode != nil {
		sets = append(sets, fmt.Sprintf("mode = $%d", len(args)+1))
		args = append(args, *mode)
	}
	if len(sets) > 0 {
		sqlText := "UPDATE quotas SET "
		for i, s := range sets {
			if i > 0 {
				sqlText += ", "
			}
			sqlText += s
		}
		sqlText += " WHERE tenant_id = $1::uuid AND period_start = $2::date"
		res, err := d.Q.ExecContext(ctx, sqlText, args...)
		if err != nil {
			return q, fmt.Errorf("repo: update quota: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return q, ErrNotFound
		}
	}
	return d.QuotaFor(ctx, tenantID)
}

// usageHistorySQL: facturas/uso por dia del tenant = filas `usage`
// de los nodos que sirvieron al tenant ese mismo dia (join con
// sessions; el medidor autoritativo no se re-inventa por tenant).
const usageHistorySQL = `
	SELECT u.day::date,
	       COALESCE(SUM(u.bytes) / 1000000000.0, 0)                       AS gb,
	       COALESCE(SUM(u.cost_minor), 0)                                 AS cost_minor,
	       MAX(u.currency)                                                AS currency,
	       bool_and(u.reconciled)                                         AS reconciled
	  FROM usage u
	 WHERE u.node_id IN (SELECT DISTINCT s.node_id FROM sessions s
		                  WHERE s.tenant_id = $1::uuid
		                    AND s.started_at >= now() - ($2 || ' days')::interval)
	   AND u.day >= CURRENT_DATE - ($2 || ' days')::interval
	 GROUP BY u.day::date
	 ORDER BY u.day::date`

// TenantUsageHistory: usage diario (GB) de los 30 dias del panel.
func (d *Data) TenantUsageHistory(ctx context.Context, tenantID string, days int) ([]map[string]any, error) {
	return d.queryUsageDays(ctx, tenantID, days)
}

// TenantInvoices: "factura" del periodo por dia con lo cobrado
// (cost_minor/currency) y el sello reconciled que deja el webhook
// firmado. El stripe_customer_id viaja aparte, del tenant.
func (d *Data) TenantInvoices(ctx context.Context, tenantID string, days int) (string, []map[string]any, error) {
	var customer string
	err := d.Q.QueryRowContext(ctx, `
		SELECT COALESCE(stripe_customer_id, '') FROM tenants WHERE tenant_id = $1::uuid`, tenantID).Scan(&customer)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("repo: tenant customer: %w", err)
	}
	rows, err := d.queryUsageDays(ctx, tenantID, days)
	return customer, rows, err
}

func (d *Data) queryUsageDays(ctx context.Context, tenantID string, days int) ([]map[string]any, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := d.Q.QueryContext(ctx, usageHistorySQL, tenantID, fmt.Sprintf("%d", days))
	if err != nil {
		return nil, fmt.Errorf("repo: tenant usage days: %w", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var day time.Time
		var gb float64
		var costMinor int
		var currency string
		var reconciled bool
		if err := rows.Scan(&day, &gb, &costMinor, &currency, &reconciled); err != nil {
			return nil, fmt.Errorf("repo: tenant usage days scan: %w", err)
		}
		out = append(out, map[string]any{
			"day": day.Format("2006-01-02"), "gb": gb, "cost_minor": costMinor,
			"currency": currency, "reconciled": reconciled,
		})
	}
	return out, rows.Err()
}
