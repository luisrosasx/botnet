package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testAssigner(t *testing.T) *Assigner {
	t.Helper()
	return NewAssigner([]string{"192.0.2.10:443", "198.51.100.20:443"}, 900)
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// getJSON pide /rdv/assign. Reintenta el dial: httptest anuncia la URL
// antes de que el listener acepte de forma fiable y en loopback Windows
// la primera conexión se cae (dial/wsarecv) con una sola tentativa.
func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Get(url + "/rdv/assign")
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("assign tras 20 intentos: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("JSON invalido: %v (%s)", err, body)
	}
	return m
}

// T-0402 AC1: JSON {gateway_addr, session_token, ttl_s} EXACTAS 3 claves.
func TestAssignJSONExactThreeKeys(t *testing.T) {
	a := testAssigner(t)
	srv := httptest.NewServer(newAssignHandler(a, mustGeo(t), quietLog()))
	defer srv.Close()
	m := getJSON(t, srv.URL)
	if len(m) != 3 {
		t.Fatalf("claves = %d (%v), want exactas 3", len(m), m)
	}
	for _, k := range []string{"gateway_addr", "session_token", "ttl_s"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("falta la clave %q en %v", k, m)
		}
	}
	gw, _ := m["gateway_addr"].(string)
	if gw == "" || !strings.Contains(gw, ":443") {
		t.Fatalf("gateway_addr = %q", gw)
	}
	tok, _ := m["session_token"].(string)
	if len(tok) != 24 { // 12 B en hex
		t.Fatalf("session_token = %q, want 24 hex", tok)
	}
	if ttl, _ := m["ttl_s"].(float64); ttl != 900 {
		t.Fatalf("ttl_s = %v, want 900", ttl)
	}
}

// T-0402 AC1 (round-robin): 2 instancias alternadas (indice compartido
// via Redis en produccion; aqui un Assigner detras de 2 servers)
// devuelven gateways distintos en calls consecutivos — assert con 10.
func TestRoundRobinTwoInstances(t *testing.T) {
	a := testAssigner(t)
	s1 := httptest.NewServer(newAssignHandler(a, mustGeo(t), quietLog()))
	defer s1.Close()
	s2 := httptest.NewServer(newAssignHandler(a, mustGeo(t), quietLog()))
	defer s2.Close()

	var got []string
	for i := 0; i < 10; i++ {
		srv := s1
		if i%2 == 1 {
			srv = s2
		}
		m := getJSON(t, srv.URL)
		gw, _ := m["gateway_addr"].(string)
		got = append(got, gw)
	}
	for i := 1; i < len(got); i++ {
		if got[i] == got[i-1] {
			t.Fatalf("round-robin roto en %d: %v", i, got)
		}
	}
	counts := map[string]int{}
	for _, gw := range got {
		counts[gw]++
	}
	if counts["192.0.2.10:443"] != 5 || counts["198.51.100.20:443"] != 5 {
		t.Fatalf("reparto en 10 calls: %v, want 5/5", counts)
	}
}

// T-0402 AC2: el servicio es sin estado — recrear la instancia entre
// calls (kubectl delete pod) no rompe al agente: 100 asignaciones con
// instancia nueva cada 10, 0 handshakes perdidos (cada respuesta
// decodifica y trae gateway del pool).
func TestStatelessRestartBetweenCalls(t *testing.T) {
	gateways := testAssigner(t).Gateways
	seen := map[string]int{}
	for i := 0; i < 100; i++ {
		// instancia "nueva" cada 10 calls: Assigner fresco, pool igual.
		a := NewAssigner(gateways, 900)
		srv := httptest.NewServer(newAssignHandler(a, mustGeo(t), quietLog()))
		m := getJSON(t, srv.URL)
		srv.Close()
		gw, _ := m["gateway_addr"].(string)
		found := false
		for _, g := range gateways {
			if g == gw {
				found = true
			}
		}
		if !found || len(m) != 3 {
			t.Fatalf("asignacion %d rotas tras reinicio: %v", i, m)
		}
		seen[gw]++
	}
	if seen["192.0.2.10:443"]+seen["198.51.100.20:443"] != 100 {
		t.Fatalf("reparto total = %v", seen)
	}
}

// El pool debe cubrir >= 2 ASNs (A1-18) o el rdv no cumple su condicion.
func TestPoolTwoASNs(t *testing.T) {
	geo := mustGeo(t)
	a := testAssigner(t)
	if n := geo.DistinctASNs(a.Gateways); n < 2 {
		t.Fatalf("ASNs del pool = %d, want >= 2 (A1-18)", n)
	}
}

func mustGeo(t *testing.T) *GatewayGeo {
	t.Helper()
	g, err := LoadGatewayGeo("")
	if err != nil {
		t.Fatal(err)
	}
	return g
}
