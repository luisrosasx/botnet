package tunnel

import (
	"io"
	"net"
	"testing"
	"time"
)

// pipeDial simula el dial TLS: un extremo al mux y el otro drenado —
// el "ss | grep -c ESTAB" del test es el conteo de dials.
func pipeDial(t *testing.T, dialed *int) func() (net.Conn, error) {
	return func() (net.Conn, error) {
		c, s := net.Pipe()
		*dialed++
		go func() {
			_, _ = io.Copy(io.Discard, s) // el peer lee hasta el cierre
		}()
		return c, nil
	}
}

// T-0405 AC1: con perdida (tc netem loss 2 % simulado por el estimador)
// y streams encolando: conexiones <= 4 y repartimiento round-robin —
// cada carril se lleva >= 25 % de 100 streams.
func TestMuxExpandUnderLoss(t *testing.T) {
	dialed := 0
	m := NewMux(DefaultMuxConfig(), pipeDial(t, &dialed))
	defer m.Close()
	m.NoteLoss(200, time.Now()) // 2 % en puntos basicos

	const streams = 100
	counts := make([]int, 4)
	for i := 0; i < streams; i++ {
		idx, err := m.Placement()
		if err != nil {
			t.Fatal(err)
		}
		if idx > 3 {
			t.Fatalf("indice de carril %d fuera de 4", idx)
		}
		counts[idx]++
	}
	conns := m.Conns()
	if conns > 4 {
		t.Fatalf("conexiones = %d, want <= 4", conns)
	}
	if conns != 4 {
		t.Fatalf("con perdida deberia expandir a 4, got %d (loads %v)", conns, m.Loads())
	}
	for i, c := range m.Loads() {
		if c < streams/4 { // 25 % por carril
			t.Fatalf("carril %d con %d streams, want >= %d (round-robin)", i, c, streams/4)
		}
	}
}

// T-0405 AC2: cesada la perdida durante 5 min, las conexiones sobrantes
// se cierran con GOAWAY y el conteo vuelve a 1 (0 zombis).
func TestMuxShrinkAfterQuiet(t *testing.T) {
	dialed := 0
	m := NewMux(DefaultMuxConfig(), pipeDial(t, &dialed))
	defer m.Close()
	now := time.Now()
	m.NoteLoss(200, now)
	for i := 0; i < 100; i++ {
		if _, err := m.Placement(); err != nil {
			t.Fatal(err)
		}
	}
	if m.Conns() != 4 {
		t.Fatalf("previo al shrink: %d carriles, want 4", m.Conns())
	}
	// perdida 0 durante 5 min:
	m.NoteLoss(0, now.Add(time.Minute))
	if m.CanShrink(now.Add(4 * time.Minute)) {
		t.Fatal("contraccion antes de los 5 min de racha limpia")
	}
	if !m.CanShrink(now.Add(6 * time.Minute)) {
		t.Fatal("la contraccion deberia estar habilitada tras 5 min limpios")
	}
	dead := m.Shrink(now.Add(6 * time.Minute))
	if dead != 3 {
		t.Fatalf("carriles cerrados = %d, want 3 (GOAWAY)", dead)
	}
	if m.Conns() != 1 {
		t.Fatalf("tras shrink: %d conexiones, want 1", m.Conns())
	}
	// los streams del carril 0 siguen vivos: placement los pone alli.
	if idx, err := m.Placement(); err != nil || idx != 0 {
		t.Fatalf("placement tras shrink: idx %d err %v", idx, err)
	}
}

// T-0405 AC3: sin perdida la regla no abre carriles por defecto — 50
// streams (los "30 min") siguen sobre UNA conexion y el presupuesto del
// beacon no crece.
func TestMuxNoExpansionWithoutLoss(t *testing.T) {
	dialed := 0
	m := NewMux(DefaultMuxConfig(), pipeDial(t, &dialed))
	defer m.Close()
	m.NoteLoss(0, time.Now())
	for i := 0; i < 50; i++ {
		if idx, err := m.Placement(); err != nil || idx != 0 {
			t.Fatalf("placement[%d] = %d (%v), want 0", i, idx, err)
		}
	}
	if m.Conns() != 1 {
		t.Fatalf("sin perdida: %d conexiones, want 1", m.Conns())
	}
	if loads := m.Loads(); loads[0] != 50 {
		t.Fatalf("carga del carril unico = %d, want 50", loads[0])
	}
	if dialed != 1 {
		t.Fatalf("dials = %d, want 1 (la regla no abre carriles por defecto)", dialed)
	}
}

// Release baja la carga y habilita laExpansion tras liberar el carril.
func TestMuxReleaseReopensHeadroom(t *testing.T) {
	dialed := 0
	cfg := DefaultMuxConfig()
	cfg.QueueDepth = 2
	m := NewMux(cfg, pipeDial(t, &dialed))
	defer m.Close()
	m.NoteLoss(100, time.Now())
	for i := 0; i < 2; i++ {
		m.Placement() // llenan el carril 0
	}
	m.Placement() // abre carril 1
	if m.Conns() != 2 {
		t.Fatalf("conns %d, want 2", m.Conns())
	}
	m.Release(0)
	if idx, _ := m.Placement(); idx != 0 {
		t.Fatalf("tras Release, placement = %d, want 0 (menos carga)", idx)
	}
}
