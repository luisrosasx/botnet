// telemetry.go — T-1202: la salud del nodo se lee del latido, no del
// router (BLUEPRINT §12). El heartbeat porta rss_kib, streams,
// loss_estimate y ver codificados por hbcodec; el panel (Redis vivo /
// Postgres histórico) los pinta sin preguntar al router.
package agent

import (
	"sort"

	"github.com/botnet/botnet/internal/hbcodec"
	"github.com/botnet/botnet/pkg/logger"
)

// AlertRSSLimitMiB es el techo del panel con alerta (fleet.json): el
// p99 de RSS de la flota real debe quedar por debajo (SLO §12, 6 MB).
const AlertRSSLimitMiB = 6

// Telemetry es la instantánea de salud del nodo que sube al latido.
// LossEstimateBPS son puntos básicos (0..10000) estimados por el
// watchdog de cadencia: pulsos perdidos sobre enviados.
type Telemetry struct {
	RSSKiB          uint32
	Streams         uint16
	LossEstimateBPS uint16
	Ver             string
}

// Collect toma la instantánea actual: RSS por la doble fuente de
// logger.SampleRSSKiB (procfs exacto donde existe), streams del proxy
// y la version embebida del binario (la misma que nodes.agent_version
// por construccion: el latido nace con la ver del proceso).
func Collect(ver string, streams int, lossEstimateBPS int) Telemetry {
	if streams < 0 {
		streams = 0
	}
	if streams > 0xFFFF {
		streams = 0xFFFF
	}
	if lossEstimateBPS < 0 {
		lossEstimateBPS = 0
	}
	if lossEstimateBPS > 10000 {
		lossEstimateBPS = 10000
	}
	return Telemetry{
		RSSKiB:          logger.SampleRSSKiB(),
		Streams:         uint16(streams),
		LossEstimateBPS: uint16(lossEstimateBPS),
		Ver:             clipVer(ver),
	}
}

// clipVer recorta la version al hueco del codec (<= 16 B); el borde es
// un truncado por contrato, no un fallo: el panel lo pinta igualmente.
func clipVer(v string) string {
	if len(v) <= hbcodec.MaxAgentVer {
		return v
	}
	return v[:hbcodec.MaxAgentVer]
}

// Heartbeat arma la carga del latido completo lista para Encode.
func (t Telemetry) Heartbeat(nodeID [16]byte, seq uint32, uptimeS uint32) *hbcodec.Heartbeat {
	return &hbcodec.Heartbeat{
		NodeID:       nodeID,
		Seq:          seq,
		RssKiB:       t.RSSKiB,
		UptimeS:      uptimeS,
		Streams:      t.Streams,
		LossEstimate: t.LossEstimateBPS,
		AgentVer:     t.Ver,
	}
}

// BeatFrame serializa el latido con su telemetria (el frame que viaja
// por el tunel; Pad deja el relleno anti-DPI al codec).
func (t Telemetry) BeatFrame(nodeID [16]byte, seq uint32, uptimeS uint32) ([]byte, error) {
	return t.Heartbeat(nodeID, seq, uptimeS).Encode()
}

// FleetRSSP99KiB calcula el percentil 99 de una muestra de RSS (KiB)
// de la flota: la alerta del panel se valida aqui antes de pintar.
// Muestra vacia devuelve 0.
func FleetRSSP99KiB(samples []uint32) uint32 {
	if len(samples) == 0 {
		return 0
	}
	cp := make([]uint32, len(samples))
	copy(cp, samples)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := (99 * len(cp)) / 100
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}
