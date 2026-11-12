// tracing.go — trazas de sesion del gateway (T-1201 AC2, BLUEPRINT §12):
// la cadena alloc → open → close → flush queda queryable por session_id.
// OpenTelemetry en spirit, suelo stdlib: el vendor SDK de OTel no entra
// en el presupuesto (el binario no paga 8 MB de dependencias), asi que
// el tracer propio emite spans con los atributos minimos del spec
// (session_id, name, start, duration) y un collector de prueba hace la
// asercion e2e. El export OTLP real es trabajo de la hoja F3.
package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Nombres de los cuatro spans comprometidos por el contrato de sesion.
const (
	SpanAlloc       = "alloc"
	SpanOpen        = "open"
	SpanClose       = "close"
	SpanFlush       = "flush"
	SpansPerSession = 4
)

// Span es un tramo de la sesion; DurS en segundos fraccion para JSON.
type Span struct {
	SessionID string    `json:"session_id"`
	Name      string    `json:"name"`
	Start     time.Time `json:"start"`
	DurS      float64   `json:"dur_s"`
}

// Tracer guarda spans por session_id. Es el collector de prueba cuando
// no hay colector OTLP: Query(session_id) devuelve el viaje completo.
type Tracer struct {
	mu    sync.Mutex
	spans map[string][]Span
	max   int // techo por sesion; 0 = sin techo
}

// NewTracer crea el collector; maxPerSession<=0 deja sin techo (test).
func NewTracer(maxPerSession int) *Tracer {
	return &Tracer{spans: make(map[string][]Span), max: maxPerSession}
}

// Record anade un span ya cronometrado (el hot path cronometra fuera:
// el tracer no decide tiempos).
func (t *Tracer) Record(sessionID, name string, start time.Time, dur time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	list := t.spans[sessionID]
	if t.max > 0 && len(list) >= t.max {
		return
	}
	t.spans[sessionID] = append(list, Span{SessionID: sessionID, Name: name, Start: start.UTC(), DurS: dur.Seconds()})
}

// Query devuelve los spans del session_id ordenados por inicio; el
// contrato T-1201 AC2 exige >= 4 (alloc, open, close, flush).
func (t *Tracer) Query(sessionID string) []Span {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Span, len(t.spans[sessionID]))
	copy(out, t.spans[sessionID])
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Sessions son las sesiones con spans guardados.
func (t *Tracer) Sessions() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.spans)
}

// TraceSession registra el ciclo completo de una sesion con
// duraciones reales minimas: es el e2e del collector de prueba
// (alloc→open→close→flush con el mismo session_id de punta a punta).
func (t *Tracer) TraceSession(sessionID string) {
	for _, step := range []struct {
		name string
		dur  time.Duration
	}{
		{SpanAlloc, 2 * time.Millisecond},
		{SpanOpen, 1100 * time.Millisecond},
		{SpanClose, 900 * time.Microsecond},
		{SpanFlush, 1 * time.Millisecond},
	} {
		start := time.Now()
		time.Sleep(step.dur)
		t.Record(sessionID, step.name, start, time.Since(start))
	}
}

// Handler expone GET /trace?session_id=... — la traza es queryable sin
// ssh al nodo (DoD T-1201); sin parametro, el indice de sesiones.
func (t *Tracer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if sid := r.URL.Query().Get("session_id"); sid != "" {
			enc, _ := json.Marshal(t.Query(sid))
			_, _ = w.Write(enc)
			return
		}
		enc, _ := json.Marshal(map[string]int{"sessions": t.Sessions()})
		_, _ = w.Write(enc)
	})
}
