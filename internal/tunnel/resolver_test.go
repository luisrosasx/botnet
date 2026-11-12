package tunnel

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestResolverBasicResolve(t *testing.T) {
	zone := map[string][]string{"api.botnet.example": {"203.0.113.10:443"}}
	r := NewResolver("api.botnet.example",
		func(host string) ([]string, error) { return zone[host], nil },
		6*time.Hour, time.Hour, "node-1")
	dest, err := r.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if dest != "203.0.113.10:443" {
		t.Fatalf("dest %q", dest)
	}
	// TTL + jitter acotado por Jitter: la expiracion jamas supera TTL+Jitter
	// (criterio 2: peor caso 24 h con TTL 23 h + jitter 1 h).
	if d := r.Expiry().Sub(r.Now()); d < r.TTL || d > r.TTL+r.Jitter {
		t.Fatalf("expiracion TTL+jitter=%v fuera de [%v, %v]", d, r.TTL, r.TTL+r.Jitter)
	}
}

// Criterio 2: cambio de registro DNS -> el 100 % de los nodos simulados
// re-asignan destino en <= TTL + jitter (reloj acelerado), sin reentregar
// binario (la "version" del agente no cambia jamas en este test).
func TestResolverZoneChangeReassignsAllNodes(t *testing.T) {
	const nodes = 100
	oldIPs := []string{"203.0.113.10:443", "203.0.113.11:443", "203.0.113.12:443"}
	newIPs := []string{"198.51.100.7:443", "198.51.100.8:443"}
	var zoneNow int
	look := func(string) ([]string, error) {
		if zoneNow == 0 {
			return oldIPs, nil
		}
		return newIPs, nil
	}
	cur := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	ttl, jit := 23*time.Hour, time.Hour // peor caso del criterio: <= 24 h
	rs := make([]*Resolver, 0, nodes)
	for i := 0; i < nodes; i++ {
		r := NewResolver("api.botnet.example", look, ttl, jit, fmt.Sprintf("node-%03d", i))
		r.Now = func() time.Time { return cur }
		if _, err := r.Resolve(); err != nil {
			t.Fatal(err)
		}
		rs = append(rs, r)
	}
	// El registro cambia a mitad de ventana. El deadline es TTL + jitter max
	// desde el primer resolve (peor caso 24 h) + 10 min de granularidad del
	// reloj acelerado.
	cur = cur.Add(2 * time.Hour)
	zoneNow = 1
	deadline := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC).
		Add(ttl).Add(jit).Add(10 * time.Minute)
	isNew := func(dest string) bool {
		for _, ip := range newIPs {
			if dest == ip {
				return true
			}
		}
		return false
	}
	for step := 0; ; step++ {
		for _, r := range rs {
			if cur.After(r.Expiry()) {
				if _, err := r.Resolve(); err != nil {
					t.Fatal(err)
				}
			}
		}
		allNew := true
		for _, r := range rs {
			if !isNew(r.Dest()) {
				allNew = false
			}
		}
		if allNew {
			if cur.After(deadline) {
				t.Fatalf("re-asignacion total a las %v > deadline %v", cur, deadline)
			}
			return
		}
		cur = cur.Add(10 * time.Minute)
		if cur.After(deadline.Add(time.Hour)) {
			t.Fatalf("no re-asingaron el 100%% en <= TTL+jitter")
		}
	}
}

// Criterio 3: resolver caído conserva el último destino válido y reintenta
// en < 5 min; sin destino previo devuelve error (nunca panic).
func TestResolverDownKeepsStaleAndRetries(t *testing.T) {
	cur := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	dnsErr := errors.New("dns refused")
	healthy := true
	look := func(string) ([]string, error) {
		if !healthy {
			return nil, dnsErr
		}
		return []string{"203.0.113.10:443"}, nil
	}
	r := NewResolver("api.botnet.example", look, 30*time.Minute, time.Minute, "node-9")
	r.Now = func() time.Time { return cur }
	if _, err := r.Resolve(); err != nil {
		t.Fatal(err)
	}
	healthy = false
	cur = cur.Add(time.Hour) // destino caducado y DNS caido
	dest, err := r.Resolve()
	if err != nil {
		t.Fatalf("con destino previo conservado no hay error: %v", err)
	}
	if dest != "203.0.113.10:443" {
		t.Fatalf("debia conservar el ultimo destino valido, got %q", dest)
	}
	if retry := r.RetryIn(); retry >= 5*time.Minute {
		t.Fatalf("reintento a los %v, want < 5 min", retry)
	}
	// Arranque en frio sin DNS: error propagado, cero panico.
	r2 := NewResolver("api.botnet.example", look, time.Hour, 0, "node-x")
	r2.Now = func() time.Time { return cur }
	_, errCold := r2.Resolve()
	if !errors.Is(errCold, ErrNoDNS) {
		t.Fatalf("arranque sin DNS: err %v, want ErrNoDNS", errCold)
	}
	if strings.Contains(errCold.Error(), "resolver fatal") {
		t.Fatalf("el log jamas debe decir 'resolver fatal': %v", errCold)
	}
}
