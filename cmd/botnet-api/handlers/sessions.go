package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/tunnel"
)

// Allocator: el gateway emite el nonce de sesion en la asignacion
// (T-0808). En produccion es un req/reply NATS a gw.{gw_id}.alloc; el
// InlineAllocator lo resuelve in-process para tests y dev.
type Allocator interface {
	Assign(ctx context.Context, tenantID, nodeID string) (uint32, error)
}

// InlineAllocator emite nonces con la libreria del tunel (mismo codigo
// que usa el gateway por NATS).
type InlineAllocator struct{}

func (InlineAllocator) Assign(context.Context, string, string) (uint32, error) {
	return tunnel.IssueSessionNonce()
}

// NATSPub es la cara de publicacion del backend (nil = sin NATS).
type NATSPub interface {
	PublishNodeState(nodeID string, payload []byte)
	PublishQuotaExhausted(tenantID string)
}

// sessionView es el 201 del contrato T-0805; nonce es extension
// documentada en openapi (el cliente la ecota en OPEN_STREAM, T-0808).
type sessionView struct {
	SessionID int64  `json:"session_id"`
	ProxyURL  string `json:"proxy_url"`
	ExpiresAt string `json:"expires_at"`
	Nonce     uint32 `json:"nonce"`
}

// createSession — POST /api/v1/sessions (injerto 9): Bearer api key +
// {country, city?, asn?, mode} → 201 {session_id, proxy_url, expires_at,
// nonce}. Credencial 403, cuota 402, rate-limit por tenant 429, Redis
// caido 503 (A3-X3: sin admision a ciegas).
func createSession(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if d.RDB == nil || d.Keys == nil || d.Watch == nil {
			return jsonError(c, http.StatusServiceUnavailable, backend.ErrRedisDown.Error())
		}
		if !d.Watch.AdmissionOK() {
			return jsonError(c, http.StatusServiceUnavailable, backend.ErrRedisDown.Error())
		}
		bearer := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
		if bearer == "" || strings.HasPrefix(c.Get("Authorization"), "Basic") {
			return jsonError(c, http.StatusForbidden, "api key requerida")
		}
		key, err := d.Keys.Validate(c.Context(), bearer)
		if errors.Is(err, backend.ErrKeyUnknown) || errors.Is(err, backend.ErrKeyRevoked) {
			return jsonError(c, http.StatusForbidden, "api key invalida o revocada")
		}
		if err != nil {
			return jsonError(c, http.StatusServiceUnavailable, err.Error())
		}
		if st, err := d.Repo.TenantStatus(c.Context(), key.TenantID); err != nil || st != "active" {
			return jsonError(c, http.StatusForbidden, "tenant no activo")
		}

		var req struct {
			Country string `json:"country"`
			City    string `json:"city"`
			ASN     int    `json:"asn"`
			Mode    string `json:"mode"`
		}
		if err := c.BodyParser(&req); err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		if len(req.Country) != 2 {
			return jsonError(c, http.StatusBadRequest, "country ISO-3166-1 alfa-2 requerida")
		}
		if req.Mode != "sticky" && req.Mode != "rotating" {
			req.Mode = key.Mode // la key fija el modo salvo pedido explicito valido
		}

		// rate-limit por tenant (429): INCR con ventana de 1 s.
		rlKey := "rate:sess:" + key.TenantID
		n, rerr := d.RDB.Incr(c.Context(), rlKey).Result()
		if rerr == nil && n == 1 {
			d.RDB.Expire(c.Context(), rlKey, time.Second)
		}
		if rerr == nil && n > d.Cfg.RateLimitRPS {
			return jsonError(c, http.StatusTooManyRequests, "rate-limit por tenant")
		}

		quota, qerr := d.Repo.QuotaFor(c.Context(), key.TenantID)
		if qerr != nil && !errors.Is(qerr, repo.ErrNotFound) {
			return jsonError(c, http.StatusInternalServerError, qerr.Error())
		}
		if qerr == nil {
			if used := d.gbUsed(c.Context(), key.TenantID, quota.PeriodStart); used >= quota.GBAllowance {
				d.markQuotaExhausted(c.Context(), key.TenantID)
				return jsonError(c, http.StatusPaymentRequired, "cuota de GB agotada")
			}
			if open, cerr := d.Repo.CountOpenForTenant(c.Context(), key.TenantID); cerr == nil && open >= quota.ConcurrentLimit {
				return jsonError(c, http.StatusTooManyRequests, "cupos concurrentes agotados")
			}
		}

		nodeID, perr := d.Repo.PickNode(c.Context(), strings.ToUpper(req.Country), req.City, req.ASN)
		if perr != nil {
			return jsonError(c, http.StatusInternalServerError, perr.Error())
		}
		if nodeID == "" {
			return jsonError(c, http.StatusServiceUnavailable, "pool geo sin nodos vivos")
		}
		nonce, aerr := d.Alloc.Assign(c.Context(), key.TenantID, nodeID)
		if aerr != nil {
			return jsonError(c, http.StatusServiceUnavailable, aerr.Error())
		}
		sid, ierr := d.Repo.InsertSession(c.Context(), nodeID, key.TenantID, key.KeyID, c.IP(), d.Cfg.GWAddr)
		if ierr != nil {
			return jsonError(c, http.StatusInternalServerError, ierr.Error())
		}
		ttl := d.Cfg.SessionTTLDur
		if req.Mode == "sticky" {
			ttl = time.Duration(key.StickyTTL) * time.Second
			d.RDB.Set(c.Context(), stickyKey(key.TenantID, c.IP(), key.KeyHash), nodeID, ttl)
		}
		d.RDB.IncrBy(c.Context(), "tenant:"+key.TenantID+":concurrent", 1)
		d.RDB.Expire(c.Context(), "tenant:"+key.TenantID+":concurrent", 60*time.Second)
		if d.JS != nil {
			d.JS.PublishNodeState(nodeID, []byte(fmt.Sprintf(`{"session_id":%d,"state":"assigned"}`, sid)))
		}
		_ = d.Repo.TouchKeyLastUsed(c.Context(), key.KeyID)

		return c.Status(http.StatusCreated).JSON(sessionView{
			SessionID: sid,
			ProxyURL:  "socks5h://" + d.Cfg.GWAddr,
			ExpiresAt: time.Now().Add(ttl).UTC().Format(time.RFC3339),
			Nonce:     nonce,
		})
	}
}

// gbUsed: el contador vivo esta en Redis; en frio se calcula desde
// sessions (fuente autoritativa) y se fija (T-0806 tolerancia 0,5 %).
func (d *Deps) gbUsed(ctx context.Context, tenantID, period string) float64 {
	key := "tenant:" + tenantID + ":gb_used:" + period
	if v, err := d.RDB.Get(ctx, key).Float64(); err == nil {
		return v
	}
	sum, err := d.Repo.SumTenantBytes(ctx, tenantID, period)
	if err != nil {
		return 0
	}
	gb := float64(sum) / (1024 * 1024 * 1024)
	d.RDB.Set(ctx, key, gb, 0).Err()
	return gb
}

// markQuotaExhausted: cache del corte quota:{t}:state TTL 30 s + NATS
// quota.exhausted (invalida la carrera del cache, A3-04).
func (d *Deps) markQuotaExhausted(ctx context.Context, tenantID string) {
	d.RDB.Set(ctx, "quota:"+tenantID+":state", "exhausted", 30*time.Second).Err()
	if d.JS != nil {
		d.JS.PublishQuotaExhausted(tenantID)
	}
}

// closeSession — DELETE /api/v1/sessions/{id}: GOAWAY_SESSION del
// stream; 404 si la sesion es de otro tenant o ya estaba cerrada.
func closeSession(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if d.RDB == nil || d.Keys == nil {
			return jsonError(c, http.StatusServiceUnavailable, backend.ErrRedisDown.Error())
		}
		sid, err := strconv.ParseInt(c.Params("session_id"), 10, 64)
		if err != nil {
			return jsonError(c, http.StatusNotFound, "sesion desconocida")
		}
		bearer := strings.TrimPrefix(c.Get("Authorization"), "Bearer ")
		key, kerr := d.Keys.Validate(c.Context(), bearer)
		if kerr != nil {
			return jsonError(c, http.StatusForbidden, "api key invalida")
		}
		if err := d.Repo.CloseSession(c.Context(), sid, "client", key.TenantID); err != nil {
			return jsonError(c, http.StatusNotFound, "sesion desconocida")
		}
		return c.Status(http.StatusNoContent).Send(nil)
	}
}

func stickyKey(tenant, client, keyHash string) string {
	return "sticky:" + tenant + ":" + client + ":" + keyHash
}
