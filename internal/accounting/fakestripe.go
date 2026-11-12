// FakeStripe es el doble HTTP local de EP-10 (encargo: "doble HTTP
// local que firma webhooks con el mismo esquema, sin red externa"):
// sirve la pata Stripe de la conciliacion (GB facturado por dia) y
// emite invoice.paid firmados con HMAC-SHA256 — el MISMO codigo de
// firma que verifica el backend. No hay red externa: se levanta con
// httptest.Server (o http.Server propio) y se llama por HTTP.
package accounting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// FakeStripeConfig: el fixture de facturacion por dia (GB) y el
// destino del webhook (el /v1/billing/webhook del backend doble).
type FakeStripeConfig struct {
	Secret string
	// BilledGBByDay: GB que "Stripe" facturo por dia (YYYY-MM-DD).
	BilledGBByDay map[string]float64
	// NodeIDsByDay: nodos cubiertos por la factura del dia (para
	// marcar usage.reconciled).
	NodeIDsByDay map[string][]string
	Now          func() time.Time
}

// ServeHTTP es la cara del doble:
//
//	GET /v1/invoices?day=YYYY-MM-DD → {"gb":..,"day":..,"node_ids":[..],"invoice_id":"in_fake_<day>"}
//	POST /deliver?target=http://127.0.0.1:PORT/v1/billing/webhook
//	     → firma invoice.paid del dia y lo entrega por HTTP al target.
//
// El backend verifica con VerifyWebhook y la MISMA constante de
// cabecera: el contrato es el esquema, no el proveedor.
type FakeStripe struct {
	cfg FakeStripeConfig
}

// NewFakeStripe valida config minima.
func NewFakeStripe(cfg FakeStripeConfig) *FakeStripe { return &FakeStripe{cfg: cfg} }

func (f *FakeStripe) now() time.Time {
	if f.cfg.Now == nil {
		return time.Now()
	}
	return f.cfg.Now()
}

func (f *FakeStripe) invoiceBody(day string) ([]byte, error) {
	return json.Marshal(InvoiceEvent{
		Type:      "invoice.paid",
		Day:       day,
		GB:        f.cfg.BilledGBByDay[day],
		NodeIDs:   f.cfg.NodeIDsByDay[day],
		InvoiceID: "in_fake_" + day,
		EventID:   "evt_fake_" + day,
	})
}

// ServeHTTP implementa el contrato del doble.
func (f *FakeStripe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/invoices":
		day := r.URL.Query().Get("day")
		if day == "" {
			http.Error(w, "fake stripe: day requerido", http.StatusBadRequest)
			return
		}
		body, err := f.invoiceBody(day)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	case r.Method == http.MethodPost && r.URL.Path == "/deliver":
		day := r.URL.Query().Get("day")
		target := r.URL.Query().Get("target")
		tgt, err := url.Parse(target)
		if err != nil || tgt.Scheme == "" {
			http.Error(w, "fake stripe: target http requerido", http.StatusBadRequest)
			return
		}
		body, err := f.invoiceBody(day)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set(WebhookSignatureHeader, SignWebhook(f.cfg.Secret, f.now(), body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, fmt.Sprintf("fake stripe: deliver: %v", err), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	default:
		http.NotFound(w, r)
	}
}

// GBForDay es la pata Stripe de jobs.RunBillingDay sobre este doble
// (mismo HTTP local; sin red externa).
func (f *FakeStripe) GBForDay(day time.Time) (float64, error) {
	return f.cfg.BilledGBByDay[day.UTC().Format("2006-01-02")], nil
}

var _ http.Handler = (*FakeStripe)(nil)
