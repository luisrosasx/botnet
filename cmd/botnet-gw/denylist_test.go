package main

import (
	"net"
	"testing"
)

// TestDeniedConnect cubre los cuatro rangos clasicos y los dos nuevos: la
// loopback y el enlace local no son destino valido para un CONNECT.
func TestDeniedConnect(t *testing.T) {
	casos := []string{"127.0.0.1", "169.254.1.1", "10.1.2.3", "192.168.0.5",
		"172.16.4.4", "100.64.0.9", "::1", "fe80::1"}
	for _, c := range casos {
		if !DeniedIP(net.ParseIP(c)) {
			t.Errorf("%s deberia estar denegada", c)
		}
		t.Logf("denegada %s", c)
	}
	for _, c := range []string{"8.8.8.8", "74.208.94.91", "2001:4860::8888"} {
		if DeniedIP(net.ParseIP(c)) {
			t.Errorf("%s no deberia estar denegada", c)
		}
	}
}
