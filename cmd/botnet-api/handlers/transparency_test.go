// Test T-1101 (EP-11): la pagina publica GET /t/{slug} es compartible,
// medible y sin fugas — flota, modelo, tope de ancho, GB del mes,
// consentimiento fechado y baja con leave_code; y rate-limit por IP
// 60/60 s igual que la puerta de baja (A3-13).
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/botnet/botnet/cmd/botnet-api/middleware"
	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestTransparencyCardContent(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	var slug string
	if err := db.QueryRowContext(ctx, `SELECT slug FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE nodes SET leave_code = 'ABCD1234', bandwidth_cap_kbps = 3000, model = 'TP-Link ER60' WHERE node_id = $1::uuid`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes)
		VALUES (CURRENT_DATE, $1::uuid, 5000000000)`, nodeID); err != nil {
		t.Fatal(err)
	}
	app := NewApp(&Deps{Repo: repo.New(db)})

	// La JSON solo asoma si se pide explicitamente (el HTML es el
	// defectivo): el panel Next manda Accept: application/json.
	req := httptest.NewRequest(http.MethodGet, "/t/"+slug, nil)
	req.Header.Set("Accept", "application/json")
	resp, err := app.Test(req, 5000) // 5 s: el PG portable bajo carga pasa del defectivo de Fiber (1 s)
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
	// 5 asserts de contenido: flota, modelo, tope de ancho, GB del
	// mes, consentimiento fechado y enlace de baja con leave_code.
	if page["status"] != "live" {
		t.Fatalf("flota: %v", page["status"])
	}
	if mo, _ := page["model"].(string); mo != "TP-Link ER60" {
		t.Fatalf("modelo: %q", page["model"])
	}
	if page["bandwidth_cap_kbps"] != float64(3000) {
		t.Fatalf("tope de ancho: %v", page["bandwidth_cap_kbps"])
	}
	if gb, _ := page["gb_month"].(float64); gb < 5.0 {
		t.Fatalf("GB del mes: %v", page["gb_month"])
	}
	if d, _ := page["consent_date"].(string); !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(d) {
		t.Fatalf("consentimiento sin fecha: %v", page["consent_date"])
	}
	if code, _ := page["leave_code"].(string); !regexp.MustCompile(`^[A-Za-z0-9]{8}$`).MatchString(code) {
		t.Fatalf("leave_code: %q", page["leave_code"])
	}
	// La credencial nunca asoma: ni el device_token en claro ni su hash.
	raw, _ := json.Marshal(page)
	for _, leak := range []string{"device_token", "token_hash"} {
		if strings.Contains(strings.ToLower(string(raw)), leak) {
			t.Fatalf("fuga de credencial en la pagina: %s en %s", leak, raw)
		}
	}
	// Slug invalido (no 12 alfanum) y slug desconocido: 404 seco.
	for _, bad := range []string{"/t/corto", "/t/zzzzzzzzzzzz"} {
		r, err := app.Test(httptest.NewRequest(http.MethodGet, bad, nil))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: queria 404, tuve %d", bad, r.StatusCode)
		}
	}
}

func TestTransparencyRateLimit(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	var slug string
	if err := db.QueryRowContext(context.Background(),
		`SELECT slug FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	lim := middleware.NewFixedWindow(middleware.LeaveRateLimitPerWindow, middleware.LeaveWindow)
	app := NewApp(&Deps{Repo: repo.New(db), TransparentLimiter: lim})

	var codes [61]int
	for i := range codes {
		// 5 s por golpe: el defectivo de Fiber son 1000 ms y el PG
		// portable bajo Docker/Windows los roza en serie; el limite
		// que se prueba es de peticiones, no de milisegundos.
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/t/"+slug, nil), 5000)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		codes[i] = resp.StatusCode
	}
	var n429 int
	for _, s := range codes {
		if s == http.StatusTooManyRequests {
			n429++
		}
	}
	// AC3: 61 requests en la ventana → >= 1 429 (y 60 x 200).
	if n429 < 1 {
		t.Fatalf("61 requests sin ningun 429: %v", codes)
	}
	if got := codes[0]; got != http.StatusOK {
		t.Fatalf("primer golpe: %d", got)
	}
	if codes[59] != http.StatusOK {
		t.Fatalf("golpe 60 debia pasar: %d", codes[59])
	}
	if codes[60] != http.StatusTooManyRequests {
		t.Fatalf("golpe 61 debia 429: %d", codes[60])
	}
}
