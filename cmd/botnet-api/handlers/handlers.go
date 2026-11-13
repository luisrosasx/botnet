// Package handlers: capa HTTP del backend (Fiber). Solo transporte:
// parsear, invocar el service, tipar el codigo de estado. La logica
// vive en services/ y el SQL en repo/ (BLUEPRINT §9).
package handlers

import (
	"bytes"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/cmd/botnet-api/middleware"
	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
	"github.com/botnet/botnet/internal/accounting"
	"github.com/botnet/botnet/internal/backend"
)

// Deps es el cable completo del backend; nil en RDB/Keys apaga las
// rutas que dependen de Redis (sesiones devuelven 503: sin Redis no hay
// admision, A3-X3).
type Deps struct {
	Cfg   backend.Config
	Repo  *repo.Data
	RDB   *redis.Client
	Keys  *backend.ApiKeyCache
	Watch *backend.RedisWatch
	CA    *services.CA
	Alloc Allocator
	JS    NATSPub // nil = plano NATS apagado
	Log   *bytes.Buffer

	// Plano contable EP-10: sink del flush del nodo y dedup por
	// flush_id. Nil apaga POST /v1/nodes/{id}/flush (503).
	FlushSink accounting.FlushSink  // production: FlushRepo{Repo}
	Dedup     accounting.DedupStore // production: RedisDedup{RDB}

	// StripeSecret es el secreto HMAC del webhook firmado (mismo
	// esquema firma/verifica; la simulacion local lo usa igual).
	StripeSecret string

	// PanelOrigins: orígenes del panel para CORS (S-20). Vacío = sin CORS.
	// Antes el navegador exigía --disable-web-security para el cruce de
	// origen del panel; con esto el preflight pasa solo.
	PanelOrigins string

	// LeaveCodeTTL: caducidad del codigo de baja (0 = no caduca). nowFn
	// permite reloj falso en los tests (S-18).
	LeaveCodeTTL time.Duration
	nowFn        func() time.Time

	// LeaveLimiter: ventana rate:leave:{ip} de la puerta legal
	// (EP-07 T-0706, A3-13). nil = sin limite (dev/test).
	LeaveLimiter middleware.Limiter

	// TransparentLimiter: ventana rate:transparency:{ip} de la
	// pagina publica GET /t/{slug} (EP-11 T-1101). nil = sin limite.
	TransparentLimiter middleware.Limiter

	// AccessLog: fichero abierto en append donde el middleware
	// accessLog anota una linea por peticion. nil = sin registro
	// (dev/tests); produccion: BOTNET_ACCESS_LOG.
	AccessLog *os.File
}

// NewApp monta la superficie HTTP versionada: control de flota para el
// agente (/v1/...), transparencia para el dueño (/t/...) y la puerta
// del cliente (/api/v1/...). El gateway (botnet-gw) sirve la misma
// /api/v1/sessions; aqui la dejamos montada porque el contrato vive en
// el backend y el gateway «consume los mismos services».
func NewApp(d *Deps) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "botnet-api",
		DisableStartupMessage: true,
	})
	if d.PanelOrigins != "" {
		app.Use(cors.New(cors.Config{
			AllowOrigins: d.PanelOrigins,
			AllowMethods: "GET,POST,OPTIONS",
			AllowHeaders: "Authorization,Content-Type,X-Device-Token,X-Node-Id",
			MaxAge:       600,
		}))
	}
	app.Use(accessLog(d)) // una linea por peticion si Deps.AccessLog != nil
	app.Get("/healthz", healthz(d))
	app.Get("/t/:slug", middleware.TransparencyRateLimit(d.TransparentLimiter), transparency(d)) // EP-11 T-1101
	app.Get("/v1/nodes", listNodes(d))                                                           // EP-11 T-1107
	app.Post("/v1/enroll", enroll(d))
	app.Post("/v1/heartbeat/:node_id", heartbeat(d))
	app.Post("/v1/heartbeat", heartbeat(d)) // ruta a pelo del agente: X-Node-Id o NodeID del frame (T-0308 AC2)
	app.Post("/v1/nodes/:node_id/revoke", revoke(d))
	app.Post("/api/v1/sessions", createSession(d))
	app.Delete("/api/v1/sessions/:session_id", closeSession(d))
	app.Post("/v1/nodes/:node_id/flush", flushNode(d))
	app.Post("/v1/nodes/:node_id/geo", geoProbe(d))                                           // EP-07 T-0704
	app.Post("/v1/nodes/:node_id/leave", middleware.LeaveRateLimit(d.LeaveLimiter), leave(d)) // EP-07 T-0706
	app.Post("/v1/billing/webhook", billingWebhook(d))
	app.Post("/v1/webhooks/stripe", billingWebhook(d)) // alias literal del plano Stripe (T-1104 AC1)
	// Panel del tenant (EP-11 T-1105): todo con Bearer api key.
	app.Get("/v1/panel/usage", panelUsage(d))
	app.Get("/v1/panel/quotas", panelQuotas(d))
	app.Patch("/v1/panel/quotas", panelQuotaPatch(d))
	app.Get("/v1/panel/api-keys", panelKeysList(d))
	app.Post("/v1/panel/api-keys", panelKeyCreate(d))
	app.Get("/v1/panel/invoices", panelInvoices(d))
	return app
}

// jsonError responde {"error": msg} con el codigo tipado del contrato.
func jsonError(c *fiber.Ctx, code int, msg string) error {
	return c.Status(code).JSON(fiber.Map{"error": msg})
}
