// caps_test.go — T-0703 con dobles: un CapProber falso siembra el link
// de la fixture (4 Mbps) y el tope escrito cae en [medido × 0.8,
// medido]; sin sonda (nodo sin tun) queda el default del perfil; y la
// sonda no abre ninguna sesion SOCKS (delta sessions = 0). Requiere
// DATABASE_URL (Postgres portable) — sin motor, skip.
package services

import (
	"context"
	"database/sql"
	"testing"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

type fakeProber struct {
	kbps int
	err  error
}

func (f fakeProber) Probe(_ context.Context, _ string) (int, error) {
	return f.kbps, f.err
}

func seedNodeOfClass(t *testing.T, db *sql.DB, class string) string {
	t.Helper()
	id := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET device_class = $2 WHERE node_id = $1::uuid`, id, class); err != nil {
		t.Fatalf("clasificar nodo %s: %v", class, err)
	}
	return id
}

func TestCapFromProbeBanda(t *testing.T) {
	// AC1: link de 4 Mbps → tope ∈ [3276, 4096] (la banda del task
	// es [0.8, 1.0] × medido; el factor 0.9 debe caer dentro SIEMPRE).
	for _, m := range []int{2048, 4096, 5000, 3072, 32767, 100} {
		seed := CapFromProbe(m)
		low := int(float64(m) * 0.8)
		if seed < low || seed > m {
			t.Errorf("medido %d: tope %d fuera de [%d, %d]", m, seed, low, m)
		}
	}
	if CapFromProbe(0) != 0 || CapFromProbe(-9) != 0 {
		t.Error("sonda basura (<=0) debe dar 0 y no escribir")
	}
	if CapFromProbe(4096) > 32767 {
		t.Error("el tope debe caber en smallint")
	}
}

func TestSeedBandwidthCapEscribeFixure4Mbps(t *testing.T) {
	db := testenv.PG(t)
	rp := repo.New(db)
	nodeID := seedNodeOfClass(t, db, "B")

	seed, err := SeedBandwidthCap(context.Background(), rp, nodeID, "B", fakeProber{kbps: 4096})
	if err != nil {
		t.Fatalf("SeedBandwidthCap: %v", err)
	}
	if seed < 3276 || seed > 4096 {
		t.Errorf("fixture 4 Mbps: tope devuelto %d fuera de [3276, 4096]", seed)
	}
	var stored int
	if err := db.QueryRowContext(context.Background(),
		`SELECT bandwidth_cap_kbps::int FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored < 3276 || stored > 4096 {
		t.Errorf("nodes.bandwidth_cap_kbps escrito = %d, want ∈ [3276, 4096]", stored)
	}
}

func TestSinSondaQedaDefaultDeClase(t *testing.T) {
	// AC2: nodo sin tun (prober nil) — no se escribe nada y el
	// default del perfil manda: C 2048 (el de schema), B 3000, A 5000.
	db := testenv.PG(t)
	rp := repo.New(db)
	for _, class := range []string{"A", "B", "C"} {
		nodeID := seedNodeOfClass(t, db, class)
		got, err := SeedBandwidthCap(context.Background(), rp, nodeID, class, nil)
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		if got != DefaultCapKbps(class) {
			t.Errorf("%s sin sonda: %d, want default %d", class, got, DefaultCapKbps(class))
		}
		stored, err := rp.BandwidthCapKbps(context.Background(), nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if stored != 2048 { // el default de 0003_core intacto: la funcion no escribe
			t.Errorf("%s sin sonda: la columna debe quedar en el default 2048, hay %d", class, stored)
		}
	}
}

func TestSondaNoAbreSesionesSOCKS(t *testing.T) {
	// AC3: durante la sonda el contador de sesiones no se mueve.
	db := testenv.PG(t)
	rp := repo.New(db)
	nodeID := seedNodeOfClass(t, db, "A")

	before, err := rp.CountSessionsForNode(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SeedBandwidthCap(context.Background(), rp, nodeID, "A", fakeProber{kbps: 5120}); err != nil {
		t.Fatal(err)
	}
	after, err := rp.CountSessionsForNode(context.Background(), nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if after-before != 0 {
		t.Errorf("la sonda abrio %d sesiones SOCKS, want 0", after-before)
	}
}
