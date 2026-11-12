package jobs

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Criterio 1: fixture de 24 h con desvío objetivo 0,4 % -> 0 horas en rojo
// (umbral 0,5 % cumplido en las 24).
func TestFixture24hUnderTolerance(t *testing.T) {
	deltas, usage, _ := Fixture24h(0.4, 10)
	var alarms int
	r := &Reconciler{
		Deltas: deltas, Usage: usage,
		Publish: func(string, []byte) error { alarms++; return nil },
	}
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	reps, red, err := r.RunHours(base, 24)
	if err != nil {
		t.Fatal(err)
	}
	if red != 0 || alarms != 0 {
		t.Fatalf("horas rojas %d alarmas %d, want 0/0", red, alarms)
	}
	for _, rep := range reps {
		if rep.DriftPct > TolerancePct {
			t.Fatalf("%s drift %.3f%% > %.2f%%", rep.Hour, rep.DriftPct, TolerancePct)
		}
	}
}

// Criterio 3: hora con desvío > 0,5 % -> alarma usage.audit publicada y el
// cobro no se bloquea (RunHours termina con err nil y sigue la vuelta).
func TestRedHourPublishesAndBillingProceeds(t *testing.T) {
	deltas, usage, _ := Fixture24h(0.9, 4)
	var subjects []string
	r := &Reconciler{
		Deltas: deltas, Usage: usage,
		Publish: func(subject string, _ []byte) error {
			subjects = append(subjects, subject)
			return nil
		},
	}
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	_, red, err := r.RunHours(base, 24)
	if err != nil {
		t.Fatalf("RunHours con desvio alto: %v (el desvio no bloquea)", err)
	}
	if red == 0 {
		t.Fatal("fixture con 0,9% debio producir horas rojas")
	}
	for _, s := range subjects {
		if s != UsageAuditSubject {
			t.Fatalf("subject %q want %s", s, UsageAuditSubject)
		}
	}
}

// Publicar falla no debe truncar la vuelta horaria: RunHour propaga el error
// solo del transporte de alarma; la conciliacion ya esta hecha.
func TestPublishErrorPropagates(t *testing.T) {
	deltas, usage, _ := Fixture24h(0.9, 2)
	r := &Reconciler{Deltas: deltas, Usage: usage,
		Publish: func(string, []byte) error { return errors.New("nats down") }}
	_, _, err := r.RunHours(time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), 24)
	if err == nil || !strings.Contains(err.Error(), "nats down") {
		t.Fatalf("want nats down, got %v", err)
	}
}

// Criterio 2 (cero bytes extra): el job solo invoca las dos lecturas por
// hora (Deltas y Usage, ambas server-side); jamas añade requests por nodo al
// agente: reads = 2/hora y 0 alarmas en el fixture verde.
func TestNoExtraAgentRequests(t *testing.T) {
	deltas, usage, reads := Fixture24h(0.4, 8)
	r := &Reconciler{Deltas: deltas, Usage: usage,
		Publish: func(string, []byte) error { return nil }}
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if _, red, err := r.RunHours(base, 24); err != nil || red != 0 {
		t.Fatalf("horas rojas %d err %v", red, err)
	}
	if got := reads(); got != 48 {
		t.Fatalf("lecturas %d, want 48 (2 por hora, cero red por nodo)", got)
	}
}

// RunFixture24h imprime la tabla y el resumen; usado por make reconcile-hourly.
func TestRunFixture24hOutput(t *testing.T) {
	var sb strings.Builder
	red, err := RunFixture24h(&sb, 0.4, 5)
	if err != nil {
		t.Fatal(err)
	}
	if red != 0 {
		t.Fatalf("horas rojas %d want 0 con drift objetivo 0.4%%", red)
	}
	out := sb.String()
	if !strings.Contains(out, "0 horas en rojo") {
		t.Fatalf("salida sin resumen: %s", firstLines(out, 3))
	}
	if strings.Contains(out, "ALARM") {
		t.Fatal("no debe haber alarmas bajo tolerancia")
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	return strings.Join(parts, "\n")
}
