package agent

import (
	"math/rand"
	"testing"

	"github.com/botnet/botnet/internal/hbcodec"
)

// AC1: el heartbeat porta rss_kib, streams, loss_estimate y ver
// codificados por hbcodec — round-trip exacto (assert de vectores en
// internal/hbcodec; aqui se aserta la via telemetria→frame→decode).
func TestTelemetryHeartbeatVectorRoundTrip(t *testing.T) {
	nid := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	tel := Telemetry{RSSKiB: 3145, Streams: 37, LossEstimateBPS: 250, Ver: "v0.1.0"}
	raw, err := tel.BeatFrame(nid, 7, 4242)
	if err != nil {
		t.Fatalf("BeatFrame: %v", err)
	}
	var back hbcodec.Heartbeat
	if err := back.Decode(raw); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.RssKiB != tel.RSSKiB || back.Streams != tel.Streams ||
		back.LossEstimate != tel.LossEstimateBPS || back.AgentVer != tel.Ver {
		t.Fatalf("telemetria no viajo intacta: %+v", back)
	}
	if back.UptimeS != 4242 || back.Seq != 7 || back.NodeID != nid {
		t.Fatalf("identidad del latido perdida: %+v", back)
	}
}

// AC2: el p99 de RSS de la flota simulada queda bajo el techo del
// panel con alerta (6 MB): 1.000 nodos con 0..150 streams segun el
// presupuesto del blueprint (reposo 3,1 MB + pico 4,6 MB).
func TestFleetRSSP99UnderAlert(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	fleet := make([]uint32, 1000)
	for i := range fleet {
		streams := rng.Intn(151)
		kib := 3174 + streams*1536/150 + rng.Intn(103) // 3,1 MiB + pico por streams + ruido
		fleet[i] = uint32(kib)
	}
	p99 := FleetRSSP99KiB(fleet)
	limitKiB := uint32(AlertRSSLimitMiB * 1024)
	if p99 > limitKiB {
		t.Fatalf("p99 RSS de flota = %d KiB > alerta %d KiB", p99, limitKiB)
	}
	if p99 == 0 {
		t.Fatal("p99 = 0: la muestra no alimenta el percentil")
	}
}

// AC3: frescura del inventario de versiones — el ver del heartbeat
// coincide con el del nodo en < 1 latido (nace de la misma
// instantanea, sin viaje extra).
func TestVerFreshnessUnderOneBeat(t *testing.T) {
	nid := [16]byte{}
	nodeVer := "v0.9.9-rc1"
	tel := Collect(nodeVer, 5, 0)
	var back hbcodec.Heartbeat
	raw, err := tel.BeatFrame(nid, 1, 0)
	if err != nil {
		t.Fatalf("BeatFrame: %v", err)
	}
	if err := back.Decode(raw); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.AgentVer != nodeVer {
		t.Fatalf("ver del latido = %q != ver del nodo %q (frescura < 1 heartbeat)", back.AgentVer, nodeVer)
	}
	// Veres mas largas que el hueco del codec se recortan por contrato.
	long := Collect("v0.12.345678901234567890", 0, 0)
	if len(long.Ver) > hbcodec.MaxAgentVer {
		t.Fatalf("ver sin recortar: %q", long.Ver)
	}
}
