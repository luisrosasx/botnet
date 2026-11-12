// Package middleware: transversales de las rutas /v1 del backend
// (EP-07 T-0706). Hoy solo hay uno con contrato duro: la ventana de
// la puerta legal (A3-13) — el node_id uuid es enumerable, la baja no.
package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// LeaveRateLimitPerWindow y LeaveWindow: 60 requests por ventana de
// 60 s por IP (AC2: 61 requests en la ventana -> >= 1 429).
const (
	LeaveRateLimitPerWindow = 60
	LeaveWindow             = 60 * time.Second
)

// LeaveRateKey es la clave del criterio: rate:leave:{ip}.
func LeaveRateKey(ip string) string { return "rate:leave:" + ip }

// Limiter: ventana fija por clave. err != nil se trata como
// fail-open: la puerta legal nunca puede producir un 503 por culpa
// del almacén (los 200k curls de A3-13 piden exactamente eso).
type Limiter interface {
	Allow(ctx context.Context, key string) (ok bool, err error)
}

type win struct {
	count int
	until time.Time
}

// FixedWindow: ventana fija en memoria — el doble determinista del
// test y el modo dev, con reloj inyectable (sin sleeps).
type FixedWindow struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[string]*win
}

func NewFixedWindow(limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{limit: limit, window: window, now: time.Now, hits: map[string]*win{}}
}

// SetClock inyecta el reloj del test (nil = time.Now).
func (f *FixedWindow) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if now != nil {
		f.now = now
	}
}

// Allow: primer golpe de la clave abre la ventana; los siguientes se
// cuentan hasta el limite. La ventana es dura: sin sliding (la AC2 es
// un barrido, no una rafaga).
func (f *FixedWindow) Allow(_ context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	w := f.hits[key]
	if w == nil || !now.Before(w.until) {
		w = &win{count: 0, until: now.Add(f.window)}
		f.hits[key] = w
	}
	w.count++
	return w.count <= f.limit, nil
}

// RedisWindow: implementacion de produccion (INCR + EXPIRE al primer
// golpe). Mismo contrato que FixedWindow; el backend levita entre una
// y otra sin tocar handlers.
type RedisWindow struct {
	RDB    *redis.Client
	Limit  int
	Window time.Duration
}

func (r *RedisWindow) Allow(ctx context.Context, key string) (bool, error) {
	if r == nil || r.RDB == nil {
		return true, nil
	}
	n, err := r.RDB.Incr(ctx, key).Result()
	if err != nil {
		return true, err // fail-open documentado arriba
	}
	if n == 1 {
		_ = r.RDB.Expire(ctx, key, r.Window).Err()
	}
	return n <= int64(r.Limit), nil
}

// LeaveRateLimit monta la ventana sobre la ruta de baja. nil Limiter
// es pasamanos (dev/test sin Redis). El 429 lleva el contrato JSON
// del backend: {"error": ...}.
func LeaveRateLimit(l Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if l == nil {
			return c.Next()
		}
		ok, err := l.Allow(c.Context(), LeaveRateKey(c.IP()))
		if err == nil && !ok {
			return c.Status(429).JSON(fiber.Map{"error": "rate:leave excedido (60/60 s)"})
		}
		return c.Next()
	}
}

// TransparencyRateKey: la pagina publica del dueño (T-1101, A3-13) es
// publica y aun asi la reban la ventana — rate:transparency:{ip}.
func TransparencyRateKey(ip string) string { return "rate:transparency:" + ip }

// TransparencyRateLimit: misma ventana 60/60 s que la puerta de baja,
// clave propia. El dueño sin curl hace UN solo GET; el scraper hace
// 200k y recibe 429 a partir del golpe 61 (AC3 de T-1101).
func TransparencyRateLimit(l Limiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if l == nil {
			return c.Next()
		}
		ok, err := l.Allow(c.Context(), TransparencyRateKey(c.IP()))
		if err == nil && !ok {
			return c.Status(429).JSON(fiber.Map{"error": "rate:transparency excedido (60/60 s)"})
		}
		return c.Next()
	}
}
