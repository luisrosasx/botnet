package main

import (
	"strings"
	"testing"
)

// TestComandoMasscanExplicito: la IP de origen y el adaptador van en el argv
// y el ritmo sale del suelo del codigo.
func TestComandoMasscanExplicito(t *testing.T) {
	argv := strings.Join(comandoMasscan(1500, "74.208.94.91", "eth0", "0.0.0.0/0", "23,80"), " ")
	for _, esperado := range []string{"nice -n 10", "--source-ip 74.208.94.91", "--adapter eth0", "--min-rate 1500", "--output-format json"} {
		if !strings.Contains(argv, esperado) {
			t.Fatalf("argv sin %q: %s", esperado, argv)
		}
	}
}

// TestCohorte: 325.100 SYN a 1500 pps son cohortes de 90.000 y no una sola.
func TestCohorte(t *testing.T) {
	tam, n := cohorte(325100, 1500, 60)
	if tam != 90000 {
		t.Fatalf("tamano de cohorte=%d (quiero 90000)", tam)
	}
	if n < 3 {
		t.Fatalf("cohortes=%d (quiero >=3)", n)
	}
	// Sin segundos explcitos se usa la ventana por defecto.
	tam, _ = cohorte(1000, 1500, 0)
	if tam != 90000 {
		t.Fatalf("ventana por defecto=%d (quiero 90000)", tam)
	}
}
