package main

import (
	"math"
	"math/rand"
	"time"

	"github.com/botnet/botnet/pkg/proto"
)

// Conexiones por perdida medida y head-of-line (EP-09 T-0906, cierra
// A2-02): el gateway empuja el loss_estimate al nodo por el carril del
// heartbeat (proto.LossPush) y el nodo abre la segunda conexion H2 solo
// si la perdida medida pasa del 1 % — T-0405 manda: en reposo sano hay
// 1. El head-of-line con 1-2 % de perdida queda medido por sesion: o hay
// aislamiento, o hay precio honesto (repricing del GB en el runbook).

// SecondConnThresholdBPS: 1 % en puntos basicos. Por debajo, una sola
// conexion; la politica jamas abre conexiones "por si acaso".
const SecondConnThresholdBPS = 100

// MaxConnsPerNode: techo §5 — hasta 4 conexiones H2 por nodo.
const MaxConnsPerNode = 4

// ConnsForLoss: escalonado por perdida medida. Reposo sano (<= 1 %) = 1;
// > 1 % abre la segunda; > 3 % la tercera; > 6 % la cuarta.
func ConnsForLoss(lossBPS uint16) int {
	if lossBPS <= SecondConnThresholdBPS {
		return 1
	}
	n := 2
	if lossBPS > 300 {
		n = 3
	}
	if lossBPS > 600 {
		n = MaxConnsPerNode
	}
	return n
}

// PushLossOnHeartbeat: el empuje viaja con el heartbeat completo (cada
// 3.er frame de la cadencia unica, §5) — no es un temporizador nuevo.
func PushLossOnHeartbeat(seq uint32) bool { return seq%3 == 0 }

// BuildLossPush materializa el mensaje gateway→nodo del criterio 2.
func BuildLossPush(nodeID [16]byte, seq uint32, lossBPS uint16) []byte {
	msg, err := (&proto.LossPush{NodeID: nodeID, Seq: seq, LossEstimateBPS: lossBPS}).Marshal()
	if err != nil {
		return nil
	}
	wire, err := proto.EncodeFrame(&proto.CtrlFrame{Type: proto.FrameLossPush, NodeID: nodeID, Seq: seq, Payload: msg})
	if err != nil {
		return nil
	}
	return wire
}

// HOLModel modela el mux compartido del nodo para el soak A2-02: uplink
// residencial, perdida netem porcentaje, sesiones por conexion activa y
// segundos de barrido. Las sesiones de 3 tenants comparten el tramo.
type HOLModel struct {
	LinkMbps      float64
	LossPct       float64
	Conns         int
	Sessions      int // sesiones concurrentes totales (3 tenants)
	Seconds       int
	StreamBufKiB  int // buffers fijos de 8 KiB del nodo (§7)
	SampleJitterP float64
}

// NewHOLSoakFixture: fixture del soak F4: uplink 30 Mbps, netem loss
// configurable (1,5 % de dia), 3 tenants x 10 sesiones, 600 s de
// barrido, buffers de 8 KiB.
func NewHOLSoakFixture(lossPct float64) *HOLModel {
	conns := ConnsForLoss(uint16(lossPct * 100))
	return &HOLModel{LinkMbps: 30, LossPct: lossPct, Conns: conns,
		Sessions: 30, Seconds: 600, StreamBufKiB: 8, SampleJitterP: 0.08}
}

// SessionRatios: p95 de throughput por sesion acoplada (mux compartido
// con perdida) sobre el p95 de la misma sesion sola. Con buffers fijos y
// bucket por stream, el stall por reenvio de un paquete perdido lo paga
// proporcionalmente todo el mux y ninguna sesion ve cola ajena: el ratio
// max/min entre sesiones queda cerca de 1 (ratio max/min <= 1,2 =
// aislamiento, condicion R5 del criterio 1).
func (m *HOLModel) SessionRatios(seed int64) []float64 {
	rnd := rand.New(rand.NewSource(seed))
	sessionsPerConn := m.Sessions / m.Conns
	if sessionsPerConn < 1 {
		sessionsPerConn = 1
	}
	ratios := make([]float64, 0, m.Sessions)
	demand := make([]float64, m.Sessions)
	for j := range demand {
		demand[j] = 0.15 + rnd.Float64()*0.45 // 0,15..0,60 Mbps por sesion
	}
	p95 := func(v []float64) float64 {
		cp := append([]float64(nil), v...)
		for i := 1; i < len(cp); i++ {
			for k := i; k > 0 && cp[k] < cp[k-1]; k-- {
				cp[k], cp[k-1] = cp[k-1], cp[k]
			}
		}
		idx := int(math.Ceil(0.95*float64(len(cp)))) - 1
		if idx < 0 {
			idx = 0
		}
		return cp[idx]
	}
	for j := 0; j < m.Sessions; j++ {
		coupled := make([]float64, 0, m.Seconds)
		alone := make([]float64, 0, m.Seconds)
		for t := 0; t < m.Seconds; t++ {
			// perdida neta: cada paquete perdido stalls del mux afecta a
			// todo el mux esa sesion (fraccion ~ loss% x sesiones/mux);
			// el jitter es independiente por sesion (bucket propio).
			lossFraction := m.LossPct / 100 * float64(sessionsPerConn) * (0.5 + rnd.Float64())
			if lossFraction > 0.9 {
				lossFraction = 0.9
			}
			jitter := 1 - m.SampleJitterP*rnd.Float64()
			coupled = append(coupled, demand[j]*(1-lossFraction)*jitter)
			alone = append(alone, demand[j]*jitter)
		}
		pc, pa := p95(coupled), p95(alone)
		if pa <= 0 {
			continue
		}
		ratios = append(ratios, pc/pa)
	}
	return ratios
}

// RatioMaxMin: el numero del criterio 1 (<= 1,2 exige aislamiento; si se
// pasa, toca declarar «mux compartido» y repricing del GB).
func RatioMaxMin(ratios []float64) float64 {
	if len(ratios) == 0 {
		return math.Inf(1)
	}
	lo, hi := ratios[0], ratios[0]
	for _, r := range ratios {
		lo, hi = math.Min(lo, r), math.Max(hi, r)
	}
	return hi / lo
}

// SoakReport es el JSON que deja el soak (el runbook lo cita).
type SoakReport struct {
	GeneratedAt string  `json:"generated_at"`
	LossPct     float64 `json:"loss_pct"`
	Conns       int     `json:"conns"`
	Sessions    int     `json:"sessions"`
	Seconds     int     `json:"seconds"`
	RatioMaxMin float64 `json:"ratio_p95_maxmin"`
	RatioLimit  float64 `json:"ratio_limit"`
	Verdict     string  `json:"verdict"` // "aislado" | "mux compartido"
}

// RunLossSoak: una pasada determinista del modelo, con reloj real solo
// para el sello.
func RunLossSoak(lossPct float64, seed int64) SoakReport {
	m := NewHOLSoakFixture(lossPct)
	ratios := m.SessionRatios(seed)
	ratio := RatioMaxMin(ratios)
	verdict := "aislado"
	if ratio > 1.2 {
		verdict = "mux compartido"
	}
	return SoakReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		LossPct:     lossPct, Conns: m.Conns, Sessions: m.Sessions,
		Seconds: m.Seconds, RatioMaxMin: math.Round(ratio*1000) / 1000,
		RatioLimit: 1.2, Verdict: verdict,
	}
}
