// Nonce en el gateway (T-0808): la asignacion emite, el OPEN_STREAM
// ecota; un eco incorrecto (fixture nonce-1) cierra el stream con
// closed_reason='error' y un edge que manda datos antes que su prueba
// propia no consume bytes de tenant.
package main

import (
	"testing"

	"github.com/botnet/botnet/internal/tunnel"
)

func TestNonceRegistryRejectsBadEcho(t *testing.T) {
	reg := NewNonceRegistry()
	s1, s2 := uint64(101), uint64(202)
	n1, err := tunnel.IssueSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	n2, err := tunnel.IssueSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	reg.Issue(s1, n1)
	reg.Issue(s2, n2)

	// fixture del nodo con bug: ecota nonce-1 -> 'error'
	if reason, ok := reg.HandleOpen(tunnel.OpenStream{SessionID: s1, Nonce: n1 - 1}); ok || reason != closedReasonNonceError {
		t.Errorf("eco nonce-1: reason=%q ok=%v, quiero 'error'/false", reason, ok)
	}
	// eco correcto: el stream vive
	if reason, ok := reg.HandleOpen(tunnel.OpenStream{SessionID: s2, Nonce: n2}); !ok || reason != "" {
		t.Errorf("eco correcto: reason=%q ok=%v", reason, ok)
	}
	// sesion desconocida (stream huerfano): rechazada
	if reason, ok := reg.HandleOpen(tunnel.OpenStream{SessionID: 999, Nonce: 1}); ok || reason != "error" {
		t.Errorf("sesion desconocida: reason=%q ok=%v", reason, ok)
	}
	// AC2: sin OPEN validado, los datos del edge falso se descartan
	if AllowData(false) {
		t.Error("AllowData(false) debe descartar bytes previos al OPEN validado")
	}
	if !AllowData(true) {
		t.Error("AllowData(true) debe dejar pasar tras OPEN ok")
	}
}
