package main

import (
	"testing"
)

// AC1: tope tecnico por clase desde device_class — A 150, B 100, C 64
// (tabla versionada); fixture: nodo clase C rechaza el stream 65.
func TestClassCapsTableAndFixture(t *testing.T) {
	p := NewAdmissionPolicy()
	if p.Version != AdmissionTableVersion {
		t.Fatalf("tabla versionada: version %d", p.Version)
	}
	for class, want := range map[byte]int{'A': 150, 'B': 100, 'C': 64} {
		if got, ok := ClassCap(class); !ok || got != want {
			t.Fatalf("clase %c: %d ok=%v (queremos %d)", class, got, ok, want)
		}
	}
	if _, ok := ClassCap('D'); ok {
		t.Fatal("clase desconocida: no admisible")
	}
	// fixture AC1: clase C con cuota holgada — los 64 pasan, el 65 no.
	for s := 0; s < 64; s++ {
		if ok, r := p.Admit('C', s, 150, 1, false); !ok {
			t.Fatalf("clase C stream %d rechazado: %s", s+1, r)
		}
	}
	if ok, r := p.Admit('C', 64, 150, 1, false); ok || r != AdmitClassCap {
		t.Fatalf("stream 65 en clase C: ok=%v reason=%s (class_cap)", ok, r)
	}
}

// AC2: equidad — 3 tenants de 300 sesiones sobre 1.000 nodos no dejan al
// 4.º sin admision durante 10 min (pool_starved = 0). El tope de 8 fijo
// de P2 no se repite como dogma: un solo tenant ocupa > 8 streams.
func TestFairnessPoolStarvedZero(t *testing.T) {
	p := NewAdmissionPolicy()
	nodes := make([]nodeState, 1000)
	for i := range nodes {
		switch {
		case i < 600:
			nodes[i].class = 'A'
		case i < 900:
			nodes[i].class = 'B'
		default:
			nodes[i].class = 'C'
		}
		nodes[i].tenants = map[string]int{}
	}
	// Los 3 tenants grandes reparten 300 sesiones round-robin por el
	// pool — el gateway ya las coloco; aqui se aserta la admision.
	for tn := 1; tn <= 3; tn++ {
		name := string(rune('A' + tn))
		for s := 0; s < 300; s++ {
			idx := (tn-1)*300 + s
			if ok, r := nodes[idx%1000].admit(p, name); !ok {
				t.Fatalf("tenant %d sesion %d: %s (pool_starved en los grandes)", tn, s+1, r)
			}
		}
	}
	// El 4.º tenant pide 20 sesiones por minuto durante 10 min.
	starved := 0
	for m := 0; m < 10; m++ {
		admitted := 0
		for s := 0; s < 20; s++ {
			idx := (m*20 + s) % 1000
			if ok, _ := nodes[idx].admit(p, "D"); ok {
				admitted++
			}
		}
		if admitted == 0 {
			starved++
		}
	}
	if starved != 0 {
		t.Fatalf("pool_starved: %d min de 10 (exigido 0)", starved)
	}
	// Nada de dogma de 8: el tenant grande tiene ~3 sesiones/nodo de
	// media y el 4.º convive; un tenant aislado pasa de 8 con holgura.
	var solo nodeState
	solo.class = 'A'
	solo.tenants = map[string]int{}
	for s := 0; s < 50; s++ {
		if ok, r := solo.admit(p, "X"); !ok {
			t.Fatalf("tenant unico stream %d en clase A: %s (el tope de 8 no es cuota por tenant)", s+1, r)
		}
	}
}

// nodeState es el doble del nodo: clase + streams por tenant + tenants
// activos distintos.
type nodeState struct {
	class   byte
	tenants map[string]int // tenant -> streams activos
}

func (n *nodeState) admit(p *AdmissionPolicy, tenant string) (bool, AdmissionReason) {
	tenants := len(n.tenants)
	streams := n.tenants[tenant]
	ok, r := p.Admit(n.class, streams, 10_000, tenants, streams == 0)
	if ok {
		n.tenants[tenant]++
	}
	return ok, r
}

// AC3: la cuota concurrente del tenant (quotas.concurrent_limit) y el
// tope de <= 8 tenants mandan por encima del tope de clase donde el min
// manda (assert combinado).
func TestTenantQuotaAndFanoutDominate(t *testing.T) {
	p := NewAdmissionPolicy()
	// Cuota 5 en clase A (150): el min manda y el 6.º cae por cuota.
	for s := 0; s < 5; s++ {
		if ok, _ := p.Admit('A', s, 5, 1, false); !ok {
			t.Fatalf("cuota 5: stream %d rechazado antes de tiempo", s+1)
		}
	}
	if ok, r := p.Admit('A', 5, 5, 1, false); ok || r != AdmitTenantQuota {
		t.Fatalf("stream 6 con cuota 5: ok=%v reason=%s", ok, r)
	}
	// 9.º tenant sobre nodo con 8 activos: cae por abanico aunque la
	// clase tenga hueco de sobra.
	if ok, r := p.Admit('A', 1, 150, MaxActiveTenantsPerNode, true); ok || r != AdmitTenantFan {
		t.Fatalf("tenant 9: ok=%v reason=%s (tenant_fanout)", ok, r)
	}
	// Un tenant YA activo en el nodo no esta topado por el 8.
	if ok, _ := p.Admit('A', 1, 150, MaxActiveTenantsPerNode, false); !ok {
		t.Fatal("tenant activo debe seguir entrando con 8 distintos")
	}
	// Combinado: clase B (100), cuota 200 → manda la clase en el 101.
	if ok, r := p.Admit('B', 100, 200, 2, false); ok || r != AdmitClassCap {
		t.Fatalf("B con cuota 200: stream 101 ok=%v reason=%s", ok, r)
	}
	// Combinado inverso: clase C (64), cuota 10 → manda la cuota en el 11.
	if ok, r := p.Admit('C', 10, 10, 1, false); ok || r != AdmitTenantQuota {
		t.Fatalf("C con cuota 10: stream 11 ok=%v reason=%s", ok, r)
	}
}
