package proxy

import (
	"context"
	"io"
	"sync"
	"time"
)

// Corte por cuota (§7, A3-04): Redis quota:{t}:state=exhausted y el
// evento NATS quota.exhausted invalidan la carrera del cache de 30 s. El
// agente responde con GOAWAY por session_id y closed_reason='quota' —
// causa tipada, separada de 'error' y 'client' a efectos contables — y
// libera stream y contadores al instante; el corte completo vive bajo los
// 60 s del SLA de puerta.

// ClosedReason es la causa tipada de cierre de sesion (sessions.closed_reason).
type ClosedReason string

const (
	ClosedQuota  ClosedReason = "quota"
	ClosedError  ClosedReason = "error"
	ClosedClient ClosedReason = "client"
	ClosedIdle   ClosedReason = "idle"
	ClosedBye    ClosedReason = "bye"
)

// SubjectQuotaExhausted es el sujeto NATS que notifica el agotamiento de
// cuota por tenant (payload: tenant_id).
const SubjectQuotaExhausted = "quota.exhausted"

// QuotaStateExhausted es el valor de Redis quota:{t}:state que dispara el
// corte.
const QuotaStateExhausted = "exhausted"

// QuotaCacheTTL es la ventana del cache local de estado de cuota. Su
// carrera admite sobre-precio: lo cobrado de mas queda acotado por
// MaxOverchargeBytes (T-0605 AC2).
const QuotaCacheTTL = 30 * time.Second

// MaxOverchargeBytes acota lo que un tenant puede consumir de mas tras
// agotar cuota, por sesion viva: TTL del cache x tope del bucket. A
// 5 Mbps (640 KiB/s): 30 s x 655.360 B = 18,75 MiB <= 19 MiB por sesion
// cortada.
func MaxOverchargeBytes(bytesPerSec float64) float64 {
	return QuotaCacheTTL.Seconds() * bytesPerSec
}

// Session es una sesion viva del tenant: su stream H2 y su causa de
// cierre. El registro la conoce para poder lanzarle el GOAWAY.
type Session struct {
	SessionID string
	TenantID  string
	OpenedAt  time.Time

	stream io.Closer

	mu       sync.Mutex
	closed   bool
	reason   ClosedReason
	registry *Registry
}

// CloseWith cierra el stream con causa tipada. Idempotente: el primer
// cierre gana; el stream se cierra una sola vez.
func (s *Session) CloseWith(reason ClosedReason) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.reason = reason
	stream := s.stream
	s.mu.Unlock()

	if stream != nil {
		_ = stream.Close()
	}
	if s.registry != nil {
		s.registry.recordClosed(s.SessionID, reason)
	}
}

// Reason devuelve la causa con la que cerro (vacio si sigue viva).
func (s *Session) Reason() ClosedReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Alive informa si la sesion sigue viva.
func (s *Session) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

// CloseSink es la pieza contable: toda sesion cerrada deja su causa
// tipada (sessions.closed_reason en Postgres — en tests, un doble).
type CloseSink interface {
	Closed(sessionID string, reason ClosedReason)
}

// Registry lleva las sesiones vivas por id y por tenant.
type Registry struct {
	mu        sync.Mutex
	byID      map[string]*Session
	byTenant  map[string]map[string]*Session
	sink CloseSink
}

// NewRegistry crea el registro; sink puede ser nil (solo GOAWAY).
func NewRegistry(sink CloseSink) *Registry {
	return &Registry{
		byID:     make(map[string]*Session),
		byTenant: make(map[string]map[string]*Session),
		sink:     sink,
	}
}

// Register alta una sesion viva. Reutiliza el id si existiera (idempotente).
func (r *Registry) Register(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.registry = r
	r.byID[s.SessionID] = s
	m := r.byTenant[s.TenantID]
	if m == nil {
		m = make(map[string]*Session)
		r.byTenant[s.TenantID] = m
	}
	m[s.SessionID] = s
}

// Unregister quita la sesion del registro (cierre natural del relay).
func (r *Registry) Unregister(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.byID[sessionID]
	delete(r.byID, sessionID)
	if s != nil {
		if m := r.byTenant[s.TenantID]; m != nil {
			delete(m, sessionID)
			if len(m) == 0 {
				delete(r.byTenant, s.TenantID)
			}
		}
	}
}

// TenantSessions devuelve copia de las sesiones vivas del tenant.
func (r *Registry) TenantSessions(tenantID string) []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.byTenant[tenantID]
	out := make([]*Session, 0, len(m))
	for _, s := range m {
		out = append(out, s)
	}
	return out
}

// Count devuelve el numero de sesiones vivas.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byID)
}

// recordClosed notifica al sink la causa tipada de un cierre.
func (r *Registry) recordClosed(sessionID string, reason ClosedReason) {
	r.mu.Lock()
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink.Closed(sessionID, reason)
	}
}

// StateReader lee quota:{tenant}:state (Redis). En tests, un fixture.
type StateReader interface {
	QuotaState(ctx context.Context, tenantID string) (string, error)
}

// EventSource suscribe al plano de eventos (NATS). En tests, un doble que
// entrega payloads a demanda.
type EventSource interface {
	Subscribe(subject string, fn func(payload []byte)) error
}

// Guard aplica el corte por cuota: ante quota.exhausted lanza GOAWAY con
// closed_reason='quota' a las sesiones vivas del tenant y libera
// contadores. La puerta completa (evento → stream liberado) se mide en
// el test y queda muy por debajo de los 60 s del SLA.
type Guard struct {
	reg       *Registry
	reader    StateReader
	now       func() time.Time
	cacheTTL  time.Duration
	mu        sync.Mutex
	cached    map[string]cacheEntry
}

type cacheEntry struct {
	state string
	at    time.Time
}

// NewGuard crea el guard. reader puede ser nil si solo se usa la via de
// eventos; rate no hace falta aqui: el tope de sobre-precio lo da
// MaxOverchargeBytes sobre el bucket vigente.
func NewGuard(reg *Registry, reader StateReader) *Guard {
	return &Guard{
		reg:      reg,
		reader:   reader,
		now:      func() time.Time { return time.Now().UTC() },
		cacheTTL: QuotaCacheTTL,
		cached:   make(map[string]cacheEntry),
	}
}

// Start suscribe el sujeto quota.exhausted: cada payload (tenant_id)
// dispara el corte. Devuelve el error del broker sin reintentos aqui: el
// supervisor del agente decide.
func (g *Guard) Start(events EventSource) error {
	if events == nil {
		return nil
	}
	return events.Subscribe(SubjectQuotaExhausted, func(payload []byte) {
		g.CloseTenant(string(payload))
	})
}

// CheckTenant consulta el estado con cache TTL; si llega "exhausted"
// corta ya. Es la via periodica (heartbeat) que cubre eventos perdidos.
func (g *Guard) CheckTenant(ctx context.Context, tenantID string) error {
	if g.reader == nil {
		return nil
	}
	g.mu.Lock()
	e, ok := g.cached[tenantID]
	g.mu.Unlock()
	if ok && g.now().Sub(e.at) < g.cacheTTL && e.state == QuotaStateExhausted {
		g.CloseTenant(tenantID)
		return nil
	}
	if ok && g.now().Sub(e.at) < g.cacheTTL {
		return nil
	}
	state, err := g.reader.QuotaState(ctx, tenantID)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.cached[tenantID] = cacheEntry{state: state, at: g.now()}
	g.mu.Unlock()
	if state == QuotaStateExhausted {
		g.CloseTenant(tenantID)
	}
	return nil
}

// CloseTenant lanza el GOAWAY con closed_reason='quota' a todas las
// sesiones vivas del tenant y libera streams y contadores al instante.
// Devuelve el numero de sesiones cortadas.
func (g *Guard) CloseTenant(tenantID string) int {
	sessions := g.reg.TenantSessions(tenantID)
	for _, s := range sessions {
		s.CloseWith(ClosedQuota) // cierra stream + notifica sink
		g.reg.Unregister(s.SessionID)
	}
	return len(sessions)
}
