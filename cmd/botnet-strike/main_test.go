package main

import (
	"os"
	"strings"
	"testing"
)

// TestLegbaParseSinRed: el parser de legba funciona por stdin, sin ejecutar
// nada externo (modo fixture de CI, injerto V3).
func TestLegbaParseSinRed(t *testing.T) {
	f, err := os.Open("../../testdata/intrusion/legba_hit.txt")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	defer f.Close()
	sal, err := os.CreateTemp("", "legba")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer os.Remove(sal.Name())
	if rc := legbaParse(f, "", sal); rc != 0 {
		t.Fatalf("legba-parse rc=%d", rc)
	}
	b, _ := os.ReadFile(sal.Name())
	out := string(b)
	if !strings.Contains(out, `"ok":true`) {
		t.Fatalf("sin veredicto valido: %s", out)
	}
	if !strings.Contains(out, `"protocol":"http"`) {
		t.Fatalf("sin protocolo: %s", out)
	}
}

// TestProtocolos: los cuatro protocolos del diccionario siguen ahi.
func TestProtocolos(t *testing.T) {
	// run escribe en *os.File: se usa un temporal para poder leerlo.
	f, err := os.CreateTemp("", "proto")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer os.Remove(f.Name())
	if rc := run([]string{"protocols"}, strings.NewReader(""), f); rc != 0 {
		t.Fatalf("protocols rc=%d", rc)
	}
	f.Seek(0, 0)
	b, _ := os.ReadFile(f.Name())
	out := string(b)
	for _, p := range []string{"http", "ssh", "telnet", "ftp"} {
		if !strings.Contains(out, p) {
			t.Fatalf("falta protocolo %q", p)
		}
	}
}
