package tunnel

import (
	"hash/fnv"
	"time"
)

// Cadencia unica por nodo (BLUEPRINT §5 "Cadencia unica con keepalive",
// T-0403): un solo temporizador emite un frame de aplicacion cada
// 40 s ± jitter, siempre < 45 s — el purge TCP del CGNAT (60-120 s)
// nunca encuentra la conexion inactiva. Cada 3.er frame porta el
// heartbeat completo; los demas son pings parciales (ratio 2:1).

// Cadence genera los intervalos con jitter determinista por seed: el
// nodo repite su serie (replays de log), dos nodos nunca laten al unisono
// y ningun par de intervalos consecutivos es identico (mata la firma de
// cadencia fija del DPI, injerto P3).
type Cadence struct {
	Base   time.Duration // 40 s nominales
	Max    time.Duration // techo duro: < 45 s (purge CGNAT)
	Jitter time.Duration // amplitud simetrica sobre Base
	Seed   string        // identidad del nodo (node_id o mac)

	n    uint64
	prev time.Duration
}

// NewCadence fija la politica del plano: base 40 s, techo 45 s (el
// criterio es estricto: todo intervalo es < 45 s), jitter ±4 s — el
// maximo serializable queda en 44 s, dentro del techo.
func NewCadence(seed string) *Cadence {
	return &Cadence{Base: 40 * time.Second, Max: 45 * time.Second, Jitter: 4 * time.Second, Seed: seed}
}

// spread es pseudo-aleatorio simetrico [-1,1] derivado de (seed, n):
// determinista, estable por nodo, distinto en cada tick.
func (c *Cadence) spread() float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(c.Seed))
	_, _ = h.Write([]byte{byte(c.n), byte(c.n >> 8), byte(c.n >> 16), byte(c.n >> 24)})
	j := uint64(c.Jitter)
	if j == 0 {
		return 0
	}
	n := h.Sum64() % (2*j + 1) // 0..2J
	return float64(n)/float64(j) - 1
}

// Next avanza el temporizador: intervalo jitterizado en
// [Base-Jitter, Base+Jitter], clampado a < Max y distinto del anterior.
// La media de la serie tiende a Base (jitter simetrico), criterio AC1.
func (c *Cadence) Next() time.Duration {
	c.n++
	off := int64(float64(c.Jitter) * c.spread())
	d := c.Base + time.Duration(off)
	if d >= c.Max {
		d = c.Max - time.Second
	}
	if d <= 0 {
		d = c.Base
	}
	if d == c.prev { // 0 intervalos identicos consecutivos
		d += time.Second
		if d >= c.Max {
			d -= 2 * time.Second
		}
	}
	c.prev = d
	return d
}

// Reset vuelve al inicio de la serie (nuevo proceso, mismo seed: la
// firma del nodo se conserva).
func (c *Cadence) Reset() { c.n, c.prev = 0, 0 }

// BeatKind: cada 3.er frame porta el heartbeat completo (control cada
// ~120 s); los otros dos son keepalive parcial (ping). Ratio
// parciales:heartbeats = 2:1 exacto por construccion (AC2).
func BeatKind(seq uint64) bool { return seq%3 == 0 }

// TicksPerDay: frames de aplicacion por dia natural con la cadencia
// nominal (86400/40 = 2160).
func TicksPerDay(base time.Duration) int {
	if base <= 0 {
		return 0
	}
	return int(24 * time.Hour / base)
}

// BeaconBytesPerDay simula el presupuesto del beacon (AC3): el contador
// tx del agente en 24 h — heartbeats completos (hbBytes, con su relleno
// <=55 B) + pings parciales (pingBytes). El contrato: <= 120 KB por
// nodo y dia incluyendo keepalive.
func BeaconBytesPerDay(base time.Duration, hbBytes, pingBytes int) int {
	ticks := TicksPerDay(base)
	hb := ticks / 3
	return hb*hbBytes + (ticks-hb)*pingBytes
}

// ── T-1303: regla anti-perfil-C2 (A1-07) ──────────────────────────────────

// MaxOwnUdpGap: el agente no abre sockets UDP propios — todo el control
// (heartbeat, cmds, bye) viaja sobre el mux TLS/H2 y el unico temporizador
// es la cadencia unica. El UDP propio fuera de DNS se tolera solo si sus
// huecos superan este techo: un keepalive UDP de 25-45 s a destino fijo
// (perfil bottunnel/QUIC clasico) es exactamente lo que esta regla mata.
const MaxOwnUdpGap = 300 * time.Second

// OwnUdpCadenceOK: serie de huecos entre paquetes UDP propios (no-DNS)
// vista por el capture. Sin UDP propio (serie vacia) o con todos los
// huecos > MaxOwnUdpGap, el nodo pasa la regla; cualquier hueco <= 300 s
// delata una segunda cadencia.
func OwnUdpCadenceOK(gaps []time.Duration) bool {
	for _, g := range gaps {
		if g <= MaxOwnUdpGap {
			return false
		}
	}
	return true
}

// UdpGaps convierte marcas de tiempo (segundos) de paquetes propios en
// huecos consecutivos: la serie que auditaria tcpdump en el enlace.
func UdpGaps(stamps []int64) []time.Duration {
	if len(stamps) < 2 {
		return nil
	}
	out := make([]time.Duration, 0, len(stamps)-1)
	for i := 1; i < len(stamps); i++ {
		out = append(out, time.Duration(stamps[i]-stamps[i-1])*time.Second)
	}
	return out
}
