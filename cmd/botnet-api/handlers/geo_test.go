// geo_test.go — T-0704 en la capa HTTP: POST /v1/nodes/{id}/geo es la
// puerta del script geo_probe.sh (bearer admin, mismo esquema que
// revoke); el veredicto del service queda en nodes y el JSON lo
// devuelve tipado. Requiere DATABASE_URL; sin motor, skip.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestGeoProbeEndpoint(t *testing.T) {
	db := testenv.PG(t)
	rp := repo.New(db)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	cfg := backend.ConfigFromEnv() // AdminToken dev: el bearer del script
	app := NewApp(&Deps{Cfg: cfg, Repo: rp})

	call := func(node, body, bearer string) *http.Response {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+node+"/geo", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	if resp := call(nodeID, `{"country":"ES","asn":12345}`, ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("sin bearer: %d, want 401", resp.StatusCode)
	}
	if resp := call(nodeID, `{"country":"x","asn":12345}`, cfg.AdminToken); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("pais de burla: %d, want 400", resp.StatusCode)
	}
	resp := call(nodeID, `{"country":"ES","asn":12345,"bytes_tx":700}`, cfg.AdminToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sonda ES residencial: %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out["geo_verified"] != true || out["country_probed"] != "ES" {
		t.Errorf("veredicto ES residencial: %v", out)
	}
	var verified bool
	if err := db.QueryRowContext(context.Background(),
		`SELECT geo_verified FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&verified); err != nil || !verified {
		t.Errorf("nodes.geo_verified tras el 200: %v err %v", verified, err)
	}

	// El script no crea inventario: nodo fantasma con token bueno = 404.
	ghost := "11111111-1111-4111-8111-111111111111"
	if resp := call(ghost, `{"country":"ES","asn":12345}`, cfg.AdminToken); resp.StatusCode != http.StatusNotFound {
		t.Errorf("nodo fantasma: %d, want 404", resp.StatusCode)
	}
}
