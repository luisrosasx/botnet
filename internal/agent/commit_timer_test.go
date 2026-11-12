package agent

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time      { return f.t }
func (f *fakeClock) adv(d time.Duration) { f.t = f.t.Add(d) }

func newCommitFixture() (*CommitTimer, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	return NewCommitTimer(5*time.Minute, clk.now), clk
}

func TestCommitTimerDisparaTrasLaVentanaSana(t *testing.T) {
	c, clk := newCommitFixture()
	if c.Beat(true, 3) {
		t.Fatal("el primer latido no puede commitear")
	}
	clk.adv(4 * time.Minute)
	if c.Beat(true, 3) {
		t.Fatal("a 4 min todavía no")
	}
	clk.adv(90 * time.Second) // 5m30s de racha
	if !c.Beat(true, 3) {
		t.Fatal("pasada la ventana debe commitear")
	}
	// Reintento inmediato: la racha se reinició.
	if c.Beat(true, 3) {
		t.Fatal("no debe commitear dos veces seguidas")
	}
}

func TestCommitTimerSeRompeConFalloOSinStreams(t *testing.T) {
	c, clk := newCommitFixture()
	c.Beat(true, 2)
	clk.adv(4 * time.Minute)

	if c.Beat(false, 2) { // fallo de heartbeat
		t.Fatal("un fallo no commitea")
	}
	if c.Streak() != 0 {
		t.Fatalf("la racha debe romperse, sigue en %v", c.Streak())
	}
	clk.adv(4 * time.Minute)
	if c.Beat(true, 0) { // sano pero sin tráfico
		t.Fatal("sin streams no commitea")
	}
	clk.adv(4 * time.Minute)
	if c.Beat(true, 1) {
		t.Fatal("la racha se reinició: 4 min no bastan")
	}
	clk.adv(5*time.Minute + time.Second)
	if !c.Beat(true, 1) {
		t.Fatal("racha nueva completa: debe commitear")
	}
}

func TestCommitTimerUsaElContratoPorDefecto(t *testing.T) {
	c := NewCommitTimer(0, nil)
	if c.after != CommitAfter {
		t.Fatalf("ventana por defecto = %v, want %v", c.after, CommitAfter)
	}
}
