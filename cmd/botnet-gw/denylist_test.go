package main

import (
	"testing"
	"time"
)

// AC1: tabla de 4 rangos con 8 asserts de fixture — 192.168/16, 10/8,
// 172.16/12 y 100.64/10 rechazan con host_unreachable.
func TestDenyListFixtureEightAsserts(t *testing.T) {
	denied := [][2]string{
		{"192.168.1.1:80", "192.168/16"}, {"192.168.255.255:8080", "192.168/16"},
		{"10.1.2.3:22", "10/8"}, {"10.255.255.254:443", "10/8"},
		{"172.20.0.1:80", "172.16/12"}, {"172.31.255.255:443", "172.16/12"},
		{"100.64.0.1:443", "100.64/10"}, {"100.127.255.255:53", "100.64/10"},
	}
	for _, d := range denied {
		ok, reason := DeniedConnect(d[0])
		if !ok || reason != DenyReason {
			t.Fatalf("CONNECT %s (%s): ok=%v reason=%q", d[0], d[1], ok, reason)
		}
	}
	// 172.32/12 NO es 172.16/12: no topamos de mas (el cliente no nota).
	if ok, _ := DeniedConnect("172.32.0.1:80"); ok {
		t.Fatal("172.32.0.1 fuera de 172.16/12")
	}
}

// AC2: 8.8.8.8:53 y 1.1.1.1:443 admitidos; con solo intentos LAN el
// contador de egress del nodo queda a 0 bytes (rechazo en el gateway).
func TestPublicDestFlowAndZeroEgress(t *testing.T) {
	for _, ok := range []string{"8.8.8.8:53", "1.1.1.1:443"} {
		if d, r := DeniedConnect(ok); d {
			t.Fatalf("%s admitido, got %q", ok, r)
		}
	}
	var eg EgressCounters
	for _, lan := range []string{"192.168.1.1:80", "10.0.0.5:443", "100.64.0.9:22"} {
		if d, _ := eg.ConnectDenied(lan); !d {
			t.Fatalf("%s deberia caer en la deny-list", lan)
		}
	}
	if eg.Bytes != 0 || eg.Conns != 0 {
		t.Fatalf("egress con solo intentos LAN: %d B / %d conns (0 exigido)", eg.Bytes, eg.Conns)
	}
	if d, _ := eg.ConnectDenied("8.8.8.8:53"); d {
		t.Fatal("8.8.8.8 fluye")
	}
	if eg.Conns != 1 {
		t.Fatalf("conns publicas: %d", eg.Conns)
	}
}

// AC3: port-scan simulado (1.000 CONNECTs a LAN en 1 min): el panel del
// router no recibe ni un SYN (egress 0) y el tenant recibe 429 de
// rate-limit por sesion al agotar el budget de la ventana.
func TestLanscapeScanRateLimit(t *testing.T) {
	guard := NewScanGuard()
	var eg EgressCounters
	guard.Now = func() time.Time { return time.Unix(1_000, 0) } // ventana fija de 1 min

	scan429, scanDenied := 0, 0
	for i := 0; i < 1000; i++ {
		target := "192.168.1." + itoa(i%255+1) + ":80"
		denied, _ := eg.ConnectDenied(target)
		if !denied {
			t.Fatalf("%s en LAN denegada", target)
		}
		if ok, code := guard.Allow(); !ok {
			if code != 429 {
				t.Fatalf("code %d (429 exigido)", code)
			}
			scan429++
		} else {
			scanDenied++
		}
	}
	if eg.Bytes != 0 {
		t.Fatalf("0 SYN en la LAN del dueño: %d bytes", eg.Bytes)
	}
	if scanDenied != guard.Budget {
		t.Fatalf("budget LAN por ventana: %d (presupuesto %d)", scanDenied, guard.Budget)
	}
	if scan429 != 1000-guard.Budget {
		t.Fatalf("429 por session: %d", scan429)
	}

	// Reloj acelerado: ventana cumplida -> budget renovado (no es baneo
	// permanente; el scan lento sigue pudiendo sondear).
	guard.Now = func() time.Time { return time.Unix(1_061, 0) }
	if ok, _ := guard.Allow(); !ok {
		t.Fatal("ventana nueva: el budget renueva")
	}
}
