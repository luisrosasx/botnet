package watchdog

import (
	"testing"
	"time"
)

// AC2: 4 auto-kills en 1 h -> el intervalo de revive escala el presupuesto
// de 3/h a 1/h, los intervalos son monotonos no decrecientes y 0 rafagas
// (ningun revive cae a menos de FastRevival del anterior).
func TestRestartBudgetEscalates3to1(t *testing.T) {
	b := NewRestartBudget()
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	kills := []time.Time{t0, t0.Add(5 * time.Minute), t0.Add(10 * time.Minute), t0.Add(15 * time.Minute)}

	var delays []time.Duration
	var prev time.Duration
	for i, k := range kills {
		d := b.RecordKill(k)
		if i > 0 && d < prev {
			t.Fatalf("intervalo de revive no monotono: %v despues de %v", d, prev)
		}
		if d < b.FastRevival {
			t.Fatalf("rafaga: revive en %v (< FastRevival %v)", d, b.FastRevival)
		}
		delays = append(delays, d)
		prev = d
	}
	if !b.Escalated() || b.Allowed() != 1 {
		t.Fatalf("presupuesto tras 4 kills en 1 h: allowed=%d escalated=%v, want 1/true", b.Allowed(), b.Escalated())
	}
	// El cuarto revive debe caer mas alla de la ventana: tasa efectiva 1/h.
	fourthRevival := kills[3].Add(delays[3])
	if !fourthRevival.After(kills[0].Add(time.Hour)) {
		t.Fatalf("revive %v dentro de la ventana del primer kill %v: la tasa sigue >1/h", fourthRevival, kills[0])
	}
}

// Con presion esporadica (≤3 kills/h) el revive sigue siendo rapido y el
// presupuesto no escala: 0 handshakes de mas no es el objetivo, si no hay
// ciclo.
func TestRestartBudgetWithinBudgetStaysFast(t *testing.T) {
	b := NewRestartBudget()
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		d := b.RecordKill(t0.Add(time.Duration(i) * 20 * time.Minute))
		if d != b.FastRevival {
			t.Fatalf("kill %d: revive en %v, want %v", i, d, b.FastRevival)
		}
	}
	if b.Escalated() {
		t.Fatal("3 kills dentro de presupuesto no deben escalar a 1/h")
	}
}

// Tras escalar, el presupuesto 1/h es permanente (monotonia): aunque pasen
// horas sanas, Rate no vuelve a 3.
func TestRestartBudgetEscalationIsMonotone(t *testing.T) {
	b := NewRestartBudget()
	t0 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	for _, off := range []time.Duration{0, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute} {
		b.RecordKill(t0.Add(off))
	}
	if b.Allowed() != 1 {
		t.Fatalf("allowed=%d, want 1 tras la escalada", b.Allowed())
	}
	// Un kill tardio sigue bajo 1/h: revive en FastRevival pero Rate=1.
	d := b.RecordKill(t0.Add(3 * time.Hour))
	if d != b.FastRevival {
		t.Fatalf("kill aislado tras escalar: %v, want %v", d, b.FastRevival)
	}
	if b.Allowed() != 1 {
		t.Fatalf("el presupuesto volvio a %d/h: la escalada debe ser monotona", b.Allowed())
	}
}
