package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Politica de seleccion de sessions (T-1308, A1-17): la forma por defecto
// de la red es la que cuida la prima residencial — rotativo con TTL, y
// los nodos calientes (mas de 150 sesiones/dia) se queman 24 h. El
// contrato del gateway (POST /api/v1/sessions) no admite modo ausente:
// `mode` que no venga en el body es `rotating`, no sticky.

// SessionMode es el modo del contrato de sessions.
type SessionMode string

const (
	ModeRotating SessionMode = "rotating"
	ModeSticky   SessionMode = "sticky"
)

// DefaultRotationTTL: cuanto dura un binding rotativo antes de poder
// moverse de nodo; es el reloj del rotativo (A1-17 "modo rotativo con
// TTL"). En el contrato viaja en ttl_s.
const DefaultRotationTTL = 30 * time.Minute

// SessionRequest es el payload de POST /api/v1/sessions.
type SessionRequest struct {
	TenantID        string
	Mode            SessionMode
	TTL             time.Duration
	ConcurrentLimit int
}

// UnmarshalJSON aplica los defaults del contrato: mode ausente o vacio es
// rotating (AC1: el modo rotativo con TTL es default, exigencia de A1) y
// ttl ausente es DefaultRotationTTL.
func (r *SessionRequest) UnmarshalJSON(b []byte) error {
	var raw struct {
		TenantID        string  `json:"tenant_id"`
		Mode            *string `json:"mode"`
		TTLS            *int    `json:"ttl_s"`
		ConcurrentLimit int     `json:"concurrent_limit"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	r.TenantID = raw.TenantID
	r.ConcurrentLimit = raw.ConcurrentLimit
	r.Mode = ModeRotating
	if raw.Mode != nil && *raw.Mode != "" {
		switch m := SessionMode(*raw.Mode); m {
		case ModeRotating, ModeSticky:
			r.Mode = m
		default:
			return fmt.Errorf("sessions: mode %q invalido", *raw.Mode)
		}
	}
	r.TTL = DefaultRotationTTL
	if raw.TTLS != nil && *raw.TTLS > 0 {
		r.TTL = time.Duration(*raw.TTLS) * time.Second
	}
	return nil
}

// HotNodePolicy: quemado temporal por calor (AC2): un nodo que supera
// MaxSessionsPerDay sesiones en un dia natural queda fuera de seleccion
// durante Cooloff (24 h); despues vuelve con el contador a cero.
type HotNodePolicy struct {
	MaxSessionsPerDay int
	Cooloff           time.Duration
}

func NewHotNodePolicy() *HotNodePolicy {
	return &HotNodePolicy{MaxSessionsPerDay: 150, Cooloff: 24 * time.Hour}
}

// BurnTracker lleva el conteo diario por nodo y los quemados en curso.
type BurnTracker struct {
	policy   *HotNodePolicy
	sessions map[string]map[int]int // node -> dia -> sesiones
	coolOff  map[string]time.Time   // node -> elegible desde
}

func NewBurnTracker(p *HotNodePolicy) *BurnTracker {
	return &BurnTracker{policy: p, sessions: map[string]map[int]int{}, coolOff: map[string]time.Time{}}
}

func dayOf(t time.Time) int {
	y, d := t.Year(), t.YearDay()
	return y*1000 + d
}

// Record anota una asignacion; al cruzar el tope diario el nodo queda
// quemado hasta t+Cooloff. Devuelve true si el registro provoco el
// quemado (para logs).
func (b *BurnTracker) Record(node string, t time.Time) bool {
	d := dayOf(t)
	if b.sessions[node] == nil {
		b.sessions[node] = map[int]int{}
	}
	n := b.sessions[node][d] + 1
	b.sessions[node][d] = n
	if n == b.policy.MaxSessionsPerDay+1 && b.coolOff[node].IsZero() {
		b.coolOff[node] = t.Add(b.policy.Cooloff)
		return true
	}
	return false
}

// Eligible: fuera de la ventana de quemado el nodo vuelve a la seleccion
// (AC2: count de selecciones = 0 en la ventana, y solo en la ventana).
func (b *BurnTracker) Eligible(node string, t time.Time) bool {
	until, ok := b.coolOff[node]
	return !ok || !t.Before(until)
}

// CoolOffUntil es el fin del quemado (cero si esta sano).
func (b *BurnTracker) CoolOffUntil(node string) time.Time { return b.coolOff[node] }

// RotatingPool: rotativo round-robin con TTL sobre nodos elegibles; la
// quema por calor vive en el BurnTracker compartido.
type RotatingPool struct {
	nodeIDs []string
	next    int
	tracker *BurnTracker
	binds   map[string]bind // tenant -> (nodo, expira)
}

type bind struct {
	node   string
	expire time.Time
}

func NewRotatingPool(tracker *BurnTracker, nodeIDs ...string) *RotatingPool {
	return &RotatingPool{nodeIDs: nodeIDs, tracker: tracker, binds: map[string]bind{}}
}

// Select: un tenant con bind vivo conserva su nodo (TTL); si no, entra por
// round-robin sobre los elegibles. Devuelve "" si todos estan quemados.
func (p *RotatingPool) Select(tenant string, t time.Time, ttl time.Duration) string {
	if bd, ok := p.binds[tenant]; ok && t.Before(bd.expire) && p.tracker.Eligible(bd.node, t) {
		return bd.node
	}
	n := len(p.nodeIDs)
	for i := 0; i < n; i++ {
		cand := p.nodeIDs[(p.next+i)%n]
		if p.tracker.Eligible(cand, t) {
			p.next = (p.next + i + 1) % n
			p.tracker.Record(cand, t)
			p.binds[tenant] = bind{node: cand, expire: t.Add(ttl)}
			return cand
		}
	}
	return ""
}

// LatencyModel modela la latencia de admision del gateway para la
// simulacion mixta 70/30 (AC3): el rotativo reparte y paga poco; un
// tenant sticky que supera su concurrent_limit hace cola — y la cola no
// debe tumbar el p99 del rotativo por debajo de 1.500 ms.
type LatencyModel struct {
	BaseMs          float64 // red + TLS + SOCKS (250)
	RotationSpread  float64 // ms por sesion concurrente en el nodo rotativo
	StickyQueueMs   float64 // ms por sesion concurrente por encima del limite
	StickyRetryMult float64 // reintentos del cliente sticky (amplificacion)
}

func NewLatencyModel() *LatencyModel {
	return &LatencyModel{BaseMs: 250, RotationSpread: 6, StickyQueueMs: 120, StickyRetryMult: 1.15}
}

// MixedLatencySim: `sessions` aperturas mixtas (70 % rotativo, 30 %
// sticky con `stickyOver` sesiones por encima de su concurrent_limit);
// `concurrent` es la carga concurrente del nodo rotativo (el tope de
// densidad de T-1304 lo topa en 8 tenants). Devuelve el p99 en ms.
func (m *LatencyModel) MixedLatencySim(sessions int, concurrent int, stickyOver int, seed int64) float64 {
	rnd := rand.New(rand.NewSource(seed))
	lat := make([]float64, 0, sessions)
	for i := 0; i < sessions; i++ {
		jitter := rnd.Float64() * 120 // pila TCP del router + RTT variable
		if rnd.Float64() < 0.70 {
			lat = append(lat, m.BaseMs+float64(concurrent)*m.RotationSpread+jitter)
			continue
		}
		// sticky saturado: cola por encima del limite + reintentos
		q := float64(stickyOver) * m.StickyQueueMs * m.StickyRetryMult
		lat = append(lat, m.BaseMs+q+jitter)
	}
	return p99Ms(lat)
}

func p99Ms(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]float64(nil), v...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	idx := int(math.Ceil(0.99*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	return cp[idx]
}

// String es util para logs/aserciones.
func (r SessionRequest) String() string {
	return fmt.Sprintf("session{tenant:%s mode:%s ttl:%s limit:%d}", r.TenantID, r.Mode, r.TTL, r.ConcurrentLimit)
}
