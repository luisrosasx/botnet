package backend

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis degradado (A3-X3): caido Redis, el gateway no admite sesiones
// nuevas (503, nunca admision a ciegas), las sesiones vivas siguen en su
// stream (el plano de datos no depende de Redis por byte) y al volver se
// re-anuda presencia por heartbeat y se reconstruye el contador de GB
// desde la fuente autoritativa fria.

// SessionBytes es la cara de Postgres para reconstruir contadores:
// suma de bytes del tenant desde el inicio de periodo.
type SessionBytes interface {
	SumTenantBytes(ctx context.Context, tenantID string, periodStart string) (int64, error)
}

// RedisWatch es el monitor de salud de Redis del gateway/backend: ping
// periodico, bandera atomica para la admision y gancho de recuperacion.
type RedisWatch struct {
	rdb       *redis.Client
	interval  time.Duration
	healthy   atomic.Bool
	rebuilder SessionBytes
	period    string // periodo de facturacion actual (YYYY-MM-DD)
	tenants   func() []string
}

// PingInterval: lo bastante corto para que la ventana de deteccion no
// se coma el contrato de 90 s de caos; lo bastante largo para no ser
// ruido en el plano de control.
const PingInterval = 5 * time.Second

func NewRedisWatch(rdb *redis.Client, rb SessionBytes, period string, tenants func() []string) *RedisWatch {
	w := &RedisWatch{rdb: rdb, interval: PingInterval, rebuilder: rb, period: period, tenants: tenants}
	w.healthy.Store(true)
	return w
}

// AdmissionOK es la puerta de POST /api/v1/sessions: sin Redis no hay
// admision (503), con Redis recuperado la puerta vuelve sola.
func (w *RedisWatch) AdmissionOK() bool { return w.healthy.Load() }

// HealthySnapshot para asserts de test.
func (w *RedisWatch) Healthy() bool { return w.healthy.Load() }

// SetHealthyForTest vuelca la bandera de vigilia sin esperar al ping:
// los tests de caos y degradado la usan para simular la caida real.
func (w *RedisWatch) SetHealthyForTest(ok bool) { w.healthy.Store(ok) }

// Start ejecuta el ciclo de vigilia hasta cancelar ctx. En la transicion
// caido->vivo reconstruye tenant:{t}:gb_used desde Postgres: el gateway
// sobre-cobra dentro de tolerancia (<=0,5 %) porque Redis era el
// acumulador en vivo, no la fuente de verdad.
func (w *RedisWatch) Start(ctx context.Context) error {
	tick := time.NewTicker(w.interval)
	defer tick.Stop()
	var lastErr error
	for {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := w.rdb.Ping(pctx).Err()
		cancel()
		was := w.healthy.Swap(err == nil)
		if err != nil {
			lastErr = err
		} else if !was && w.AdmissionOK() {
			lastErr = w.RebuildCounters(ctx)
		}
		select {
		case <-ctx.Done():
			return lastErr
		case <-tick.C:
		}
	}
}

// RebuildCounters: tenant:{t}:gb_used:{period} se fija desde la suma de
// sessions (autoritativa). Devuelve el error acumulado del batch.
func (w *RedisWatch) RebuildCounters(ctx context.Context) error {
	if w.rebuilder == nil || w.tenants == nil {
		return nil
	}
	var lastErr error
	for _, t := range w.tenants() {
		gb, err := w.rebuilder.SumTenantBytes(ctx, t, w.period)
		if err != nil {
			lastErr = err
			continue
		}
		key := fmt.Sprintf("tenant:%s:gb_used:%s", t, w.period)
		val := floatBytes(gb)
		if err := w.rdb.Set(ctx, key, val, 0).Err(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// TouchLive es la presencia re-anudada por heartbeat (§9):
// node:{id}:live con TTL 180 s; el heartbeat lo escribe en cada latido,
// por eso la presencia vuelve en <=180 s tras recuperar Redis sin
// re-enrolls.
func TouchLive(ctx context.Context, rdb *redis.Client, nodeID string, ttl time.Duration) error {
	if err := rdb.Set(ctx, "node:"+nodeID+":live", "1", ttl).Err(); err != nil {
		return fmt.Errorf("backend: presence: %w", err)
	}
	return nil
}

// floatBytes convierte bytes a GB (numeric tolerante en Redis).
func floatBytes(b int64) float64 { return float64(b) / (1024 * 1024 * 1024) }
