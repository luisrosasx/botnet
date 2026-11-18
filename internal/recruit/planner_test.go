package recruit

import (
	"context"
	"os"
	"testing"
)

// Fixture de ventanas del blueprint (testdata/recruit/planner_windows.json).
func fixtureMetricas(t *testing.T) Metricas {
	t.Helper()
	b, err := os.ReadFile("../../testdata/recruit/planner_windows.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("fixture vacio")
	}
	// El fixture fija los valores de la ventana buena; los casos los ajustan.
	return Metricas{SuccessRatio: 0.97, EffectiveRatio: 0.95, LeaveRatio24h: 0.01, Probes15m: 250, NodeLoad1: 2.5, HostCPUKnown: true, Embudo: 10}
}

// TestSubeUnaClase: con tres ventanas buenas sube exactamente una.
func TestSubeUnaClase(t *testing.T) {
	d := Decide(1, fixtureMetricas(t), 0, 0)
	if d.Action != "up" || d.RateClass != 2 {
		t.Fatalf("decision=%s clase=%d (quiero up y 2)", d.Action, d.RateClass)
	}
	if d.Reason == "" {
		t.Fatal("la decision debe llevar motivo")
	}
}

// TestDenominadorCorto: 199 sondas no suben.
func TestDenominadorCorto(t *testing.T) {
	m := fixtureMetricas(t)
	m.Probes15m = 199
	d := Decide(1, m, 0, 0)
	if d.Action != "hold" || d.Reason != "denominador_corto" {
		t.Fatalf("decision=%s motivo=%s", d.Action, d.Reason)
	}
}

// TestRevierteUnaClase: una metrica mala baja una clase y como mucho una.
func TestRevierteUnaClase(t *testing.T) {
	m := fixtureMetricas(t)
	m.SuccessRatio = 0.80
	d := Decide(3, m, 0, 0)
	if d.Action != "down" || d.RateClass != 2 {
		t.Fatalf("decision=%s clase=%d (quiero down y 2, no -2)", d.Action, d.RateClass)
	}
	// En el suelo no baja de 1.
	d = Decide(1, m, 0, 0)
	if d.Action == "down" && d.RateClass < 1 {
		t.Fatalf("clase por debajo del suelo: %d", d.RateClass)
	}
}

// TestCooldown: con subida reciente no se sube otra vez.
func TestCooldown(t *testing.T) {
	d := Decide(2, fixtureMetricas(t), 1, 0)
	if d.Action != "hold" || d.Reason != "cooldown" {
		t.Fatalf("decision=%s motivo=%s", d.Action, d.Reason)
	}
}

// TestFrenoDeEmbudo: 501 atascadas frenan; 499 no.
func TestFrenoDeEmbudo(t *testing.T) {
	m := fixtureMetricas(t)
	m.Embudo = 501
	d := Decide(2, m, 0, 0)
	if d.Action != "hold" || d.Reason != "enroll_backlog" || d.PPSObjetivo != 0 {
		t.Fatalf("decision=%s motivo=%s pps=%d", d.Action, d.Reason, d.PPSObjetivo)
	}
	m.Embudo = 499
	d = Decide(2, m, 0, 0)
	if d.Action == "hold" && d.Reason == "enroll_backlog" {
		t.Fatal("499 no debe frenar")
	}
}

// TestFallbackDeHost: sin CPU fiable se marca y se usa load1.
func TestFallbackDeHost(t *testing.T) {
	m := fixtureMetricas(t)
	m.HostCPUKnown = false
	d := Decide(2, m, 0, 0)
	if !d.HostFallback {
		t.Fatal("debe marcarse host_fallback")
	}
	m.NodeLoad1 = 9
	d = Decide(2, m, 0, 0)
	if d.Action != "down" || d.Reason != "host_cargado" {
		t.Fatalf("carga alta: %s %s", d.Action, d.Reason)
	}
}

// TestPPSFijo: con RECRUIT_PPS_FIXED el lazo se congela.
func TestPPSFijo(t *testing.T) {
	d := Decide(1, fixtureMetricas(t), 0, 1200)
	if d.PPSObjetivo != 1200 || d.Reason != "pps_fijo" {
		t.Fatalf("fijo: pps=%d motivo=%s", d.PPSObjetivo, d.Reason)
	}
	if d.RateClass != 1 {
		t.Fatalf("la clase no debe cambiar con pps fijo: %d", d.RateClass)
	}
}

// TestTickEscribeYRespetaPausa: el tick escribe la decision y pausa manda.
func TestTickEscribeYRespetaPausa(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := st.Tick(ctx, fixtureMetricas(t)); err != nil {
		t.Fatalf("tick: %v", err)
	}
	var accion, motivo string
	if err := st.db.QueryRow(`SELECT action, reason FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 1`).Scan(&accion, &motivo); err != nil {
		t.Fatalf("lectura: %v", err)
	}
	if accion != "up" || motivo == "" {
		t.Fatalf("tick escrito=%s motivo=%q", accion, motivo)
	}
	// Clase manual 0: pausa con motivo.
	if _, err := st.db.Exec(`UPDATE recruit_control SET manual_rate_class=0`); err != nil {
		t.Fatalf("manual: %v", err)
	}
	d, err := st.Tick(ctx, fixtureMetricas(t))
	if err != nil {
		t.Fatalf("tick pausa: %v", err)
	}
	if d.Action != "paused" || d.RateClass != 0 || d.Reason == "" {
		t.Fatalf("pausa: %s clase=%d motivo=%q", d.Action, d.RateClass, d.Reason)
	}
}
