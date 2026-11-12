// Package logger es el suelo de observabilidad del backend y del gateway
// (BLUEPRINT §12): metricas Prometheus en texto plano stdlib-puro (sin
// el cliente oficial: el presupuesto de binario no paga dependencias),
// logs JSON de una linea con spool a disco y el muestreo de RSS para
// botnet_rss_kib. El nodo no scrapea por HTTP: esto sirve el lado
// servidor (api, gw, rdv); el beacon del agente viaja por el tunel.
package logger

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Nombres fijos de las metricas comprometidas por T-1201 AC1: deben
// estar presentes en /metrics de botnet-api, botnet-gw y botnet-rdv.
const (
	MetricStreamsActive = "botnet_streams_active"
	MetricRSSKiB        = "botnet_rss_kib"
	MetricAllocMS       = "botnet_alloc_ms"
)

// Gauge float64 con semantica Prometheus (ultimo valor observado).
type Gauge struct {
	name string
	help string
	val  atomic.Uint64 // bits de float64
}

func newGauge(name, help string) *Gauge { return &Gauge{name: name, help: help} }

// Set fija el valor actual del gauge.
func (g *Gauge) Set(v float64) { g.val.Store(float64bits(v)) }

// Value lee el valor actual del gauge.
func (g *Gauge) Value() float64 { return float64frombits(g.val.Load()) }

// Counter monotonico; reset solo por reinicio del proceso.
type Counter struct {
	name string
	help string
	val  atomic.Uint64
}

func newCounter(name, help string) *Counter { return &Counter{name: name, help: help} }

// Add suma delta al contador.
func (c *Counter) Add(delta uint64) { c.val.Add(delta) }

// Inc suma uno.
func (c *Counter) Inc() { c.val.Add(1) }

// Value lee el contador.
func (c *Counter) Value() uint64 { return c.val.Load() }

// Metrics es el registro por componente: las tres metricas del
// contrato T-1201 nacen siempre y cada binario puede anadir las suyas.
type Metrics struct {
	component string
	mu        sync.Mutex
	gauges    map[string]*Gauge
	counters  map[string]*Counter
	rss       *Gauge // auto-muestreada en cada Write
}

// NewMetrics crea el registro del componente ("api", "gw", "rdv") con
// las metricas del contrato presentes desde el arranque.
func NewMetrics(component string) *Metrics {
	m := &Metrics{
		component: component,
		gauges:    make(map[string]*Gauge),
		counters:  make(map[string]*Counter),
	}
	m.gauges[MetricStreamsActive] = newGauge(MetricStreamsActive, "Active H2 proxy streams (gauge).")
	m.gauges[MetricRSSKiB] = newGauge(MetricRSSKiB, "Resident set size in KiB (sampled at scrape time).")
	m.gauges[MetricAllocMS] = newGauge(MetricAllocMS, "Last measured session-alloc latency in milliseconds.")
	m.rss = m.gauges[MetricRSSKiB]
	return m
}

// GaugeNamed devuelve (creando si no existe) el gauge name.
func (m *Metrics) GaugeNamed(name, help string) *Gauge {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := m.gauges[name]
	if g == nil {
		g = newGauge(name, help)
		m.gauges[name] = g
	}
	return g
}

// CounterNamed devuelve (creando si no existe) el counter name.
func (m *Metrics) CounterNamed(name, help string) *Counter {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.counters[name]
	if c == nil {
		c = newCounter(name, help)
		m.counters[name] = c
	}
	return c
}

// StreamsActive expone el gauge de streams vivos del proxy.
func (m *Metrics) StreamsActive() *Gauge { return m.gauges[MetricStreamsActive] }

// AllocMS expone el gauge de latencia de alloc de sesion; TrackAlloc lo
// actualiza alrededor del trabajo de apertura.
func (m *Metrics) AllocMS() *Gauge { return m.gauges[MetricAllocMS] }

// RSSKiB expone el gauge de RSS; se re-muestrea solo en Write (el
// scrape no puede pagar un readmemstats por request fuera de aqui).
func (m *Metrics) RSSKiB() *Gauge { return m.rss }

// TrackAlloc mide fn en milisegundos y deja el ultimo valor en el gauge
// botnet_alloc_ms: la cadena alloc→open→close→flush queda medible.
func (m *Metrics) TrackAlloc(fn func()) {
	start := time.Now()
	fn()
	m.AllocMS().Set(float64(time.Since(start).Microseconds()) / 1000.0)
}

// SampleRSSKiB estima el RSS del proceso en KiB sin CGO: en Linux se lee
// VmRSS de procfs (exacto); en el resto se suma el footprint que Go
// declara residente (heap+stacks+sistemas). El panel lo documenta como
// estimacion en hosts sin procfs (asercion T-1202 con doble medicion).
func SampleRSSKiB() uint32 {
	if v, ok := vmRSSKiB(); ok {
		return v
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	total := ms.HeapInuse + ms.StackSys + ms.MSpanSys + ms.MCacheSys + ms.GCSys + ms.OtherSys
	return uint32(total / 1024)
}

// vmRSSKiB lee VmRSS de /proc/self/status (Linux); ok=false fuera de
// procfs (Windows/macOS caen a la estimacion runtime).
func vmRSSKiB() (uint32, bool) {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			n, err := strconv.ParseUint(strings.Fields(rest)[0], 10, 64)
			return uint32(n), err == nil
		}
	}
	return 0, false
}

// Write emite el texto Prometheus (version 0.0.4): metricas ordenadas
// por nombre para diffs estables en el scrape y en los asserts.
func (m *Metrics) Write(w io.Writer) error {
	m.rss.Set(float64(SampleRSSKiB()))
	m.mu.Lock()
	names := make([]string, 0, len(m.gauges)+len(m.counters))
	for n := range m.gauges {
		names = append(names, n)
	}
	for n := range m.counters {
		names = append(names, n)
	}
	m.mu.Unlock()
	sort.Strings(names)
	for _, n := range names {
		m.mu.Lock()
		g, cg := m.gauges[n], m.counters[n]
		m.mu.Unlock()
		if cg != nil {
			if _, err := fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", cg.name, cg.name, cg.Value()); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "# TYPE %s gauge\n%s %g\n", g.name, g.name, g.Value()); err != nil {
			return err
		}
	}
	return nil
}

// Handler sirve GET /metrics (text/plain; version=0.0.4), el unico
// endpoint de scraping del plano de control (descartado en el nodo).
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = m.Write(w)
	})
}

func float64bits(f float64) uint64 { return math.Float64bits(f) }

func float64frombits(b uint64) float64 { return math.Float64frombits(b) }
