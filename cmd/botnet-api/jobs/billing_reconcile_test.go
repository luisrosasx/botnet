package jobs

import (
	"testing"
	"time"
)

// TestBillingReconcileOK — fixture del dia con desvios <= 0,5 % en
// sessions y Stripe contra el usage del nodo: reconciled, cero alarmas.
func TestBillingReconcileOK(t *testing.T) {
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	r := BillingFixture3Patas(day, 5_000_000_000, 0.4)
	var marks int
	var lastOK bool
	var lastDrift float64
	var alarms int
	r.Mark = func(_ time.Time, drift float64, ok bool) error {
		marks++
		lastDrift, lastOK = drift, ok
		return nil
	}
	r.Publish = func(string, []byte) error { alarms++; return nil }
	rep, err := r.RunDay(day)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Reconciled || alarms != 0 || marks != 1 || !lastOK {
		t.Fatalf("desvio %.3f%% deberia conciliar: rep=%+v marks=%d", rep.DriftPct, rep, marks)
	}
	if lastDrift > TolerancePct {
		t.Fatalf("drift medido %.3f%% > tolerancia", lastDrift)
	}
}

// TestBillingReconcileAlarm — un desvio de 0,6 % en el fixture dispara
// alarma (usage.audit + usage.reconciled=false + metrica) SIN bloquear
// el cobro: RunDay no devuelve error y el dia se factura igual.
func TestBillingReconcileAlarm(t *testing.T) {
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	r := BillingFixture3Patas(day, 5_000_000_000, 0.6)
	var alarms int
	var marked *bool
	var drift float64
	r.Mark = func(_ time.Time, d float64, ok bool) error {
		marked = &ok
		drift = d
		return nil
	}
	r.Publish = func(subject string, payload []byte) error {
		if subject != UsageAuditSubject {
			t.Fatalf("alarma fuera del tema de auditoria: %q", subject)
		}
		alarms++
		return nil
	}
	rep, err := r.RunDay(day)
	if err != nil {
		t.Fatalf("la alarma no debe bloquear el cobro (err=%v)", err)
	}
	if rep.Reconciled {
		t.Fatalf("0,6%% de desvio paso como reconciled: drift=%.3f", drift)
	}
	if alarms != 1 {
		t.Fatalf("alarma publicada %d veces, want 1", alarms)
	}
	if marked == nil || *marked {
		t.Fatal("Mark no recibio reconciled=false (usage.reconciled sigue el veredicto)")
	}
	if drift <= TolerancePct {
		t.Fatalf("el drift maximo de las tres patas debe superar la tolerancia: %.3f", drift)
	}
}

// TestBillingFixtureDeterminism — dos pasadas del fixture dan el mismo
// veredicto: el reloj controlado del test no depende de wall-clock.
func TestBillingFixtureDeterminism(t *testing.T) {
	day := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		r := BillingFixture3Patas(day, 3_000_000_000, 0.4)
		rep, err := r.RunDay(day)
		if err != nil || !rep.Reconciled || rep.DriftPct > TolerancePct {
			t.Fatalf("pasada %d: %+v err=%v", i, rep, err)
		}
	}
}
