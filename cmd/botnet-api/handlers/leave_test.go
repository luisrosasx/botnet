// leave_test.go — T-0706 contra Postgres portable (testenv.PG):
// codigo valido -> 200 + fila en leaves (method='http', leave_code
// char(8)); codigo equivocado -> 404; sin codigo -> 403 (el ID
// enumerable solo no basta); 61 requests con rate-limit -> >= 1 429;
// y el barrido de 1.000 IDs secuenciales no produce ningun 503 (a
// escala de los 200k curls de A3-13, pool/select sigue vivo).
package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/middleware"
	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func leaveNode(t *testing.T, code string) (string, *repo.Data) {
	t.Helper()
	db := testenv.PG(t)
	id := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET leave_code = $2::char(8) WHERE node_id = $1::uuid`, id, code); err != nil {
		t.Fatalf("sembrar leave_code: %v", err)
	}
	return id, repo.New(db)
}

func postLeave(t *testing.T, app *fiber.App, nodeID, query string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+nodeID+"/leave"+query, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLeaveCodigoValidoCreaFilaHTTP(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET leave_code = 'ABCD1234'::char(8) WHERE node_id = $1::uuid`, nodeID); err != nil {
		t.Fatal(err)
	}
	rp := repo.New(db)
	app := NewApp(&Deps{Repo: rp, LeaveLimiter: middleware.NewFixedWindow(1000, time.Minute)})

	resp := postLeave(t, app, nodeID, "?code=ABCD1234")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("codigo valido: %d, want 200", resp.StatusCode)
	}

	var method, code string
	var blocked string
	err := db.QueryRowContext(context.Background(), `
		SELECT method, RTRIM(leave_code::text), (reprobe_blocked_until > now() + interval '11 months')::text
		FROM leaves WHERE node_id = $1::uuid ORDER BY leave_id DESC LIMIT 1`, nodeID).
		Scan(&method, &code, &blocked)
	if err != nil {
		t.Fatalf("fila en leaves: %v", err)
	}
	if method != "http" || code != "ABCD1234" {
		t.Errorf("leaves: method=%q leave_code=%q, want http/ABCD1234", method, code)
	}
	if blocked != "true" {
		t.Error("reprobe_blocked_until debe quedar a >=12 meses (no re-clutaje)")
	}
	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&status); err != nil || status != "retiring" {
		t.Errorf("status del nodo tras la baja: %q err %v, want retiring (baja por pull)", status, err)
	}
}

func TestLeaveCodigoEquivocado404SinCodigo403(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET leave_code = 'ABCD1234'::char(8) WHERE node_id = $1::uuid`, nodeID); err != nil {
		t.Fatal(err)
	}
	app := NewApp(&Deps{Repo: repo.New(db), LeaveLimiter: middleware.NewFixedWindow(1000, time.Minute)})

	if resp := postLeave(t, app, nodeID, "?code=XXYYZZWW"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("codigo erroneo: %d, want 404", resp.StatusCode)
	}
	if resp := postLeave(t, app, nodeID, ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sin codigo: %d, want 403 (el ID enumerable no es la puerta)", resp.StatusCode)
	}
	if resp := postLeave(t, app, nodeID, "?code=corto"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("codigo de 5 chars: %d, want 403", resp.StatusCode)
	}
	// AC3: un id numerable "bigserial-style" sin codigo tampoco abre.
	if resp := postLeave(t, app, "00000000-0000-4000-8000-000000000042", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("id enumerable sin codigo: %d, want 403", resp.StatusCode)
	}
}

func TestLeaveRateLimitSesentaYUno(t *testing.T) {
	db := testenv.PG(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET leave_code = 'AAAA0000'::char(8) WHERE node_id = $1::uuid`, nodeID); err != nil {
		t.Fatal(err)
	}
	lim := middleware.NewFixedWindow(60, 60*time.Second)
	app := NewApp(&Deps{Repo: repo.New(db), LeaveLimiter: lim})

	got429, gotOK := 0, 0
	for i := 0; i < 61; i++ {
		resp := postLeave(t, app, nodeID, "?code=AAAA0000")
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			got429++
		case http.StatusOK:
			gotOK++
		default:
			t.Fatalf("request %d: status inesperado %d", i, resp.StatusCode)
		}
	}
	if got429 < 1 {
		t.Errorf("61 requests en ventana: %d 429, want >= 1", got429)
	}
	if gotOK != 60 {
		t.Errorf("61 requests en ventana: %d 200, want 60 admitidos", gotOK)
	}
}

func TestBarridoMilIDsSin503(t *testing.T) {
	// A3-13 a escala: 1.000 IDs secuenciales barridos (sin codigo
	// valido) dejan pool/select vivo: NI UN 503 en el barrido.
	db := testenv.PG(t)
	app := NewApp(&Deps{Repo: repo.New(db), LeaveLimiter: middleware.NewFixedWindow(1_000_000, 60*time.Second)})

	for i := 1; i <= 1000; i++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		resp := postLeave(t, app, id, "?code=ABCDEFGH")
		if resp.StatusCode == http.StatusServiceUnavailable {
			t.Fatalf("barrido %d: pool en 503 (A3-13 reviviendo)", i)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("barrido %d: %d, want 404 silencioso", i, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// S-18: el código caduca. Con TTL corto y el alta envejecida, el código
// correcto responde 403 (caducado) y no 200.
func TestLeaveCodeCaduca(t *testing.T) {
	db := testenv.PG(t)
	id := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(context.Background(),
		`UPDATE nodes SET leave_code = 'caduca01'::char(8),
		 enrolled_at = now() - interval '30 days' WHERE node_id = $1::uuid`, id); err != nil {
		t.Fatal(err)
	}
	rp := repo.New(db)
	app := NewApp(&Deps{Repo: rp, LeaveCodeTTL: 7 * 24 * time.Hour})
	resp := postLeave(t, app, id, "?code=caduca01")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("código caducado = %d, want 403", resp.StatusCode)
	}

	// Con TTL desactivado (0) el mismo código vuelve a valer.
	app2 := NewApp(&Deps{Repo: rp})
	resp2 := postLeave(t, app2, id, "?code=caduca01")
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("sin TTL = %d, want 200", resp2.StatusCode)
	}
}

// S-20: con PanelOrigins el preflight del navegador pasa; sin él, no hay
// cabecera CORS (comportamiento previo intacto).
func TestCORSConPanelOrigin(t *testing.T) {
	app := NewApp(&Deps{PanelOrigins: "http://127.0.0.1:3100"})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://127.0.0.1:3100")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://127.0.0.1:3100" {
		t.Fatalf("ACAO = %q, want el origen del panel", got)
	}

	app2 := NewApp(&Deps{})
	req2 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req2.Header.Set("Origin", "http://127.0.0.1:3100")
	resp2, err := app2.Test(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("sin PanelOrigins no debe haber CORS")
	}
}
