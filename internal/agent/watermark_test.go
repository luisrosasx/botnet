package agent

import (
	"testing"
	"time"
)

// fakeClock: reloj inyectable del watermark; las muestras entran por
// minuto simulado, sin time.Sleep en el test.
type wmClock struct{ t time.Time }

func (c *wmClock) Now() time.Time          { return c.t }
func (c *wmClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newWmFixture(t *testing.T) (*Watermark, *wmClock, uint64, uint64) {
	t.Helper()
	w := NewWatermark(MemLimitBytes)
	clk := &wmClock{t: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)}
	w.SetNow(clk.Now)
	pressure := w.PressureHeap() // umbral exacto del watermark (92 % del GOMEMLIMIT)
	low := uint64(MemLimitBytes * 60 / 100)
	if pressure <= low {
		t.Fatal("fixture: la franja de presion debe quedar por encima de la sana")
	}
	if got := MemLimitBytes * 92 / 100; int64(pressure)-int64(got) > 1 {
		t.Fatalf("franja de presion %d, want ~%d (92%% de GOMEMLIMIT)", pressure, got)
	}
	return w, clk, pressure, low
}

// AC1: presion cronica al 92 % de GOMEMLIMIT — la pausa arranca con la
// primera muestra presionada y las admisiones permanecen cerradas >= 10
// min desde esa pausa, aunque la presion ceda al minuto 2 (pausa corta
// prohibida: assert de duracion minima).
func TestT1306PauseHasTenMinuteFloor(t *testing.T) {
	w, clk, pressure, low := newWmFixture(t)

	w.Observe(pressure) // t0: primera pausa
	if w.Phase() != WmPaused || w.Admit() {
		t.Fatalf("tras la primera muestra presionada: fase=%s admit=%v, want paused/false", w.Phase(), w.Admit())
	}
	pauseStart := clk.Now()

	for m := 1; m <= 2; m++ { // presion hasta el minuto 2
		clk.Advance(time.Minute)
		w.Observe(pressure)
	}
	clk.Advance(time.Minute) // minuto 3: presion cesada
	w.Observe(low)
	if w.Phase() != WmCooldown {
		t.Fatalf("cesada la presion: fase=%s, want cooldown", w.Phase())
	}

	// Reanudacion: ni a los 7 min (cooldown) ni a los 9; si al cumplir el
	// suelo de 10 min de pausa.
	for m := 4; m <= 9; m++ {
		clk.Advance(time.Minute)
		w.Observe(low)
		if w.Admit() {
			t.Fatalf("admision reabierta al minuto %d: pausa de %v < %v", m, clk.Now().Sub(pauseStart), WatermarkPauseMin)
		}
	}
	clk.Advance(time.Minute) // minuto 10 = pausa >= 10 min
	w.Observe(low)
	if !w.Admit() {
		t.Fatalf("a los %v de pausa las admisiones siguen cerradas: resumeAt=%v", clk.Now().Sub(pauseStart), w.ResumeAt())
	}
	if d := clk.Now().Sub(pauseStart); d < WatermarkPauseMin {
		t.Fatalf("duracion de pausa %v < suelo de %v", d, WatermarkPauseMin)
	}
}

// AC2: presion larga (30 min) y cesada: la reanudacion espera Cooldown
// (5 min) EXTRA tras cesar la presion. En el log de 1 h con presiones
// intermitentes, 0 ciclos conecta/desconecta por debajo del suelo.
func TestT1306CooldownAfterLongPressure(t *testing.T) {
	w, clk, pressure, low := newWmFixture(t)

	w.Observe(pressure)
	for m := 1; m <= 30; m++ { // presion sostenida 30 min
		clk.Advance(time.Minute)
		w.Observe(pressure)
	}
	clk.Advance(time.Minute) // presion cesada en el minuto 31
	w.Observe(low)
	for m := 32; m <= 35; m++ { // cooldown en curso
		clk.Advance(time.Minute)
		w.Observe(low)
		if w.Admit() {
			t.Fatalf("admision abierta al minuto %d: faltaba cooldown (5 min tras cesar)", m)
		}
	}
	clk.Advance(time.Minute) // minuto 36 = cesar+5
	w.Observe(low)
	if !w.Admit() {
		t.Fatalf("pasado el cooldown las admisiones siguen cerradas (resumeAt=%v)", w.ResumeAt())
	}
}

// AC2 (oscilacion): una hora con picos de presion de 1 min cada 10 min
// no produce ni un ciclo conecta/desconecta de menos de 10 min: la
// histeria es la que lo garantiza, no el patron del fixture.
func TestT1306NoSubTenMinuteFlapInOneHour(t *testing.T) {
	w, clk, pressure, low := newWmFixture(t)

	pauses := 0
	var durations []time.Duration
	curPause := time.Time{}
	w.Observe(pressure) // minuto 0: primera pausa
	if w.Phase() == WmPaused {
		curPause = w.PauseStarted()
		pauses++
	}

	for m := 1; m <= 60; m++ {
		clk.Advance(time.Minute)
		heap := low
		if m%10 == 1 { // pico de presion de un minuto cada 10
			heap = pressure
		}
		w.Observe(heap)
		if w.Phase() == WmPaused && curPause.IsZero() {
			curPause = w.PauseStarted()
			pauses++
		}
		if w.Phase() == WmRunning && !curPause.IsZero() {
			durations = append(durations, clk.Now().Sub(curPause))
			curPause = time.Time{}
		}
	}
	if pauses == 0 {
		t.Fatal("el fixture no llego a pausar nunca: el watermark no detecta presion")
	}
	for i, d := range durations {
		if d < WatermarkPauseMin {
			t.Fatalf("ciclo conecta/desconecta %d de %v < %v en log de 1 h", i, d, WatermarkPauseMin)
		}
	}
	t.Logf("pausas=%d duraciones=%v — 0 ciclos por debajo del suelo", pauses, durations)
}

// AC3: durante la pausa el heartbeat sigue vivo (modo solo-control):
// 0 heartbeats perdidos en 1 h de presion simulada.
func TestT1306HeartbeatSurvivesPause(t *testing.T) {
	w, clk, pressure, _ := newWmFixture(t)

	w.Observe(pressure)
	beat := 0
	for m := 0; m <= 60; m++ { // 1 h con presion permanente
		if m > 0 {
			clk.Advance(time.Minute)
			w.Observe(pressure)
		}
		if w.Admit() {
			t.Fatalf("minuto %d: admision abierta con presion permanente", m)
		}
		if !w.HeartbeatAlive() {
			t.Fatalf("minuto %d: heartbeat muerto en fase %s", m, w.Phase())
		}
		beat++
	}
	if beat != 61 {
		t.Fatalf("heartbeats vivos = %d, want 61 (0 perdidos en 1 h)", beat)
	}
}

// La presion que reaparece durante el cooldown NO reinicia el suelo de
// 10 min: la pausa en curso continua (sin ciclos cortos disfrazados).
func TestT1306PressureBounceKeepsPauseFloor(t *testing.T) {
	w, clk, pressure, low := newWmFixture(t)

	w.Observe(pressure)
	pauseStart := clk.Now()
	for m := 1; m <= 4; m++ {
		clk.Advance(time.Minute)
		w.Observe(low) // presion cedida: cooldown abierto, resumeAt = t+10
	}
	if w.Phase() != WmCooldown {
		t.Fatalf("fase=%s, want cooldown", w.Phase())
	}
	clk.Advance(time.Minute)
	w.Observe(pressure) // rebote: la pausa continua con el suelo original
	if w.Phase() != WmPaused {
		t.Fatalf("fase=%s tras el rebote, want paused", w.Phase())
	}
	for clk.Now().Sub(pauseStart) < WatermarkPauseMin {
		clk.Advance(time.Minute)
		w.Observe(low)
		if w.Admit() {
			t.Fatal("el rebote de presion abrevio la pausa por debajo del suelo de 10 min")
		}
	}
	clk.Advance(time.Minute)
	w.Observe(low)
	if !w.Admit() {
		t.Fatalf("cumplido el suelo (resumeAt=%v) las admisiones debian reabrirse", w.ResumeAt())
	}
}
