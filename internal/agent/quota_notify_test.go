package agent

import "testing"

// TestQuotaNotifyGoaway — el GOAWAY tras quota.exhausted se tipa
// closed_reason='quota' (causa contable separada de 'error'/'client',
// T-0605/T-1004); el GOAWAY operativo de un tenant vivo es 'goaway'.
func TestQuotaNotifyGoaway(t *testing.T) {
	q := NewQuotaNotify()
	if q.IsExhausted("t1") {
		t.Fatal("tenant fresco no debe nacer agotado")
	}
	if r := q.ClosedReasonFor("t1"); r != "goaway" {
		t.Fatalf("tenant sin corte: closed_reason=%q, want goaway", r)
	}

	q.OnExhausted("t1")
	q.OnExhausted("t1") // reenvio del evento: idempotente
	if !q.IsExhausted("t1") {
		t.Fatal("quota.exhausted no marco el tenant")
	}
	if r := q.ClosedReasonFor("t1"); r != "quota" {
		t.Fatalf("tenant cortado: closed_reason=%q, want quota", r)
	}
	if q.ClosedReasonFor("t2") != "goaway" {
		t.Fatal("el corte de t1 no debe tocar t2")
	}

	q.Clear("t1") // pago del periodo
	if q.IsExhausted("t1") || q.ClosedReasonFor("t1") != "goaway" {
		t.Fatal("Clear no levanto el corte")
	}
}
