package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func panicStream() { panic("stream inyectado") }

// T-0307 AC1: panicos recuperados por recover(); tras 3 en la ventana
// el agente entra en modo solo-control (log mode=control_only) y el
// heartbeat —carril critico— sigue vivo.
func TestSupervisorControlOnlyAfterThreePanics(t *testing.T) {
	var buf bytes.Buffer
	s := NewSupervisor(slog.New(slog.NewJSONHandler(&buf, nil)))
	s.kill = func(int) { t.Error("auto-kill no esperado en este test") }

	for range 3 {
		if err := s.Run("stream", panicStream); err == nil {
			t.Fatal("el panic debe devolver error recuperado")
		}
	}
	if !s.ControlOnly() {
		t.Fatal("tras 3 panicos no entro en solo-control")
	}
	if !strings.Contains(buf.String(), `"mode":"control_only"`) {
		t.Fatalf("falta el log mode=control_only: %s", buf.String())
	}
	if err := s.Run("stream4", func() { t.Error("no debe ejecutarse") }); !errors.Is(err, ErrControlOnly) {
		t.Fatalf("Run en solo-control: %v, want ErrControlOnly", err)
	}
	ran := 0
	if err := s.RunCritical("heartbeat", func() { ran++ }); err != nil {
		t.Fatalf("RunCritical en solo-control: %v", err)
	}
	if ran != 1 {
		t.Fatal("el heartbeat debe seguir vivo en solo-control")
	}
	if s.PanicCount() != 3 {
		t.Fatalf("PanicCount = %d != 3", s.PanicCount())
	}
}

// La ventana es de una hora: los panicos viejos no cuentan para
// activar solo-control (reloj falso, sin esperar).
func TestSupervisorPanicWindow(t *testing.T) {
	base := time.Now()
	clock := base
	s := NewSupervisor(slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil)))
	s.now = func() time.Time { return clock }
	s.kill = func(int) {}

	for range 2 {
		_ = s.Run("stream", panicStream)
	}
	clock = clock.Add(90 * time.Minute) // los 2 panicos quedan fuera de la ventana
	_ = s.Run("stream", panicStream)    // tercer panic, pero primero dentro de ventana
	if s.ControlOnly() {
		t.Fatal("panicos fuera de ventana no deben contar")
	}
	_ = s.Run("stream", panicStream)
	_ = s.Run("stream", panicStream)
	if !s.ControlOnly() {
		t.Fatal("3 panicos dentro de la ventana debian activar solo-control")
	}
}

// T-0307 AC2 (doble de la presion de heap): por encima del presupuesto
// GOMEMLIMIT el agente se auto-mata con exit != 0 y el watchdog lo
// revive. El soak de 30 min con 150 streams en QEMU cubre el pico de
// 6 MB real.
func TestSupervisorHeapAutoKill(t *testing.T) {
	s := NewSupervisor(slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil)))
	s.MemLimit = 5 << 20
	exitCode := 0
	killed := false
	s.kill = func(code int) { killed = true; exitCode = code }

	if s.CheckHeap(func() uint64 { return 4 << 20 }) {
		t.Fatal("4 MiB bajo presupuesto no debe matar")
	}
	if killed {
		t.Fatal("kill prematuro")
	}
	if !s.CheckHeap(func() uint64 { return 6 << 20 }) {
		t.Fatal("6 MiB sobre presupuesto debe auto-matar")
	}
	if !killed || exitCode == 0 {
		t.Fatalf("auto-kill con exit = %d, want != 0", exitCode)
	}
}

// WatchHeap con fixture de presion: mata en cuanto el sample supera el
// limite y la goroutine se retira.
func TestSupervisorWatchHeapKillsOnPressure(t *testing.T) {
	s := NewSupervisor(slog.New(slog.NewJSONHandler(new(bytes.Buffer), nil)))
	s.MemLimit = 1024
	done := make(chan int, 1)
	s.kill = func(code int) { done <- code }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.WatchHeap(ctx, 5*time.Millisecond, func() uint64 { return 2048 })
	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("exit 0 en auto-kill")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchHeap no mato con presion constante")
	}
}
