// Test de transparencia contable (T-1004 criterio 2): GET /t/{slug}
// publica el tope de VOLUMEN monthly_gb_cap=150 (A2-20: la cuota del
// dueno es de GB, no solo de velocidad) y fecha el consentimiento.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestTransparencyPublishesCap150(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	var slug string
	if err := db.QueryRowContext(context.Background(),
		`SELECT slug FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	app := NewApp(&Deps{Repo: repo.New(db)})
	req := httptest.NewRequest(http.MethodGet, "/t/"+slug, nil)
	req.Header.Set("Accept", "application/json") // la JSON es explicita; defectivo = HTML
	resp, err := app.Test(req, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /t/%s: %d", slug, resp.StatusCode)
	}
	var page map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if got := page["monthly_gb_cap"]; got != float64(150) {
		t.Fatalf("la pagina no publica el tope 150: %v", got)
	}
	if page["consent_date"] == nil || page["consent_date"] == "" {
		t.Fatal("la pagina debe fechar el consentimiento")
	}
}
