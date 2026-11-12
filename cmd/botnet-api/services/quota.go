// services/quota.go — T-1004 (cierra A2-20, A3-04): la cuota del
// tenant es de VOLUMEN — tenant:{t}:gb_used:{period} contra
// quotas.gb_allowance. Agotada, el estado quota:{t}:state=exhausted y
// el evento NATS quota.exhausted cortan las sesiones nuevas en <60 s;
// la carrera del cache de 30 s la absorbe la tolerancia contable,
// nunca la factura (los ~30 s sobre-medidos no viajan a Stripe).
package services

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// QuotaCacheTTL es la ventana del cache quota:{t}:state. Es la
// carrera documentada de A3-04; NATS quota.exhausted la invalida al
// instante y el peor caso (30 s) queda bajo el corte de 60 s.
const QuotaCacheTTL = 30 * time.Second

// QuotaCutBudget es el SLA de puerta: sesion nueva rechazada con 402
// en menos de 60 s desde el agotamiento.
const QuotaCutBudget = 60 * time.Second

// ErrQuotaExhausted: el gate lo traduce a 402 en el handler.
var ErrQuotaExhausted = errors.New("quota: GB agotado")

// QuotaPublisher es la cara NATS del corte (payload: tenant_id). En
// produccion es nats.Publisher.PublishQuotaExhausted.
type QuotaPublisher interface {
	PublishQuotaExhausted(tenantID string)
}

// QuotaGate pone la puerta de cuota sobre Redis. Now es inyectable:
// el cronometro del corte lo mide un reloj controlado, no time.Sleep.
type QuotaGate struct {
	RDB *redis.Client
	JS  QuotaPublisher // nil = corte sin evento (cache TTL manda)
	Now func() time.Time
}

func (g *QuotaGate) now() time.Time {
	if g.Now == nil {
		return time.Now()
	}
	return g.Now()
}

// GBUsedKey es el contador vivo del periodo: tenant:{t}:gb_used:{period}.
func GBUsedKey(tenantID, period string) string {
	return "tenant:" + tenantID + ":gb_used:" + period
}

// QuotaStateKey es el cache del corte: quota:{t}:state.
func QuotaStateKey(tenantID string) string {
	return "quota:" + tenantID + ":state"
}

// UsedGB lee el contador del periodo; 0 si aun no existe (el nodo no
// ha flushado nada del periodo).
func (g *QuotaGate) UsedGB(ctx context.Context, tenantID, period string) (float64, error) {
	v, err := g.RDB.Get(ctx, GBUsedKey(tenantID, period)).Float64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return v, err
}

// State devuelve el estado cacheado ("exhausted" o "" = activo).
func (g *QuotaGate) State(ctx context.Context, tenantID string) (string, error) {
	v, err := g.RDB.Get(ctx, QuotaStateKey(tenantID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	return v, err
}

// Check decide la sesion nueva: si el cache dice exhausted, 402
// inmediato; si no, compara el contador con el allowance y, al
// cruzarlo, fija el estado y publica quota.exhausted. Devuelve el
// instante en que el corte quedo efectivo para el cronometro del test.
type QuotaDecision struct {
	Exhausted  bool
	UsedGB     float64
	Allowance  float64
	CutAt      time.Time // cero si sigue activo; instante del corte si no
	CacheState bool      // true si el corte venia del cache (carrera TTL)
}

// Check aplica la decision sobre el periodo dado.
func (g *QuotaGate) Check(ctx context.Context, tenantID, period string, allowance float64) (QuotaDecision, error) {
	d := QuotaDecision{Allowance: allowance}
	if st, err := g.State(ctx, tenantID); err != nil {
		return d, err
	} else if st == "exhausted" {
		d.Exhausted, d.CacheState, d.CutAt = true, true, g.now()
		return d, nil
	}
	used, err := g.UsedGB(ctx, tenantID, period)
	if err != nil {
		return d, err
	}
	d.UsedGB = used
	if allowance > 0 && used >= allowance {
		if err := g.Exhaust(ctx, tenantID); err != nil {
			return d, err
		}
		d.Exhausted, d.CutAt = true, g.now()
	}
	return d, nil
}

// Exhaust fija quota:{t}:state=exhausted (TTL 30 s) y publica
// quota.exhausted: quien escucha el evento invalida su cache local.
func (g *QuotaGate) Exhaust(ctx context.Context, tenantID string) error {
	if err := g.RDB.Set(ctx, QuotaStateKey(tenantID), "exhausted", QuotaCacheTTL).Err(); err != nil {
		return err
	}
	if g.JS != nil {
		g.JS.PublishQuotaExhausted(tenantID)
	}
	return nil
}

// OnQuotaExhausted es la mano del lado NATS: el evento quota.exhausted
// invalida el cache de 30 s (los ~30 s de carrera quedan absorbidos
// por la tolerancia, no por la factura — A3-04).
func (g *QuotaGate) OnQuotaExhausted(ctx context.Context, tenantID string) error {
	return g.RDB.Del(ctx, QuotaStateKey(tenantID)).Err()
}

// CutWithin60s es el aserto del SLA: el corte (instante efectivo del
// 402 menos el instante de agotamiento) no pasa del budget.
func CutWithin60s(exhaustedAt, cutAt time.Time) bool {
	d := cutAt.Sub(exhaustedAt)
	if d < 0 {
		d = -d
	}
	return d <= QuotaCutBudget
}
