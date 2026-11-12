// Test T-1105 (EP-11): el tenant opera solo contra la API real con
// su api key — alta de claves, uso en vivo desde Redis (< 2 s),
// historico desde Postgres, cuota editable en sus 3 campos y factura
// del periodo con stripe_customer_id.
package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func panelSetup(t *testing.T) (context.Context, *sql.DB, *fiber.App, string) {
	t.Helper()
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()
	tenantID := testenv.SeedTenant(t, db, "tenant-panel")
	if _, err := db.ExecContext(ctx, `UPDATE tenants SET stripe_customer_id = 'cus_panel_test' WHERE tenant_id = $1::uuid`, tenantID); err != nil {
		t.Fatal(err)
	}
	testenv.SeedQuota(t, db, tenantID, 50, 40)
	testenv.SeedKey(t, db, tenantID, "bk_panel_key")
	app := NewApp(&Deps{Repo: repo.New(db), RDB: rdb, Keys: backend.NewApiKeyCache(rdb, repo.New(db), 300 * time.Second)})
	return ctx, db, app, tenantID
}

func panelCall(t *testing.T, app *fiber.App, method, path, key string, body []byte) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// panelCallRaw: GET cuya respuesta es un JSON array con >= minItems
// elementos; devuelve el codigo si cumple y -1 si el array no casa.
func panelCallRaw(t *testing.T, app *fiber.App, path, key string, minItems int) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var arr []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		return -1
	}
	if len(arr) < minItems {
		return -1
	}
	return resp.StatusCode
}

func TestPanelTenantFlow(t *testing.T) {
	ctx, db, app, tenantID := panelSetup(t)
	const key = "bk_panel_key"

	// Credencial invalida: 403 (la api key es la puerta).
	if code, _ := panelCall(t, app, http.MethodGet, "/v1/panel/usage", "no-existe", nil); code != http.StatusForbidden {
		t.Fatalf("key invalida: quiero 403, tengo %d", code)
	}

	// Alta de api key (flow completo): 201 con la en claro una sola
	// vez; la clave nueva autentica usage.
	code, out := panelCall(t, app, http.MethodPost, "/v1/panel/api-keys", key,
		[]byte(`{"label":"ci","mode":"sticky","sticky_ttl_s":600}`))
	if code != http.StatusCreated {
		t.Fatalf("alta api key: %d %v", code, out)
	}
	plain, _ := out["api_key"].(string)
	if plain == "" {
		t.Fatal("la api key en claro no viajo en el alta")
	}
	code, out = panelCall(t, app, http.MethodGet, "/v1/panel/usage", plain, nil)
	if code != http.StatusOK {
		t.Fatalf("usage con key nueva: %d %v", code, out)
	}
	if code := panelCallRaw(t, app, "/v1/panel/api-keys", key, 2); code != http.StatusOK {
		t.Fatal("api-keys list: el array no traia >= 2 claves")
	}

	// Uso en vivo desde Redis: el contador del periodo se ve < 2 s.
	period := time.Now().UTC().Format("2006-01-02")
	if err := db.QueryRowContext(ctx, `SELECT to_char(period_start,'YYYY-MM-DD') FROM quotas WHERE tenant_id = $1::uuid`, tenantID).Scan(&period); err != nil {
		t.Fatal(err)
	}
	rdb := testenv.Redis(t)
	rdb.Set(ctx, "tenant:"+tenantID+":gb_used:"+period, 3.5, time.Hour)
	started := time.Now()
	code, out = panelCall(t, app, http.MethodGet, "/v1/panel/usage", key, nil)
	if elapsed := time.Since(started); code != http.StatusOK || elapsed > 2*time.Second {
		t.Fatalf("usage live: %d en %v", code, elapsed)
	}
	if live, _ := out["gb_used_live"].(float64); live != 3.5 {
		t.Fatalf("gb_used_live: %v", out["gb_used_live"])
	}

	// Historico desde Postgres: nodo + session + usage de ayer.
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (node_id, tenant_id, gw_id, started_at)
		VALUES ($1::uuid, $2::uuid, 'gw-1', now() - interval '1 day')`, nodeID, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes, cost_minor)
		VALUES (CURRENT_DATE - 1, $1::uuid, 2147483648, 129)`, nodeID); err != nil {
		t.Fatal(err)
	}
	code, out = panelCall(t, app, http.MethodGet, "/v1/panel/usage?days=30", key, nil)
	if code != http.StatusOK {
		t.Fatalf("usage historico: %d", code)
	}
	hist, _ := json.Marshal(out["history"])
	var days []map[string]any
	if json.Unmarshal(hist, &days) != nil || len(days) < 1 {
		t.Fatalf("historico vacio: %v", out["history"])
	}

	// Cuota: los 3 campos POST-ables se reflejan en quotas.
	code, out = panelCall(t, app, http.MethodPatch, "/v1/panel/quotas", key,
		[]byte(`{"gb_allowance": 60, "concurrent_limit": 20, "mode": "sticky"}`))
	if code != http.StatusOK {
		t.Fatalf("patch cuota: %d %v", code, out)
	}
	var gbAllow float64
	var concurrent int
	var mode string
	if err := db.QueryRowContext(ctx, `
		SELECT gb_allowance, concurrent_limit,
		       (SELECT mode FROM api_keys WHERE tenant_id = $1::uuid ORDER BY api_key_id LIMIT 1)
		  FROM quotas WHERE tenant_id = $1::uuid AND period_start = $2::date`, tenantID, period).
		Scan(&gbAllow, &concurrent, &mode); err != nil {
		t.Fatal(err)
	}
	if gbAllow != 60 || concurrent != 20 {
		t.Fatalf("cuota no reflejada: gb=%v concurrent=%v", gbAllow, concurrent)
	}

	// Factura del periodo con stripe_customer_id del tenant.
	code, out = panelCall(t, app, http.MethodGet, "/v1/panel/invoices", key, nil)
	if code != http.StatusOK {
		t.Fatalf("invoices: %d %v", code, out)
	}
	if out["stripe_customer_id"] != "cus_panel_test" {
		t.Fatalf("stripe_customer_id: %v", out["stripe_customer_id"])
	}
	inv, _ := json.Marshal(out["invoices"])
	var invDays []map[string]any
	if json.Unmarshal(inv, &invDays) != nil || len(invDays) < 1 {
		t.Fatalf("facturas del periodo vacias: %v", out["invoices"])
	}
}
