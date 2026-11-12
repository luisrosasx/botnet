// panel.go — superficie del panel del tenant (EP-11 T-1105): api
// keys, cuotas editables, uso en vivo (Redis) e historico (Postgres)
// y facturas del periodo. Todo via Bearer api key: el tenant opera
// solo sin abrir una consola.
package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
	"github.com/botnet/botnet/internal/backend"
)

// requireAPIKey: misma credencial que POST /api/v1/sessions — el
// panel NO tiene autoprocesos: fetch con `api key`.
func requireAPIKey(d *Deps, c *fiber.Ctx) (backend.KeyInfo, error) {
	bearer := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
	if bearer == "" || strings.HasPrefix(c.Get("Authorization"), "Basic") {
		return backend.KeyInfo{}, errors.New("api key requerida")
	}
	if d.Keys == nil {
		return backend.KeyInfo{}, backend.ErrRedisDown
	}
	key, err := d.Keys.Validate(c.Context(), bearer)
	if err != nil {
		return backend.KeyInfo{}, err
	}
	if st, serr := d.Repo.TenantStatus(c.Context(), key.TenantID); serr != nil || st != "active" {
		return backend.KeyInfo{}, errors.New("tenant no activo")
	}
	return key, nil
}

// panelAuth: envoltorio 403/503 comun de las rutas /v1/panel/*.
func panelAuth(d *Deps, h func(c *fiber.Ctx, key backend.KeyInfo) error) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key, err := requireAPIKey(d, c)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrKeyUnknown), errors.Is(err, backend.ErrKeyRevoked),
				errors.Is(err, backend.ErrRedisDown), err.Error() == "api key requerida", err.Error() == "tenant no activo":
				return jsonError(c, http.StatusForbidden, err.Error())
			default:
				return jsonError(c, http.StatusServiceUnavailable, err.Error())
			}
		}
		return h(c, key)
	}
}

// panelUsage — GET /v1/panel/usage?days=30: gb_used vivo del periodo
// (Redis, < 2 s) + historico diario desde Postgres.
func panelUsage(d *Deps) fiber.Handler {
	return panelAuth(d, func(c *fiber.Ctx, key backend.KeyInfo) error {
		quota, err := d.Repo.QuotaFor(c.Context(), key.TenantID)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		live := 0.0
		if d.RDB != nil {
			if v, rerr := d.RDB.Get(c.Context(), services.GBUsedKey(key.TenantID, quota.PeriodStart)).Float64(); rerr == nil {
				live = v
			}
		}
		days, _ := strconv.Atoi(c.Query("days", "30"))
		history, err := d.Repo.TenantUsageHistory(c.Context(), key.TenantID, days)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"tenant_id": key.TenantID, "period": quota.PeriodStart,
			"gb_used_live": live, "gb_allowance": quota.GBAllowance,
			"history_days": days, "history": history,
		})
	})
}

// panelQuotas — GET /v1/panel/quotas y PATCH (gb_allowance,
// concurrent_limit y mode son los 3 campos POST-ables del criterio).
func panelQuotas(d *Deps) fiber.Handler {
	return panelAuth(d, func(c *fiber.Ctx, key backend.KeyInfo) error {
		quota, err := d.Repo.QuotaFor(c.Context(), key.TenantID)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "sin cuota de periodo")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		var cur struct {
			Mode string `json:"mode"`
		}
		if rows, kerr := d.Repo.ListTenantKeys(c.Context(), key.TenantID); kerr == nil && len(rows) > 0 {
			cur.Mode, _ = rows[0]["mode"].(string)
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"period_start": quota.PeriodStart, "gb_allowance": quota.GBAllowance,
			"concurrent_limit": quota.ConcurrentLimit, "mode": cur.Mode,
		})
	})
}

func panelQuotaPatch(d *Deps) fiber.Handler {
	return panelAuth(d, func(c *fiber.Ctx, key backend.KeyInfo) error {
		var body struct {
			GBAllowance     *float64 `json:"gb_allowance"`
			ConcurrentLimit *int     `json:"concurrent_limit"`
			Mode            *string  `json:"mode"`
		}
		if err := c.BodyParser(&body); err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		quota, err := d.Repo.QuotaFor(c.Context(), key.TenantID)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "sin cuota de periodo")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		updated, uerr := d.Repo.UpdateQuota(c.Context(), key.TenantID, quota.PeriodStart, body.GBAllowance, body.ConcurrentLimit, body.Mode)
		if errors.Is(uerr, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "cuota no encontrada")
		}
		if uerr != nil {
			if strings.Contains(uerr.Error(), "mode desconocido") {
				return jsonError(c, http.StatusBadRequest, uerr.Error())
			}
			if strings.Contains(uerr.Error(), "check") || strings.Contains(uerr.Error(), "violates") {
				return jsonError(c, http.StatusBadRequest, uerr.Error())
			}
			return jsonError(c, http.StatusInternalServerError, uerr.Error())
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"period_start": updated.PeriodStart, "gb_allowance": updated.GBAllowance,
			"concurrent_limit": updated.ConcurrentLimit,
		})
	})
}

// panelKeysList / panelKeyCreate — GET y POST /v1/panel/api-keys. La
// en claro solo asoma en el 201 del alta (en Postgres vive el hash).
func panelKeysList(d *Deps) fiber.Handler {
	return panelAuth(d, func(c *fiber.Ctx, key backend.KeyInfo) error {
		keys, err := d.Repo.ListTenantKeys(c.Context(), key.TenantID)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(keys)
	})
}

func panelKeyCreate(d *Deps) fiber.Handler {
	return panelAuth(d, func(c *fiber.Ctx, key backend.KeyInfo) error {
		var body struct {
			Label     string `json:"label"`
			Mode      string `json:"mode"`
			StickyTTL int    `json:"sticky_ttl_s"`
		}
		if err := c.BodyParser(&body); err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		id, plain, err := d.Repo.CreateAPIKey(c.Context(), key.TenantID, body.Label, body.Mode, body.StickyTTL)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		// Warmup del cache del gateway: la key nueva queda visible al
		// instante sin esperar el TTL de la ventana.
		if d.RDB != nil {
			_ = d.RDB.Del(c.Context(), "apikey:"+sha256Hex(plain)).Err()
		}
		return c.Status(http.StatusCreated).JSON(fiber.Map{
			"api_key_id": id, "api_key": plain, "label": body.Label, "once": true,
		})
	})
}

// panelInvoices — GET /v1/panel/invoices?days=30: factura por dia
// (GB, coste, sello reconciled del webhook firmado) + stripe_customer_id.
func panelInvoices(d *Deps) fiber.Handler {
	return panelAuth(d, func(c *fiber.Ctx, key backend.KeyInfo) error {
		days, _ := strconv.Atoi(c.Query("days", "30"))
		customer, invoices, err := d.Repo.TenantInvoices(c.Context(), key.TenantID, days)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "tenant desconocido")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"tenant_id": key.TenantID, "stripe_customer_id": customer, "invoices": invoices,
		})
	})
}

// sha256Hex: mismo hash que backend.HashKey (clave del cache).
func sha256Hex(plain string) string {
	return backend.HashKey(plain)
}
