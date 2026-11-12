package tunnel

import (
	"math/rand"
	"testing"
	"time"
)

// T-0406 AC1: secuencia 1s, 2s, 4s... tope 300 s; base monotona y el
// jitter dentro de ±20 % (asercion de cota y monotonicidad).
func TestBackoffSequence(t *testing.T) {
	b := NewBackoff()
	seq := b.Sequence(12)
	want := []time.Duration{1, 2, 4, 8, 16, 32, 64, 128, 256, 300, 300, 300}
	for i, w := range want {
		if seq[i] != w*time.Second {
			t.Fatalf("base[%d] = %v, want %v", i, seq[i], w*time.Second)
		}
	}
	// jitter: cada retardo real en [0.8, 1.2] x base.
	b = NewBackoff().WithRand(rand.New(rand.NewSource(7)))
	bBase := b.Base
	for i := 0; i < 40; i++ {
		d := b.Next()
		base := b.Cur()
		lo, hi := time.Duration(float64(base)*0.8), time.Duration(float64(base)*1.2)
		if d < lo || d > hi {
			t.Fatalf("jitter[%d] = %v fuera de [%v, %v]", i, d, lo, hi)
		}
		if base > b.Cap {
			t.Fatalf("base %v supera el tope %v", base, b.Cap)
		}
	}
	if b.Cur() != b.Cap {
		t.Fatalf("tras 40 ticks la base = %v, want tope %v", b.Cur(), b.Cap)
	}
	// Reset vuelve la secuencia a Base.
	b.Reset()
	if first := b.Next(); first > time.Duration(float64(bBase)*1.2) {
		t.Fatalf("tras Reset el primer retardo = %v, want ~%v", first, bBase)
	}
}

// T-0406 AC2: tormenta de 1.000 nodos reconectando a la vez — intentos
// dispersos en >= 300 s, histograma con >= 10 buckets no vacios y pico
// por bucket <= 5 % de la flota.
func TestBackoffStormDispersion(t *testing.T) {
	const nodes = 1000
	delays := make([]time.Duration, nodes)
	minD, maxD := time.Hour, time.Duration(0)
	// una fuente por nodo (cada router tiene su PRNG); la semilla global
	// deja el test determinista.
	for i := 0; i < nodes; i++ {
		b := NewBackoff().WithRand(rand.New(rand.NewSource(int64(42+i) * 7919)))
		d := b.StormDelay()
		delays[i] = d
		if d < minD {
			minD = d
		}
		if d > maxD {
			maxD = d
		}
	}
	if spread := maxD - minD; spread < 300*time.Second {
		t.Fatalf("dispersion de la tormenta = %v, want >= 300 s", spread)
	}
	const bucket = 10 * time.Second
	span := int(maxD/bucket) + 1
	hist := make([]int, span)
	for _, d := range delays {
		hist[int(d/bucket)]++
	}
	full, peak := 0, 0
	for _, n := range hist {
		if n > 0 {
			full++
		}
		if n > peak {
			peak = n
		}
	}
	if full < 10 {
		t.Fatalf("buckets no vacios = %d, want >= 10", full)
	}
	if peak > nodes*5/100 {
		t.Fatalf("pico por bucket = %d nodos (%.1f%%), want <= 5%%", peak, 100*float64(peak)/nodes)
	}
}

// T-0406 AC3: 0 reconnects en bucle cerrado — con gateway caido 1 h el
// nodo cabe en <= 20 handshakes (contador de ClientHello).
func TestBackoffHourBudget(t *testing.T) {
	b := NewBackoff().WithRand(rand.New(rand.NewSource(11)))
	n := b.AttemptsInHourSim(time.Hour)
	if n > 20 {
		t.Fatalf("intentos en 1 h = %d, want <= 20", n)
	}
	if n < 10 {
		t.Fatalf("intentos en 1 h = %d — el backoff no esta cubriendo la hora", n)
	}
}
