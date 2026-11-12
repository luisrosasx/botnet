package accounting

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestFakeStripeDeliver — el doble HTTP local firma y entrega por
// HTTP el invoice.paid; el "backend" lo verifica con VerifyWebhook y
// el mismo esquema: sin red externa, contrato real.
func TestFakeStripeDeliver(t *testing.T) {
	const secret = "whsec_fake"
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	fake := NewFakeStripe(FakeStripeConfig{
		Secret:        secret,
		BilledGBByDay: map[string]float64{"2026-11-14": 42.5},
		NodeIDsByDay:  map[string][]string{"2026-11-14": {"n1", "n2"}},
		Now:           func() time.Time { return day },
	})
	fakeSrv := httptest.NewServer(fake)
	defer fakeSrv.Close()

	var verified, applied int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if _, err := VerifyWebhook(secret, r.Header.Get(WebhookSignatureHeader), body, day); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		verified++
		ev, err := DecodeInvoiceEvent(body)
		if err != nil || ev.Type != "invoice.paid" {
			http.Error(w, "evento no aplicable", http.StatusBadRequest)
			return
		}
		applied++
		w.Write([]byte(`{"received":true}`))
	}))
	defer backend.Close()

	resp, err := http.Post(fakeSrv.URL+"/deliver?day=2026-11-14&target="+backend.URL+"/v1/billing/webhook", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deliver: %d %s", resp.StatusCode, body)
	}
	if verified != 1 || applied != 1 {
		t.Fatalf("verificadas %d, aplicadas %d", verified, applied)
	}
}
