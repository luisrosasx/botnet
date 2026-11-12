// Test de la puerta de cuota (T-1004): el contador vivo
// tenant:{t}:gb_used:{period} contra quotas.gb_allowance; agotado,
// quota:{t}:state + NATS quota.exhausted cortan la sesion nueva en
// < 60 s medidos con reloj controlado. Requiere redis de testenv.
package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/backend/testenv"
)

type fakeQuotaPub struct{ n int }

func (p *fakeQuotaPub) PublishQuotaExhausted(string) { p.n++ }

func TestQuotaCutUnder60s(t *testing.T) {
	rdb := testenv.Redis(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	clock := base
	pub := &fakeQuotaPub{}
	gate := &QuotaGate{RDB: rdb, JS: pub, Now: func() time.Time { return clock }}

	// Tenant unico por ejecucion: el redis de test es compartido.
	tenant := fmt.Sprintf("quota-test-%d", time.Now().UnixNano())
	period := "2026-11-01"
	defer rdb.Del(ctx, GBUsedKey(tenant, period))

	// 9 de 10 GB: sesion nueva admitida, sin estado ni evento.
	rdb.Set(ctx, GBUsedKey(tenant, period), 9.0, time.Hour)
	d, err := gate.Check(ctx, tenant, period, 10.0)
	if err != nil || d.Exhausted {
		t.Fatalf("con 9/10 GB no debe cortar: %+v err=%v", d, err)
	}
	if pub.n != 0 {
		t.Fatalf("publico quota.exhausted sin agotar cuota")
	}

	// El contador cruza el allowance a t+45 s (cronometro controlado).
	crossAt := base.Add(45 * time.Second)
	clock = crossAt
	rdb.Set(ctx, GBUsedKey(tenant, period), 10.2, time.Hour)
	d, err = gate.Check(ctx, tenant, period, 10.0)
	if err != nil || !d.Exhausted {
		t.Fatalf("con 10.2/10 GB debe cortar: %+v err=%v", d, err)
	}
	if pub.n != 1 {
		t.Fatalf("quota.exhausted publicado %d veces, want 1", pub.n)
	}
	if !CutWithin60s(crossAt, d.CutAt) {
		t.Fatalf("el corte tardo %v (> 60 s)", d.CutAt.Sub(crossAt))
	}
	if st, _ := gate.State(ctx, tenant); st != "exhausted" {
		t.Fatalf("quota:{t}:state = %q, want exhausted", st)
	}

	// La carrera del cache de 30 s: aunque el cache siga diciendo
	// exhausted 25 s despues, sigue dentro del budget de 60 s y la
	// tolerancia se lo come; el evento NATS invalida el cache ya.
	clock = crossAt.Add(25 * time.Second)
	d2, _ := gate.Check(ctx, tenant, period, 10.0)
	if !d2.CacheState || !CutWithin60s(crossAt, clock) {
		t.Fatalf("carrera del cache fuera de budget: %+v", d2)
	}
	if err := gate.OnQuotaExhausted(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if st, _ := gate.State(ctx, tenant); st != "" {
		t.Fatalf("el evento no invalido el cache: state=%q", st)
	}

	// El budget es duro: 61 s de corte no pasan el aserto.
	if CutWithin60s(base, base.Add(61*time.Second)) {
		t.Fatal("61 s de corte no deberian pasar CutWithin60s")
	}
}

func TestQuotaStateKeyShape(t *testing.T) {
	if got := GBUsedKey("t1", "2026-11-01"); got != "tenant:t1:gb_used:2026-11-01" {
		t.Fatalf("clave de contador: %q", got)
	}
	if got := QuotaStateKey("t1"); got != "quota:t1:state" {
		t.Fatalf("clave de estado: %q", got)
	}
}
