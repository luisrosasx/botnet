package geoip

import (
	"net"
	"strings"
	"testing"
)

func TestParseAndLookup(t *testing.T) {
	table, err := ParseASNTable(strings.NewReader(`
# prefijo madre y dos hijos mas especificos
10.0.0.0/8 64496
10.1.0.0/16 64497
10.1.2.0/24 64498
192.0.2.7 64500
`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		ip  string
		asn uint32
		ok  bool
	}{
		{"10.2.3.4", 64496, true},  // cae en el /8
		{"10.1.9.9", 64497, true},  // el /16 gana al /8
		{"10.1.2.5", 64498, true},  // el /24 gana a ambos
		{"192.0.2.7", 64500, true}, // host suelto = /32
		{"192.0.2.8", 0, false},    // fuera de toda tabla
		{"2001:db8::1", 0, false},  // v6 no tabulada
	}
	for _, c := range cases {
		asn, ok := table.Lookup(net.ParseIP(c.ip))
		if ok != c.ok || (c.ok && asn != c.asn) {
			t.Fatalf("Lookup(%s) = %d/%v, want %d/%v", c.ip, asn, ok, c.asn, c.ok)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"sin-numero 64501 64502", "no-es-ip xyz"} {
		if _, err := ParseASNTable(strings.NewReader(bad)); err == nil {
			t.Fatalf("%q deberia rechazarse", bad)
		}
	}
}

func TestASNOfGateway(t *testing.T) {
	table := DefaultFixture()
	asn, ok := table.ASNOfGateway("192.0.2.10:443", nil)
	if !ok || asn != 64501 {
		t.Fatalf("gateway literal: %d/%v, want 64501/true", asn, ok)
	}
	// nombre con resolver fixture: gw.example -> IP del segundo ASN.
	look := func(host string) ([]string, error) {
		if host == "gw-b.example" {
			return []string{"198.51.100.33"}, nil
		}
		return nil, nil
	}
	asn, ok = table.ASNOfGateway("gw-b.example:443", look)
	if !ok || asn != 64502 {
		t.Fatalf("gateway nombre: %d/%v, want 64502/true", asn, ok)
	}
	if _, ok := table.ASNOfGateway("gw-x.example:443", look); ok {
		t.Fatal("host desconocido sin ASN")
	}
}

func TestDefaultFixtureTwoASNs(t *testing.T) {
	table := DefaultFixture()
	a, okA := table.Lookup(net.ParseIP("192.0.2.1"))
	b, okB := table.Lookup(net.ParseIP("198.51.100.60"))
	if !okA || !okB {
		t.Fatal("fixture debe cubrir sus dos prefijos")
	}
	if a == b {
		t.Fatalf("A1-18 exige dos ASNs, ambos %d", a)
	}
}
