package agent

import "sync"

// QuotaNotify es la escucha del agente contra el corte por cuota
// (T-1004, T-0605): el backend publica quota.exhausted (payload:
// tenant_id) y el gateway responde con GOAWAY por sesion. El agente
// tipa el cierre con closed_reason='quota' — causa separada de
// 'error' y 'client' a efectos contables — y no reabre sesiones del
// tenant agotado hasta que el backend levante el estado.
type QuotaNotify struct {
	mu        sync.Mutex
	exhausted map[string]bool // tenantID -> GB agotados
}

// NewQuotaNotify crea la escucha vacia.
func NewQuotaNotify() *QuotaNotify {
	return &QuotaNotify{exhausted: make(map[string]bool)}
}

// OnExhausted consume el payload de quota.exhausted (tenantID).
// Idempotente: reenvios del evento no cambian el estado.
func (q *QuotaNotify) OnExhausted(tenantID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.exhausted[tenantID] = true
}

// IsExhausted informa si el tenant esta agotado.
func (q *QuotaNotify) IsExhausted(tenantID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.exhausted[tenantID]
}

// Clear levanta el corte (pago del periodo o cambio de cuota).
func (q *QuotaNotify) Clear(tenantID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.exhausted, tenantID)
}

// ClosedReasonFor tipa el cierre de una sesion que recibe GOAWAY:
// si el tenant estaba cortado por cuota, 'quota' (la cuota del dueno
// es de volumen — A2-20 — y el cargo debe decir por que cerro); si
// no, 'goaway' (drenaje operativo).
func (q *QuotaNotify) ClosedReasonFor(tenantID string) string {
	if q.IsExhausted(tenantID) {
		return "quota"
	}
	return "goaway"
}
