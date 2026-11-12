package proxy

import (
	"sync"
	"time"
)

// Presupuesto tecnico del nodo (§7, A3-05): buffers fijos de 8 KiB y tope
// de 150 streams nominales. El tenant ruidoso satura sus streams antes
// que el heap del dueño — el nodo ejecuta su tope tecnico, no la cuota de
// negocio.

// StreamBufSize es el tamano fijo de toda corriente del plano de datos.
// Los cuatro escritores fijos del paquete lo usan literal (assert de
// grep en T-0603 AC3): las 4 corrientes son handshake (saludos→cliente),
// up (cliente→stream), down (stream→cliente) y drain (restos→descarte).
const StreamBufSize = 8192

// MaxNominalStreams es el tope tecnico de streams simultaneos del agente
// (perfil de clase C: 150). Es independiente de la cuota concurrente de
// negocio, que admision.go combina con el minimo.
const MaxNominalStreams = 150

// PanicWindow y PanicLimit: supervisor de streams (§4) — tras 3 panicos
// en una hora el proxy pasa a modo solo-control: no admite sesiones
// nuevas y el heartbeat (plano de control del agente) sigue vivo.
const (
	PanicWindow = time.Hour
	PanicLimit  = 3
)

// StreamTable cuenta los streams abiertos y aplica el tope tecnico.
type StreamTable struct {
	mu   sync.Mutex
	max  int
	open int
}

// NewStreamTable crea la tabla con el tope dado; <=0 toma
// MaxNominalStreams.
func NewStreamTable(max int) *StreamTable {
	if max <= 0 {
		max = MaxNominalStreams
	}
	return &StreamTable{max: max}
}

// Acquire reserva un slot; false si el tope tecnico esta alcanzado.
func (t *StreamTable) Acquire() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open >= t.max {
		return false
	}
	t.open++
	return true
}

// Release devuelve un slot.
func (t *StreamTable) Release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open > 0 {
		t.open--
	}
}

// Open devuelve los streams abiertos actuales.
func (t *StreamTable) Open() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.open
}

// SetMax reasigna el tope (entitlement max_streams via config_rev, sin
// reinicio). Valores <=0 se ignoran.
func (t *StreamTable) SetMax(max int) {
	if max <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.max = max
}

// PanicBudget es la ventana deslizante de panicos del supervisor
// in-process. RegisterPanic marca uno; al llegar al limite dentro de la
// ventana, ControlOnly pasa a true y el proxy deja de admitir sesiones
// mientras el heartbeat del agente sigue latiendo.
type PanicBudget struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	when   []time.Time
	only   bool
	clock  func() time.Time
}

// NewPanicBudget crea el presupuesto con los limites del blueprint.
func NewPanicBudget() *PanicBudget {
	return &PanicBudget{limit: PanicLimit, window: PanicWindow, clock: time.Now}
}

// RegisterPanic suma un panico y devuelve true si el modo solo-control
// acaba de activarse. Los panicos fuera de la ventana se purgan.
func (p *PanicBudget) RegisterPanic() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.clock().UTC()
	keep := p.when[:0]
	for _, t := range p.when {
		if now.Sub(t) <= p.window {
			keep = append(keep, t)
		}
	}
	p.when = append(keep, now)
	if !p.only && len(p.when) >= p.limit {
		p.only = true
		return true
	}
	return false
}

// ControlOnly informa si el proxy debe rechazar sesiones nuevas.
func (p *PanicBudget) ControlOnly() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.only
}
