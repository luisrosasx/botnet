// services/caps.go — T-0703: el tope de ancho del dueno es MEDIDO, no
// declarado. La sonda iperf3 por la malla (scripts/mesh/iperf3_probe.sh)
// siembra nodes.bandwidth_cap_kbps con valor en [medido × 0.8, medido];
// la pagina de transparencia lo refleja. Los nodos sin `tun` no tienen
// sonda y conservan el default del perfil de clase (BLUEPRINT §2), con
// el que el agente aplica su bucket igualmente (T-0604).
package services

import (
	"context"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
)

// DefaultCapKbps: topes del perfil de clase (blueprint §2: A 5 Mbps,
// B 3 Mbps, C 2 Mbps). El default de la columna en 0003_core es 2048
// — el mas conservador — y esta funcion es la fuente canonica para
// el agente cuando no hay sonda (AC2).
func DefaultCapKbps(deviceClass string) int {
	switch deviceClass {
	case "A":
		return 5000
	case "B":
		return 3000
	default: // C y desconocidas: el piso comun.
		return 2048
	}
}

// CapProbeFactor: factor de siembra sobre lo medido por iperf3. 0.9
// cae dentro de la banda exigida [0.8, 1.0] × medido (AC1) y deja
// margen contra el jitter del link sin inflar el tope.
const CapProbeFactor = 0.9

// capSmallintMax/capFloorKbps: la columna es smallint (techo 32767) y
// un bucket de 64 kbps ya es util; los bordes nunca violan la banda
// porque el piso se recorta al propio medido.
const (
	capSmallintMax = 32767
	capFloorKbps   = 64
)

// CapFromProbe traduce los kbps medidos por la sonda en el tope a
// sembrar. invariantes: 0 < medido => cap <= medido y cap >= 0.8 ×
// medido (AC1; fixture 4096 → 3686 ∈ [3276, 4096]). medido <= 0 es
// sonda basura: devuelve 0 y el llamador no escribe.
func CapFromProbe(measuredKbps int) int {
	if measuredKbps <= 0 {
		return 0
	}
	seed := int(float64(measuredKbps) * CapProbeFactor)
	if seed < capFloorKbps {
		seed = capFloorKbps
	}
	if seed > measuredKbps { // enlaces finos: el piso no puede pasar el medido
		seed = measuredKbps
	}
	if seed > capSmallintMax {
		seed = capSmallintMax
	}
	return seed
}

// CapProber dobla el iperf3 real: la implementacion de produccion es
// el resultado del script de sonda por la malla (kbps del receptor,
// que es el cuello del upload del nodo). El test inyecta un doble.
type CapProber interface {
	Probe(ctx context.Context, nodeID string) (kbps int, err error)
}

// SeedBandwidthCap escribe nodes.bandwidth_cap_kbps con CapFromProbe
// de lo medido. prober == nil es el caso AC2 (nodo sin tun, sin
// sonda): no escribe nada y devuelve el default del perfil de clase,
// que es exactamente lo que el agente ya aplica. La sonda jamas abre
// sesion SOCKS: no toca la tabla sessions (AC3, medido por el test
// con delta = 0 sobre count de sessions).
func SeedBandwidthCap(ctx context.Context, rp *repo.Data, nodeID, deviceClass string, prober CapProber) (int, error) {
	if prober == nil {
		return DefaultCapKbps(deviceClass), nil
	}
	measured, err := prober.Probe(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	seed := CapFromProbe(measured)
	if seed == 0 {
		return 0, nil // sonda basura: el default de clase manda un ciclo mas
	}
	if err := rp.SetBandwidthCap(ctx, nodeID, seed); err != nil {
		return 0, err
	}
	return seed, nil
}
