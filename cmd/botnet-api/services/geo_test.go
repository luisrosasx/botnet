// geo_test.go — T-0704 con dobles: un GeoObserver fijo observa la geo
// del exit; el veredicto escribe geo_verified/country_probed, el pool
// «residencial» lee de ahi y la cadencia semanal cabe en el
// presupuesto de 50 KB/nodo/mes. Requiere DATABASE_URL; sin motor,
// skip (el test de cadencia es puro y siempre corre).
package services

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

type fakeObserver struct{ r GeoReading }

func (f fakeObserver) Observe(_ context.Context, _ string) (GeoReading, error) { return f.r, nil }

func geoNode(t *testing.T, db *sql.DB, selfCountry string) string {
	t.Helper()
	return testenv.SeedNode(t, db, selfCountry, "Madrid", 12345)
}

func TestGeoCadencePresupuesto(t *testing.T) {
	// AC2: 7 dias simulados -> 7 sondas por nodo (una cadencia semanal
	// por dia simulado) y la ventana real en pared: 7 semanas = 7.
	if got := PlanGeoProbes(7); got != 7 {
		t.Errorf("PlanGeoProbes(7 cadencias) = %d, want 7", got)
	}
	if got := PlanGeoProbes(-1); got != 0 {
		t.Errorf("PlanGeoProbes(-1) = %d, want 0", got)
	}
	if got := GeoWindowSondas(7 * GeoProbeInterval); got != 7 {
		t.Errorf("GeoWindowSondas(7 cadencias reales) = %d, want 7", got)
	}
	if got := GeoWindowSondas(6*GeoProbeInterval + 23*time.Hour); got != 6 {
		t.Errorf("GeoWindowSondas(6c+23h) = %d, want 6 (sin solape)", got)
	}
	// AC2: bytes de sonda ≤ 50 KB/nodo/mes (peor caso del plan).
	if got := GeoMonthlyBytesWorst(); got > GeoMonthlyBytesBudget {
		t.Errorf("presupuesto mensual %d B > %d B", got, GeoMonthlyBytesBudget)
	}

	// Y medido en la tabla de historia: 7 pasadas -> 7 filas, bytes
	// contados por la propia sonda.
	db := testenv.PG(t)
	rp := repo.New(db)
	nodeID := geoNode(t, db, "ES")
	obs := fakeObserver{r: GeoReading{Country: "ES", ASN: 12345, BytesTX: GeoProbeBytesTX}}
	for i := 0; i < 7; i++ {
		if _, err := ApplyGeoProbe(context.Background(), rp, nodeID, obs); err != nil {
			t.Fatalf("sonda %d: %v", i, err)
		}
	}
	probes, bytesTX, err := rp.ProbeStats(context.Background(), nodeID, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if probes != 7 {
		t.Errorf("7 dias: %d sondas en geo_probes, want 7", probes)
	}
	if probes*bytesTX <= 0 || bytesTX > GeoMonthlyBytesBudget {
		t.Errorf("bytes de sonda/mes: %d B, tope %d B", bytesTX, GeoMonthlyBytesBudget)
	}
}

func TestGeoVerdictResidencialYDC(t *testing.T) {
	db := testenv.PG(t)
	rp := repo.New(db)
	ctx := context.Background()

	// AC1 fixture ES residencial: pais/ASN esperados -> verified=true
	// y el nodo entra en el pool que lee geo_verified + country_probed.
	nodeES := geoNode(t, db, "ES")
	ok, err := ApplyGeoProbe(ctx, rp, nodeES,
		fakeObserver{r: GeoReading{Country: "ES", ASN: 12345}})
	if err != nil {
		t.Fatalf("sonda ES: %v", err)
	}
	if !ok {
		t.Error("ES residencial esperado y observado: geo_verified debe ser true")
	}
	if id, err := rp.PickResidential(ctx, "ES"); err != nil || id != nodeES {
		t.Errorf("pool ES: pick %q err %v, want el nodo verificado", id, err)
	}

	// AC1: egress en DC extranjero -> verified=false y sale del pool
	// al instante (el select no espera 24 h: el criterio es < 24 h).
	nodeDC := geoNode(t, db, "ES")
	if _, err := ApplyGeoProbe(ctx, rp, nodeDC,
		fakeObserver{r: GeoReading{Country: "ES", ASN: 12345}}); err != nil {
		t.Fatal(err) // entra verificado...
	}
	bad, err := ApplyGeoProbe(ctx, rp, nodeDC,
		fakeObserver{r: GeoReading{Country: "US", ASN: 16509}}) // AWS en US
	if err != nil {
		t.Fatal(err)
	}
	if bad {
		t.Error("DC extranjero (ASN AWS, egress US): geo_verified debe ser false")
	}
	var verified bool
	if err := db.QueryRowContext(ctx,
		`SELECT geo_verified FROM nodes WHERE node_id = $1::uuid`, nodeDC).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if verified {
		t.Error("nodes.geo_verified sigue true tras la sonda de DC")
	}
	if id, err := rp.PickResidential(ctx, "ES"); err != nil || id == nodeDC {
		t.Errorf("pool ES tras sonda DC: pick %q err %v — el nodo debe haber salido", id, err)
	}
}

func TestPoolLeeGeoSondeadaNoAutorreporte(t *testing.T) {
	// AC3: self-report ES, sonda US (ASN no DC) -> NO se vende como
	// ES: el pais del pool es el sondeado.
	db := testenv.PG(t)
	rp := repo.New(db)
	nodeID := geoNode(t, db, "ES")
	if _, err := ApplyGeoProbe(context.Background(), rp, nodeID,
		fakeObserver{r: GeoReading{Country: "US", ASN: 12345}}); err != nil {
		t.Fatal(err)
	}
	var probed string
	if err := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(country_probed::text, '') FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&probed); err != nil {
		t.Fatal(err)
	}
	if probed != "US" {
		t.Errorf("country_probed = %q, want US (la sonda manda, no el self-report)", probed)
	}
	if id, err := rp.PickResidential(context.Background(), "ES"); err != nil || id == nodeID {
		t.Errorf("el nodo self-report ES con sonda US no debe venderse como ES: pick %q err %v", id, err)
	}
}
