package main

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/botnet/botnet/pkg/proto"
)

// AC1: soak netem loss 1,5 % con sesiones de 3 tenants — p95 de
// throughput por sesion independiente entre sesiones (ratio max/min
// p95 <= 1,2, condicion R5).
func TestLossSoakReport(t *testing.T) {
	lossPct := envFloat("SOAK_LOSS_PCT", 1.5)
	rep := RunLossSoak(lossPct, 4242)
	if lossPct > 1.0 && rep.Conns < 2 {
		t.Fatalf("con %.1f %% medido la politica debe abrir segunda conexion: %d", lossPct, rep.Conns)
	}
	if rep.RatioMaxMin > rep.RatioLimit {
		t.Fatalf("ratio p95 max/min %.3f > %.1f: hay head-of-line — declarar «mux compartido» y repricing del GB",
			rep.RatioMaxMin, rep.RatioLimit)
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile("../../docs/verification/loss_soak_report.json", b, 0o644); err != nil {
		t.Fatalf("informe soak: %v", err)
	}
	t.Logf("loss_soak: %s", b)
}

func envFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}

// AC2: el gateway empuja loss_estimate al nodo (carril heartbeat, cada
// 3.er frame) y el nodo abre la segunda conexion solo si > 1 % medido.
// En reposo sano hay 1 conexion (T-0405 manda).
func TestLossDrivenConns(t *testing.T) {
	if got := ConnsForLoss(40); got != 1 {
		t.Fatalf("reposo sano 0,4 %%: %d conexiones (1 exigida)", got)
	}
	if got := ConnsForLoss(100); got != 1 { // el umbral es "> 1 %"
		t.Fatalf("1,0 %% exacto no abre segunda: %d", got)
	}
	if got := ConnsForLoss(150); got != 2 {
		t.Fatalf("1,5 %%: %d (segunda exigida)", got)
	}
	if got := ConnsForLoss(700); got > MaxConnsPerNode || got < 3 {
		t.Fatalf("7 %%: %d fuera del escalonado §5", got)
	}
	if !PushLossOnHeartbeat(3) || PushLossOnHeartbeat(4) {
		t.Fatal("el empuje viaja con el heartbeat completo (cada 3.er frame)")
	}
	wire := BuildLossPush([16]byte{1}, 3, 150)
	if wire == nil {
		t.Fatal("BuildLossPush nil")
	}
	f, err := proto.DecodeFrame(wire)
	if err != nil || f.Type != proto.FrameLossPush {
		t.Fatalf("frame: %v %v", f, err)
	}
	var lp proto.LossPush
	if err := lp.Unmarshal(f.Payload); err != nil || lp.LossEstimateBPS != 150 {
		t.Fatalf("payload: %v %v", lp, err)
	}
}
