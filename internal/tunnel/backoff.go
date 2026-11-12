package tunnel

import (
	"math/rand"
	"time"
)

// Backoff exponencial con tope de 5 min y jitter ±20 % (BLUEPRINT §5
// "Backoff y dispersion", T-0406): la reconexion masiva tras una tormenta
// deja de ser avalancha y el coste nocturno por nodo queda presupuestado
// (≤ 20 handshakes con gateway caido durante 1 h).
type Backoff struct {
	Base   time.Duration // primer retraso (1 s por defecto)
	Cap    time.Duration // tope: 300 s = 5 min (nunca se supera)
	Factor float64       // multiplicador de la base (2)
	Jitter float64       // amplitud del jitter simetrico (0.20 = ±20 %)

	rnd  *rand.Rand
	base time.Duration
}

// NewBackoff con la politica canonica: 1s, 2s, 4s... tope 300s, ±20 %.
func NewBackoff() *Backoff {
	return &Backoff{
		Base:   time.Second,
		Cap:    5 * time.Minute,
		Factor: 2,
		Jitter: 0.20,
		rnd:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// WithRand fija la fuente: los tests quedan deterministas.
func (b *Backoff) WithRand(r *rand.Rand) *Backoff {
	b.rnd = r
	return b
}

// Base actual de la secuencia (0 = aun sin usar). Monotona hasta Cap:
// la asercion de T-0406 AC1 vigila que el jitter no rompa la base.
func (b *Backoff) Cur() time.Duration { return b.base }

// Next avanza la secuencia y devuelve el retraso jitterizado. La base
// sube xFactor hasta Cap; el retardo real queda en [0.8, 1.2] x base
// cuando Jitter=0.20 — la cota es lo que aserta el test.
func (b *Backoff) Next() time.Duration {
	if b.base == 0 {
		b.base = b.Base
	} else {
		next := time.Duration(float64(b.base) * b.Factor)
		if next > b.Cap || next <= 0 {
			next = b.Cap
		}
		b.base = next
	}
	return b.jitter(b.base)
}

// Reset tras handshake exitoso: la proxima tormenta vuelve a empezar en
// Base (evita el sesgo de nodos que nunca duermen).
func (b *Backoff) Reset() { b.base = 0 }

func (b *Backoff) jitter(d time.Duration) time.Duration {
	if b.Jitter <= 0 || b.rnd == nil {
		return d
	}
	f := 1 - b.Jitter + 2*b.Jitter*b.rnd.Float64()
	return time.Duration(float64(d) * f)
}

// Sequence devuelve las bases de las n primeras reaperturas (sin jitter):
// 1s, 2s, 4s... 300s, 300s... — el assert de cota y monotonicidad.
func (b *Backoff) Sequence(n int) []time.Duration {
	saved := b.base
	b.Reset()
	out := make([]time.Duration, n)
	for i := range out {
		if b.base == 0 {
			b.base = b.Base
		} else {
			next := time.Duration(float64(b.base) * b.Factor)
			if next > b.Cap {
				next = b.Cap
			}
			b.base = next
		}
		out[i] = b.base
	}
	b.base = saved
	return out
}

// AttemptsInHourSim simula el bucle de reconnection de un nodo con el
// gateway caido durante window: cuantos intentos caben (el criterio 3
// exige ≤ 20 en 1 h). El primer intento es inmediato.
func (b *Backoff) AttemptsInHourSim(window time.Duration) int {
	saved := b.base
	b.Reset()
	n, elapsed := 1, time.Duration(0)
	for elapsed < window {
		elapsed += b.Next()
		if elapsed > window {
			break
		}
		n++
	}
	b.base = saved
	return n
}

// StormDelay es el retraso de una tormenta (T-0406 AC2): full jitter
// uniforme en [0.8*Base, 1.2*Cap]. Cada nodo de la flota que despierta a
// la vez elige un punto distinto del rango: los intentos quedan dispersos
// en >= 300 s sin coordinacion central.
func (b *Backoff) StormDelay() time.Duration {
	lo := float64(b.Base) * (1 - b.Jitter)
	hi := float64(b.Cap) * (1 + b.Jitter)
	if b.rnd == nil {
		return time.Duration(hi)
	}
	return time.Duration(lo + (hi-lo)*b.rnd.Float64())
}
