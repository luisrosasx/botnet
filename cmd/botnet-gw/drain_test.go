package main

import (
	"context"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/backend/testenv"
)

// memSessions es el doble de `sessions` (ended_at NULL/now): el assert
// psql `count(*) ... ended_at IS NULL AND gw_id=$GW` = 0 tiene aqui su
// espejo.
type memSessions struct {
	open map[string]string // sessionID -> gwID
}

func (m *memSessions) OpenSessions(gwID string) []string {
	var out []string
	for sid, g := range m.open {
		if g == gwID {
			out = append(out, sid)
		}
	}
	return out
}

func (m *memSessions) EndSession(_ context.Context, sid string) error {
	delete(m.open, sid)
	return nil
}

// AC1: SET gw:drain:$GW 1 → 0 admisiones nuevas, GOAWAY progresivo y 0
// sesiones abiertas del gateway pasados 60 s (huerfanos: ninguno).
func TestDrainFlagAndZeroOrphans(t *testing.T) {
	rdb := testenv.Redis(t)
	ctx := context.Background()
	store := &memSessions{open: map[string]string{}}
	for i := 0; i < 250; i++ {
		store.open["s"+itoa(i)] = "gw-es-mad"
	}
	goaways := 0
	d := &Drainer{GWID: "gw-es-mad", RDB: rdb, Store: store, Step: 50,
		Goaway: func(string) { goaways++ }}

	if err := d.AdmitNew(ctx); err != nil {
		t.Fatalf("antes del drenaje hay admision: %v", err)
	}
	if err := rdb.Set(ctx, DrainKey("gw-es-mad"), "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := d.AdmitNew(ctx); err != ErrAdmissionDrained {
		t.Fatalf("durante el drenaje: 0 admisiones nuevas (got %v)", err)
	}

	start := time.Now()
	remaining, err := d.DrainOnce(ctx)
	if err != nil {
		t.Fatalf("DrainOnce: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("huerfanos tras el drenaje: %d (0 exigido)", remaining)
	}
	if elapsed := time.Since(start); elapsed > DrainBudget {
		t.Fatalf("drenaje fuera de presupuesto: %v", elapsed)
	}
	if goaways != 250 {
		t.Fatalf("GOAWAY progresivo: %d (250 sesiones)", goaways)
	}
}

// AC2: el rdv re-asigna el 100 % de los nodos drenados a otros gateways
// (fixture 250 nodos; el destino vive en el gw:pool de otro gateway).
func TestReassignDrainedNodes(t *testing.T) {
	nodes := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		nodes = append(nodes, "node-"+itoa(i))
	}
	dest, unassigned := ReassignDrained(nodes, func(node string) string {
		// fixture ES/Madrid drenada de gw-es-mad: el rdv casa con el
		// siguiente gateway ES (geo.go T-0902: failover mismo pais).
		if len(node) > 6 && node[6] == 'v' {
			return "" // un nodo sin pool destino: el assert lo caza
		}
		return "gw-es-vlc"
	})
	if len(dest) != 250 || unassigned != 0 {
		t.Fatalf("re-asignados: %d, sin destino: %d (100 %% exigido)", len(dest), unassigned)
	}
	// El destino es otro pool: gw:pool:gw-es-vlc contera a los 250.
	for node, gw := range dest {
		if gw == "gw-es-mad" || gw == "" {
			t.Fatalf("%s sigue en el gateway drenado", node)
		}
	}
}

// AC3 (cifra del censo): 250 nodos x egress medio medido por nodo >= 75
// Mbps sostenidos — el edge se dimensiona con numeros, no de fe.
func TestEgressCensusThreshold(t *testing.T) {
	cases := []struct {
		provider string
		perNode  float64
	}{
		{"movistar", 0.32}, {"orange", 0.33}, {"jazztel", 0.31}, {"wiggg", 0.36},
	}
	for _, c := range cases {
		eg := SustainedEgressMbps(250, c.perNode)
		if eg < 75 {
			t.Fatalf("%s: %.1f Mbps < 75 (250 nodos)", c.provider, eg)
		}
	}
}
