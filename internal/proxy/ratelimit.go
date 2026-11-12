package proxy

import (
	"context"
	"io"
	"sync"
	"time"
)

// Token bucket del nodo (§7): 2-5 Mbps aplicados en el agente, cerca de la
// fuente — el dueño no nota el router lento (restriccion 6 del BRIEF).
// El tope lo gobierna el backend via bandwidth_cap_kbps (T-0306): cambiar
// el tope en runtime no exige reinicio.

// KbpsToBytes convierte bandwidth_cap_kbps del entitlement a bytes/s
// (misma formula que agent.EntitlementManager.BandwidthBytesPerSec).
func KbpsToBytes(kbps uint32) float64 { return float64(kbps) * 1024 }

// MbpsToBytes convierte Mbps de cable a bytes/s para fixtures (5 Mbps).
func MbpsToBytes(mbps float64) float64 { return mbps * 1_000_000 / 8 }

// Bucket es un token bucket clasico compartido por todas las corrientes
// del nodo. El tope de rafaga es ~1 s de trafico: una rafaga no puede
// duplicar el caudal sostenido mas alla de un segundo, y en ventanas de
// 10 s la media queda cerca del tope (T-0604 AC1).
type Bucket struct {
	mu     sync.Mutex
	rate   float64 // bytes/s
	burst  float64 // capacidad en bytes
	tokens float64
	last   time.Time

	// sleep es inyectable para dobles deterministas.
	sleep func(time.Duration)
}

// NewBucket crea el bucket con bytes/s de tope y rafaga de 1 s (minimo
// 16 KiB, dos buffers de stream).
func NewBucket(bytesPerSec float64) *Bucket {
	burst := bytesPerSec
	if burst < 16*1024 {
		burst = 16 * 1024
	}
	return &Bucket{
		rate:   bytesPerSec,
		burst:  burst,
		tokens: burst,
		last:   time.Now(),
		sleep:  time.Sleep,
	}
}

// SetRate aplica un nuevo tope en caliente (bandwidth_cap_kbps del
// heartbeat): la proxima corriente ya nace con la nueva tasa, sin
// reinicio (T-0604 AC3).
func (b *Bucket) SetRate(bytesPerSec float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rate = bytesPerSec
	b.burst = bytesPerSec
	if b.burst < 16*1024 {
		b.burst = 16 * 1024
	}
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
}

// Rate devuelve el tope vigente en bytes/s.
func (b *Bucket) Rate() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rate
}

// Take bloquea hasta disponer de n tokens. Respeta ctx; nunca entrega
// mas alla del tope en media sostenida.
func (b *Bucket) Take(ctx context.Context, n float64) error {
	if n <= 0 {
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		now := time.Now()
		b.refill(now)
		if b.tokens >= n {
			b.tokens -= n
			b.mu.Unlock()
			return nil
		}
		deficit := (n - b.tokens) / b.rate
		b.mu.Unlock()
		if deficit < 0 {
			deficit = 0
		}
		// sueno acotado para seguir vivo ante ctx cancelado.
		d := time.Duration(deficit * float64(time.Second))
		if d > 200*time.Millisecond {
			d = 200 * time.Millisecond
		}
		if d <= 0 {
			d = time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
}

// refill incorpora tokens segun el tiempo transcurrido. Llamado con mu
// tomado.
func (b *Bucket) refill(now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
}

// ShapedConn envuelve un stream del tunel aplicando el bucket en ambas
// direcciones: el ancho de banda del nodo es compartido por upstream y
// downstream, y el limite queda cerca de la fuente.
type ShapedConn struct {
	rc  io.ReadWriteCloser
	b   *Bucket
	ctx context.Context
}

// Shape crea la conexion moldeada por el bucket con el ctx dado.
func Shape(rc io.ReadWriteCloser, b *Bucket, ctx context.Context) *ShapedConn {
	return &ShapedConn{rc: rc, b: b, ctx: ctx}
}

func (s *ShapedConn) Read(p []byte) (int, error) {
	n, err := s.rc.Read(p)
	if n > 0 {
		if werr := s.b.Take(s.ctx, float64(n)); werr != nil {
			return n, werr
		}
	}
	return n, err
}

func (s *ShapedConn) Write(p []byte) (int, error) {
	if err := s.b.Take(s.ctx, float64(len(p))); err != nil {
		return 0, err
	}
	return s.rc.Write(p)
}

func (s *ShapedConn) Close() error { return s.rc.Close() }
