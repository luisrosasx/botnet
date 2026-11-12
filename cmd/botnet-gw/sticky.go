package main

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Sticky con TTL y rotativo ponderado (EP-09 T-0904, cierra A2-08): los
// modos del contrato POST /api/v1/sessions son contractuales y medidos,
// no promesas de pizarra. Sticky: `sticky:{t}:{client}:{key}` → node_id
// con TTL de `api_keys.sticky_ttl_s`. Rotativo: SWRR (smooth weighted
// round-robin) ponderado por bw_mbps medido y densidad — reparte a la
// vez que proporciona.

// StickyKeyTTLMaxSmallPool: con pool chico (< SmallPoolNodes) el TTL
// efectivo se topa a 120 s para no fijar nodos (AC3, politica asertada).
const (
	SmallPoolNodes      = 50
	SmallPoolStickyMaxT = 120 * time.Second
)

// StickyKey: `sticky:{t}:{client}:{key}` — t es tenant_id.
func StickyKey(tenant, client, key string) string {
	return fmt.Sprintf("sticky:%s:%s:%s", tenant, client, key)
}

// StickyStore: en produccion Redis con TTL; el doble de test es un mapa.
type StickyStore interface {
	Get(ctx context.Context, key string) (string, bool)
	Set(ctx context.Context, key string, nodeID string, ttl time.Duration)
}

// StickyRedisStore adapta go-redis.
type StickyRedisStore struct{ RDB *redis.Client }

func (s *StickyRedisStore) Get(ctx context.Context, key string) (string, bool) {
	v, err := s.RDB.Get(ctx, key).Result()
	if err != nil || v == "" {
		return "", false
	}
	return v, true
}

func (s *StickyRedisStore) Set(ctx context.Context, key string, nodeID string, ttl time.Duration) {
	s.RDB.Set(ctx, key, nodeID, ttl)
}

// StickyBook: seleccion sticky — el mismo (tenant, client, key) repite
// nodo mientras el binding viva (>= 95 % de los requests con pool sano).
type StickyBook struct {
	Store StickyStore
	TTL   time.Duration // de api_keys.sticky_ttl_s
	// PoolSizeTop: tamano del pool local; < SmallPoolNodes topa el TTL.
	PoolSizeTop int
	// Alive: filtro de nodos vivos/quemados (BurnTracker, T-1308).
	Alive func(nodeID string) bool
}

// EffectiveStickyTTL: politica AC3 — pool < SmallPoolNodes => TTL topado
// a SmallPoolStickyMaxT; sticky_ttl_s de la clave manda en pool grande.
func EffectiveStickyTTL(configured time.Duration, poolSize int) time.Duration {
	if poolSize < SmallPoolNodes && configured > SmallPoolStickyMaxT {
		return SmallPoolStickyMaxT
	}
	return configured
}

// Pick: binding vivo → mismo nodo (reused=true); si no, decide `next` (el
// llamante pasa el nodo del rotativo ponderado) y fija el binding con el
// TTL efectivo (reused=false). Un binding roto (nodo muerto) se re-emite:
// sticky no es cadena vital. Devuelve "" solo si `next` agoto el pool.
func (b *StickyBook) Pick(ctx context.Context, tenant, client, key string, next func() string) (string, bool) {
	skey := StickyKey(tenant, client, key)
	if node, ok := b.Store.Get(ctx, skey); ok && (b.Alive == nil || b.Alive(node)) {
		return node, true // reused
	}
	node := next()
	if node == "" {
		return "", false
	}
	ttl := EffectiveStickyTTL(b.TTL, b.PoolSizeTop)
	b.Store.Set(ctx, skey, node, ttl)
	return node, false
}

// StickyMapStore es el doble de test: TTLs reales con reloj inyectable.
type StickyMapStore struct {
	vals map[string]string
	exp  map[string]time.Time
	now  func() time.Time
}

func NewStickyMapStore(now func() time.Time) *StickyMapStore {
	if now == nil {
		now = time.Now
	}
	return &StickyMapStore{vals: map[string]string{}, exp: map[string]time.Time{}, now: now}
}

func (m *StickyMapStore) Get(_ context.Context, key string) (string, bool) {
	v, ok := m.vals[key]
	if !ok {
		return "", false
	}
	if !m.now().Before(m.exp[key]) {
		delete(m.vals, key)
		delete(m.exp, key)
		return "", false
	}
	return v, true
}

func (m *StickyMapStore) Set(_ context.Context, key string, nodeID string, ttl time.Duration) {
	m.vals[key] = nodeID
	m.exp[key] = m.now().Add(ttl)
}

// WeightedNode: entrada del rotativo ponderado.
type WeightedNode struct {
	ID     string
	BWMbps uint16 // bw_mbps del HELLO (T-0901): el peso canonico
}

// WeightedRotator: smooth weighted round-robin (SWRR, scheme nginx) —
// deterministico, proporcional al peso y con maxima dispersion (n
// requests sobre n nodos de peso igual = n nodos distintos). Pondera
// bw_mbps medido; la densidad entra restando peso en el llamante.
type WeightedRotator struct {
	nodes []WeightedNode
	cur   []int // pesos corrientes
}

// NewWeightedRotator exige pesos > 0 (bw_mbps>=1 tras el HELLO).
func NewWeightedRotator(nodes ...WeightedNode) *WeightedRotator {
	r := &WeightedRotator{nodes: append([]WeightedNode(nil), nodes...), cur: make([]int, len(nodes))}
	return r
}

// Next: un sorteo SWRR. Devuelve "" con pool vacio.
func (r *WeightedRotator) Next() string {
	if len(r.nodes) == 0 {
		return ""
	}
	total := 0
	for i, n := range r.nodes {
		r.cur[i] += int(n.BWMbps)
		total += int(n.BWMbps)
	}
	best := 0
	for i := range r.cur {
		if r.cur[i] > r.cur[best] {
			best = i
		}
	}
	r.cur[best] -= total
	return r.nodes[best].ID
}
