// Test T-1104 (EP-11): webhook Stripe firmado e idempotencia por
// event.id. Sin firma 400; firma forjada (un byte alterado) 403;
// firma valida de invoice.paid marca la cuota del dia; y la
// re-entrega del mismo evento no cambia la suma dos veces.
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/accounting"
	"github.com/botnet/botnet/internal/backend/testenv"
)

const webhookSecret = "whsec_test_ep11_0123456789"

func webhookPost(t *testing.T, app *fiber.App, body []byte, header string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/billing/webhook", strings.NewReader(string(body)))
	if header != "" {
		req.Header.Set(accounting.WebhookSignatureHeader, header)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestWebhookFirmaEIdempotencia(t *testing.T) {
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	day := time.Now().UTC().Format("2006-01-02")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO usage (day, node_id, bytes) VALUES ($2::date, $1::uuid, 7000000000)`, nodeID, day); err != nil {
		t.Fatal(err)
	}
	ev := accounting.InvoiceEvent{
		Type: "invoice.paid", Day: day, GB: 7,
		NodeIDs: []string{nodeID}, InvoiceID: "in_fake_" + day, EventID: "evt_fake_" + day,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	hdr := accounting.SignWebhook(webhookSecret, time.Now().UTC(), body)
	app := NewApp(&Deps{Repo: repo.New(db), RDB: rdb, StripeSecret: webhookSecret})

	// AC1a: sin firma → 400.
	if code, _ := webhookPost(t, app, body, ""); code != http.StatusBadRequest {
		t.Fatalf("sin firma: quiero 400, tengo %d", code)
	}
	// AC1b: firma forjada (se altera un hex del v1) → 403.
	i := strings.Index(hdr, "v1=")
	forged := hdr[:i+4] + flipHex(hdr[i+4]) + hdr[i+5:]
	if forged == hdr {
		t.Fatal("el byte forjado no cambio el header")
	}
	if code, _ := webhookPost(t, app, body, forged); code != http.StatusForbidden {
		t.Fatalf("firma forjada: quiero 403, tengo %d", code)
	}
	// AC1c: firma valida → 200 y cuota marcada (usage.reconciled).
	code, out := webhookPost(t, app, body, hdr)
	if code != http.StatusOK {
		t.Fatalf("firma valida: quiero 200, tengo %d", code)
	}
	if marked, _ := out["marked"].(float64); marked != 1 {
		t.Fatalf("usage marcado: %v", out["marked"])
	}
	var reconciled bool
	if err := db.QueryRowContext(ctx, `
		SELECT reconciled FROM usage WHERE day = $2::date AND node_id = $1::uuid`, nodeID, day).Scan(&reconciled); err != nil {
		t.Fatal(err)
	}
	if !reconciled {
		t.Fatal("la cuota del dia no quedo reconciled")
	}
	// AC3: re-entrega del mismo event.id → suma invariante (marked=0).
	code, out = webhookPost(t, app, body, hdr)
	if code != http.StatusOK {
		t.Fatalf("re-entrega: quiero 200, tengo %d", code)
	}
	if dup, _ := out["duplicate"].(string); dup != ev.EventID {
		t.Fatalf("re-entrega no reconocida como duplicate: %v", out)
	}
	var markedCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM usage WHERE reconciled AND day = $1::date`, day).Scan(&markedCount); err != nil {
		t.Fatal(err)
	}
	if markedCount != 1 {
		t.Fatalf("la re-entrega movio la cuota dos veces: %d filas marcadas", markedCount)
	}
}

func flipHex(b byte) string {
	if b == '0' {
		return "1"
	}
	return string('0')
}
