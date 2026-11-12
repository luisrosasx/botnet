package agent

import (
	"time"
)

// Histeria del watermark por heap (T-1306, A1-15): con presion cronica el
// agua sube y baja, y sin memoria el destino ve una IP que conecta y
// desconecta en ciclos de minutos — combustible de las heuristicas
// rate-limit. La regla: la pausa de admisiones dura AL MENOS PauseMin
// (10 min) desde la primera pausa, y al cesar la presion las admisiones
// se reanudan tras Cooldown (5 min) — 0 ciclos conecta/desconecta
// < 10 min. El heartbeat no se pausa: modo solo-control (T-0307), el
// control es lo ultimo que muere.

// WatermarkPhase es la fase visible de la histeria.
type WatermarkPhase uint8

const (
	WmRunning  WatermarkPhase = iota // admisiones abiertas
	WmPaused                         // presion: admisiones cerradas
	WmCooldown                       // presion cesada: reanudacion retardada
)

func (p WatermarkPhase) String() string {
	switch p {
	case WmRunning:
		return "running"
	case WmPaused:
		return "paused"
	case WmCooldown:
		return "cooldown"
	}
	return "phase?"
}

const (
	// WatermarkHighFraction: presion = heap en uso sobre esta fraccion de
	// GOMEMLIMIT (criterio 1: fixture al 92 %).
	WatermarkHighFraction = 0.92
	// WatermarkPauseMin: pausa corta prohibida; tras la primera pausa las
	// admisiones siguen cerradas >= 10 min aunque la presion ceda antes.
	WatermarkPauseMin = 10 * time.Minute
	// WatermarkCooldown: cesada la presion, 5 min de espera antes de
	// reanadir admisiones (el heap debe permanecer estable, no flotar).
	WatermarkCooldown = 5 * time.Minute
)

// Watermark es la maquina de histeria. Observe se llama con la muestra
// periodica de heap (el mismo sample del supervisor); Admit gatea nuevas
// sesiones/stream entrantes; HeartbeatAlive responde true SIEMPRE: la
// pausa mata admisiones, no el plano de control.
type Watermark struct {
	Limit    uint64        // GOMEMLIMIT del agente (MemLimitBytes)
	High     float64       // fraccion de presion (WatermarkHighFraction)
	PauseMin time.Duration // suelo de la pausa (10 min)
	Cooldown time.Duration // espera tras cesar la presion (5 min)

	now func() time.Time // reloj inyectable para fixtures

	phase        WatermarkPhase
	pauseStarted time.Time
	pressureEnds time.Time // presion aun vigente si es el cero
	resumeAt     time.Time
	lastBeat     time.Time // ultima marca de latido (HeartbeatAlive)
	beatWindow   time.Duration
}

// NewWatermark fija la histeria canonica sobre el presupuesto del agente.
func NewWatermark(limit uint64) *Watermark {
	return &Watermark{
		Limit:      limit,
		High:       WatermarkHighFraction,
		PauseMin:   WatermarkPauseMin,
		Cooldown:   WatermarkCooldown,
		now:        time.Now,
		phase:      WmRunning,
		beatWindow: HeartbeatAliveWindow,
	}
}

// HeartbeatAliveWindow es el margen del que se dispone para ver un latido
// fresco. Por encima de 2,5 intervalos de la cadencia unica (30 s) el
// control se considera muerto aunque la fase siga en running.
const HeartbeatAliveWindow = 75 * time.Second

// MarkHeartbeat sella la marca de control vivo. La llama el bucle que
// recibe o emite latidos, no el watermark por su cuenta.
func (w *Watermark) MarkHeartbeat() {
	w.lastBeat = w.now()
}

// pressured dice si la muestra cae en la franja de presion.
func (w *Watermark) pressured(heap uint64) bool {
	return heap >= uint64(float64(w.Limit)*w.High)
}

// Observe aplica una muestra de heap en el reloj actual y avanza la
// histeria. Reaparicion de presion durante el cooldown reabre la pausa
// SIN reiniciar el suelo de 10 min (la pausa en curso continua).
func (w *Watermark) Observe(heap uint64) {
	now := w.now()
	on := w.pressured(heap)
	switch w.phase {
	case WmRunning:
		if on {
			w.phase = WmPaused
			w.pauseStarted = now
			w.pressureEnds = time.Time{}
		}
	case WmPaused:
		if !on {
			w.phase = WmCooldown
			w.pressureEnds = now
			w.resumeAt = w.resumeDeadline(now)
		}
	case WmCooldown:
		if on {
			w.phase = WmPaused // la presion volvio: pausa continua
			w.pressureEnds = time.Time{}
		} else if !w.resumeAt.IsZero() && !now.Before(w.resumeAt) {
			w.phase = WmRunning
			w.resumeAt = time.Time{}
		}
	}
}

// resumeDeadline: reanadir cuando se hayan cumplido AMBOS suelos:
// pausa >= PauseMin desde el inicio de pausa y presion cesada hace
// >= Cooldown.
func (w *Watermark) resumeDeadline(pressureEnded time.Time) time.Time {
	byPause := w.pauseStarted.Add(w.PauseMin)
	byCool := pressureEnded.Add(w.Cooldown)
	if byPause.After(byCool) {
		return byPause
	}
	return byCool
}

// Admit: el plano de datos entra solo en fase running.
func (w *Watermark) Admit() bool { return w.phase == WmRunning }

// HeartbeatAlive: el heartbeat sobrevive a la pausa (modo solo-control),
// pero eso no significa responder true siempre: sin latido fresco dentro
// de la ventana, el control esta muerto y asi se reporta (H3).
func (w *Watermark) HeartbeatAlive() bool {
	if w.lastBeat.IsZero() {
		return false
	}
	return w.now().Sub(w.lastBeat) <= w.beatWindow
}

// Phase y PauseStarted son para logs y asserts del test.
func (w *Watermark) Phase() WatermarkPhase     { return w.phase }
func (w *Watermark) PauseStarted() time.Time   { return w.pauseStarted }
func (w *Watermark) PressureHeap() uint64      { return uint64(float64(w.Limit) * w.High) }
func (w *Watermark) ResumeAt() time.Time       { return w.resumeAt }
func (w *Watermark) SetNow(f func() time.Time) { w.now = f }
