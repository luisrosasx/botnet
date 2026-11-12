package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"
)

// ErrControlOnly indica que el supervisor ya no acepta streams: tras
// tres panicos en la ventana el agente queda en modo solo-control y el
// heartbeat es lo ultimo que muere (BLUEPRINT §4, A3-05).
var ErrControlOnly = errors.New("agent: modo solo-control (3 panicos en 1 h)")

// Supervisor es el supervisor in-process del agente: recover() por
// stream, ventana deslizante de panicos y auto-matanza por heap. El
// watchdog de procesos (EP-05) es el que revive; aqui solo se muere
// con exit != 0.
type Supervisor struct {
	Limit    int           // panicos que activan solo-control (3)
	Window   time.Duration // ventana de la cuenta (1 h)
	MemLimit uint64        // presupuesto de heap; por encima, exit != 0

	now  func() time.Time
	kill func(code int)
	log  *slog.Logger

	mu          sync.Mutex
	panics      []time.Time
	controlOnly bool
}

func NewSupervisor(log *slog.Logger) *Supervisor {
	return &Supervisor{
		Limit:    3,
		Window:   time.Hour,
		MemLimit: MemLimitBytes,
		now:      time.Now,
		kill:     func(code int) { os.Exit(code) },
		log:      log,
	}
}

// Run ejecuta un stream con recover. Despues de Limit panicos dentro de
// Window devuelve ErrControlOnly sin ejecutar fn: los streams nuevos ya
// no entran, el control sigue.
func (s *Supervisor) Run(name string, fn func()) error {
	if s.ControlOnly() {
		return ErrControlOnly
	}
	return s.run(name, fn)
}

// RunCritical es el carril de control (heartbeat): nunca se porta por
// solo-control — "el heartbeat es lo ultimo que muere" (§4).
func (s *Supervisor) RunCritical(name string, fn func()) error {
	return s.run(name, fn)
}

func (s *Supervisor) run(name string, fn func()) (rerr error) {
	defer func() {
		if r := recover(); r != nil {
			s.recordPanic(name, r)
			rerr = fmt.Errorf("agent: stream %q en panic recuperado: %v", name, r)
		}
	}()
	fn()
	return nil
}

func (s *Supervisor) recordPanic(name string, r any) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.panics = append(s.panics, now)
	if !s.controlOnly && s.countLocked(now) >= s.Limit {
		s.controlOnly = true
		if s.log != nil {
			s.log.Info("supervisor", "mode", "control_only", "stream", name)
		}
	}
}

func (s *Supervisor) countLocked(now time.Time) int {
	n := 0
	for _, t := range s.panics {
		if now.Sub(t) <= s.Window {
			n++
		}
	}
	return n
}

// PanicCount son los panicos dentro de la ventana (T-0307 AC1).
func (s *Supervisor) PanicCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.countLocked(s.now())
}

func (s *Supervisor) ControlOnly() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlOnly
}

// CurrentHeapInUse es el muestreo por defecto del WatchHeap.
func CurrentHeapInUse() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse + ms.StackInuse
}

// CheckHeap muestrea el heap en uso; por encima del presupuesto
// auto-matanza con exit != 0 y revival por watchdog (T-0307 AC2). El
// sample inyectable es el fixture de presion del test.
func (s *Supervisor) CheckHeap(sample func() uint64) bool {
	if sample == nil {
		sample = CurrentHeapInUse
	}
	heap := sample()
	if heap <= s.MemLimit {
		return false
	}
	if s.log != nil {
		s.log.Error("heap sobre presupuesto: auto-kill", "heap_kib", heap/1024, "exit", 1)
	}
	s.kill(1)
	return true
}

// WatchHeap hace CheckHeap cada interval hasta que muera el ctx; si el
// check mata el proceso, la goroutine ni vuelve.
func (s *Supervisor) WatchHeap(ctx context.Context, interval time.Duration, sample func() uint64) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.CheckHeap(sample) {
				return
			}
		}
	}
}
