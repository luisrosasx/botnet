package accounting

import (
	"testing"
	"time"
)

// TestWebhookRoundTrip — el doble HTTP local firma y verifica con el
// MISMO codigo: lo firmado hoy pasa; el cuerpo alterado, no.
func TestWebhookRoundTrip(t *testing.T) {
	const secret = "whsec_test_local"
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	body := []byte(`{"type":"invoice.paid","day":"2026-11-14","gb":12.5,"node_ids":["a","b"],"invoice_id":"in_1"}`)

	header := SignWebhook(secret, now, body)
	signedAt, err := VerifyWebhook(secret, header, body, now)
	if err != nil {
		t.Fatalf("verificacion valida: %v", err)
	}
	if !signedAt.Equal(now) {
		t.Fatalf("timestamp firmado %v != %v", signedAt, now)
	}

	tampered := []byte(`{"type":"invoice.paid","day":"2026-11-14","gb":99.0,"node_ids":["a","b"],"invoice_id":"in_1"}`)
	if _, err := VerifyWebhook(secret, header, tampered, now); err == nil {
		t.Fatal("cuerpo alterado paso la firma")
	}
	if _, err := VerifyWebhook("otro-secreto", header, body, now); err == nil {
		t.Fatal("secreto equivocado paso la firma")
	}
}

// TestWebhookReplay — la ventana de replay: un t viejo de 10 min se
// rechaza aunque la firma sea buena.
func TestWebhookReplay(t *testing.T) {
	const secret = "whsec_test_local"
	old := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	body := []byte(`{"type":"invoice.paid","day":"2026-11-14"}`)
	header := SignWebhook(secret, old, body)
	_, err := VerifyWebhook(secret, header, body, old.Add(10*time.Minute))
	if err == nil {
		t.Fatal("replay de 10 min paso la ventana")
	}
	if _, err := VerifyWebhook(secret, header, body, old.Add(4*time.Minute)); err != nil {
		t.Fatalf("4 min de antiguedad debe pasar: %v", err)
	}
}

// TestInvoiceEventDecode — el invoice sin day no es aplicable; los
// tipos no facturable se acusan de recibido (el handler lo resuelve).
func TestInvoiceEventDecode(t *testing.T) {
	ev, err := DecodeInvoiceEvent([]byte(`{"type":"invoice.paid","day":"2026-11-14","gb":3.25,"node_ids":["n1"],"invoice_id":"in_9"}`))
	if err != nil || ev.Type != "invoice.paid" || ev.GB != 3.25 || len(ev.NodeIDs) != 1 {
		t.Fatalf("decode: %+v err=%v", ev, err)
	}
	if _, err := DecodeInvoiceEvent([]byte(`{"type":"invoice.created"}`)); err == nil {
		t.Fatal("invoice sin day debe rechazarse")
	}
}
