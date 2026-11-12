package main

import (
	"encoding/json"
	"testing"
	"time"
)

// AC1: default del contrato — `mode` ausente en POST /api/v1/sessions es
// rotating (el modo rotativo con TTL es default, exigencia de A1).
func TestT1308ContractDefaultsToRotating(t *testing.T) {
	var r SessionRequest
	if err := json.Unmarshal([]byte(`{"tenant_id":"t1","concurrent_limit":4}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeRotating {
		t.Fatalf("mode ausente: %q, want rotating", r.Mode)
	}
	if r.TTL != DefaultRotationTTL {
		t.Fatalf("ttl ausente: %v, want %v", r.TTL, DefaultRotationTTL)
	}
	if err := json.Unmarshal([]byte(`{"tenant_id":"t2","mode":"sticky","ttl_s":900}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.Mode != ModeSticky || r.TTL != 15*time.Minute {
		t.Fatalf("sticky explicito: %+v", r)
	}
	if err := json.Unmarshal([]byte(`{"mode":""}`), &r); err != nil || r.Mode != ModeRotating {
		t.Fatalf("mode vacio: %q err=%v, want rotating", r.Mode, err)
	}
	if err := json.Unmarshal([]byte(`{"mode":"pin-to-node"}`), &r); err == nil {
		t.Fatal("mode fuera de enum debe rechazarse")
	}
}

// AC2: nodo caliente (> 150 sesiones/dia) queda fuera de seleccion 24 h:
// fixture de quemado temporal con count de selecciones = 0 en la ventana,
// y vuelta a la elegibilidad al vencer (solo en la ventana).
func TestT1308HotNodeBurnedFor24h(t *testing.T) {
	tracker := NewBurnTracker(NewHotNodePolicy())
	pool := NewRotatingPool(tracker, "n-hot")
	day1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	burnedAt := time.Time{}
	for i := 0; i < 151; i++ {
		tt := day1.Add(time.Duration(i) * 30 * time.Second)
		if tracker.Record("n-hot", tt) {
			burnedAt = tt
		}
	}
	if burnedAt.IsZero() {
		t.Fatal("151 sesiones/dia no quemaron el nodo")
	}
	// Ventana de quemado: 0 selecciones.
	selected := 0
	for h := 1; h <= 23; h++ {
		if pool.Select("tenant-x", burnedAt.Add(time.Duration(h)*time.Hour), DefaultRotationTTL) != "" {
			selected++
		}
	}
	if selected != 0 {
		t.Fatalf("selecciones dentro de la ventana de quemado: %d, want 0", selected)
	}
	// A las 24 h+ vuelve: el quemado es temporal.
	after := burnedAt.Add(24*time.Hour + time.Minute)
	if !tracker.Eligible("n-hot", after) {
		t.Fatalf("nodo sigue quemado tras vencer la ventana (coolOff=%v)", tracker.CoolOffUntil("n-hot"))
	}
	if got := pool.Select("tenant-x", after, DefaultRotationTTL); got != "n-hot" {
		t.Fatalf("tras el quemado: Select = %q, want n-hot", got)
	}
}

// AC3: en la simulacion mixta 70/30 un tenant sticky puede superar su
// concurrent_limit sin degradar el p99 del rotativo: p99 < 1.500 ms se
// mantiene. (El assert psql real sobre tenants sticky es del soak; aqui
// se aserta el modelo del gateway.)
func TestT1308MixedSimKeepsP99Under1500ms(t *testing.T) {
	m := NewLatencyModel()
	for _, over := range []int{1, 4, 6} {
		p99 := m.MixedLatencySim(2000, 8, over, 7)
		if p99 >= 1500 {
			t.Fatalf("sticky %d sobre el limite: p99 = %.0f ms >= 1500 ms", over, p99)
		}
		t.Logf("sticky +%d sobre concurrent_limit: p99 = %.0f ms (carga rotativa topada a 8 tenants)", over, p99)
	}
}
