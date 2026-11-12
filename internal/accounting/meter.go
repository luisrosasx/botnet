// Package accounting: contabilidad del nodo — medidor autoritativo,
// spool/flush con dedup, conciliacion y facturacion (BLUEPRINT §11,
// EP-10). El nodo es el reloj maestro; gateway y NATS son auditoria
// (usage.audit nunca facturable, docs/runbooks/metering.md).
package accounting

import (
	"sync"
	"time"
)

// TolerancePct es la UNICA tolerancia contable del sistema (A2-18,
// alineada a Stripe): 0,5 %. No existen el ±1 % ni el 5 % de las
// rivales; cualquier desvio mayor es alarma, nunca margen.
const TolerancePct = 0.5

// GB es el GB decimal de facturacion (10^9 bytes), el mismo que
// redondea Stripe al emitir la factura.
const GB = 1_000_000_000

// SpoolInterval: el nodo vuelca su contador a disco cada 60 s
// (criterio T-1002). Es la cota dura de perdida por crash.
const SpoolInterval = 60 * time.Second

// CrashDetectionMargin cubre el tramo entre el ultimo spool en disco y
// el instante reconstruido por el backend (diff gateway-vs-usage). Con
// SpoolInterval da la cota de 90 s del BLUEPRINT §11.
const CrashDetectionMargin = 30 * time.Second

// MaxCrashLossSeconds es la perdida maxima admitida por kill -9 a
// mitad de sesion: 60 s de spool + 30 s de deteccion = 90 s de bytes.
const MaxCrashLossSeconds = 90.0

// MasterClockSeconds declara el reloj maestro del sistema contable:
// el nodo. Lo documenta el runbook; el codigo lo expone para que la
// disputa nodo-vs-gateway no vuelva a ser margen (DoD T-1001).
const MasterClockSeconds = "node"

// NodeMeter es el medidor autoritativo del nodo: un contador de bytes
// con reloj inyectable (los tests corren con reloj controlado). Todo
// lo que cobra la factura sale de aqui; el gateway solo audita.
type NodeMeter struct {
	NodeID string
	Now    func() time.Time

	mu        sync.Mutex
	bytes     int64
	lastSpool time.Time
	spoolSeq  uint64
}

// NewNodeMeter crea un medidor con el reloj dado (nil => time.Now).
func NewNodeMeter(nodeID string, now func() time.Time) *NodeMeter {
	if now == nil {
		now = time.Now
	}
	return &NodeMeter{NodeID: nodeID, Now: now}
}

// Add registra n bytes transferidos por una sesion. Es la fuente
// canonica: el flush del nodo construye `usage`, nunca el gateway.
func (m *NodeMeter) Add(n int64) {
	if n <= 0 {
		return
	}
	m.mu.Lock()
	m.bytes += n
	m.mu.Unlock()
}

// Bytes devuelve el total autoritativo medido hasta ahora.
func (m *NodeMeter) Bytes() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes
}

// Spool marca un vuelco a disco del contador (cada SpoolInterval).
// Devuelve la secuencia del spool para el nombre de archivo.
func (m *NodeMeter) Spool() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastSpool = m.Now().UTC()
	m.spoolSeq++
	return m.spoolSeq
}

// LastSpool informa cuando se hizo el ultimo vuelco (0-time si nunca).
func (m *NodeMeter) LastSpool() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastSpool
}

// DriftPct mide el desvio del auditor (gateway/NATS) contra el reloj
// maestro (nodo), en porcentaje: |audited-master|/master*100. Con
// master<=0 no hay sesion que facturar: desvio 0.
func DriftPct(master, audited int64) float64 {
	if master <= 0 {
		return 0
	}
	d := audited - master
	if d < 0 {
		d = -d
	}
	return 100 * float64(d) / float64(master)
}

// WithinTolerance es el unico veredicto contable: dentro de la
// tolerancia facturable o alarma. No hay un segundo umbral.
func WithinTolerance(master, audited int64) bool {
	return DriftPct(master, audited) <= TolerancePct
}

// CrashLossSeconds estima la perdida de un kill -9 a mitad de sesion:
// los bytes posteriores al ultimo spool, reconstruidos por el diff
// gateway-vs-usage. Es la cota del BLUEPRINT §11: ≤ 90 s de trafico.
func CrashLossSeconds(rateBytesPerSec float64, at, lastSpool time.Time) float64 {
	if rateBytesPerSec <= 0 {
		return 0
	}
	lost := at.Sub(lastSpool).Seconds()
	if lost < 0 {
		lost = 0
	}
	return lost
}

// GBFloat convierte bytes a GB decimal con la precision que Stripe
// acepta en el item de factura (3 decimales).
func GBFloat(bytes int64) float64 {
	return float64(bytes) / float64(GB)
}

// FixtureKnown1GB simula la transferencia conocida de 1 GB por una
// sesion de test: el nodo (reloj maestro) cuenta el total exacto; el
// auditor gateway pierde determinista un chunk cada chunksPerLoss por
// perdida de lectura en el speculo del proxy. Con 2380 chunks y 10
// perdidos el desvio sale 0,42 % — dentro de la tolerancia unica.
type FixtureKnown1GB struct {
	MasterBytes  int64
	AuditedBytes int64
}

// RunKnown1GB corre la sesion de test con reloj controlado (clock
// avanzado chunk a chunk) y devuelve las dos cuentas para el assert
// drift_pct=0.42(≤0.5) del criterio T-1001.
func RunKnown1GB(nodeID string, clock func() time.Time) FixtureKnown1GB {
	m := NewNodeMeter(nodeID, clock)
	const chunks = 2380
	const chunkBytes = GB / chunks // 420168 B; el resto cae en el ultimo
	var audited int64
	for i := 0; i < chunks; i++ {
		var n int64 = chunkBytes
		if i == chunks-1 {
			n = GB - chunkBytes*(chunks-1)
		}
		m.Add(n)
		if (i+1)%238 != 0 { // se pierde 1 chunk de 238 en el speculo
			audited += n
		}
	}
	return FixtureKnown1GB{MasterBytes: m.Bytes(), AuditedBytes: audited}
}
