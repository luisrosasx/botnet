package discovery

import (
	"strings"
	"testing"
)

// T-0102: el fixture de 1000 resultados de Shodan parsea completo y
// cada candidato es insertable en discovered.
func TestParseShodanFixture1K(t *testing.T) {
	f := openTestdata(t, "testdata/shodan_1k.json")
	defer f.Close()
	cands, err := ParseShodanSearch(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 1000 {
		t.Fatalf("processed=%d want 1000", len(cands))
	}
	seen := map[string]bool{}
	for _, c := range cands {
		if err := c.Validate(); err != nil {
			t.Fatalf("candidate %+v: %v", c, err)
		}
		if c.Source != "shodan" {
			t.Errorf("source %q want shodan", c.Source)
		}
		seen[c.IP] = true
	}
	if len(seen) < 300 {
		t.Errorf("fixture too degenerate: %d IPs distinctos", len(seen))
	}
}

// El campo "ip" numerico big-endian debe poder derivar el IP cuando
// Shodan omite ip_str (formatos de exportacion).
func TestParseShodanNumericIPOnly(t *testing.T) {
	raw := `{"matches":[{"ip":16843009,"port":23,"transport":"tcp","location":{"asn":99,"country_code":"ES"}}]}`
	cands, err := ParseShodanSearch(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 1 || cands[0].IP != "1.1.1.1" || cands[0].Service != "telnet" {
		t.Fatalf("unexpected candidate %+v", cands[0])
	}
}

// Aceptar "results" como envoltura alternativa de "matches".
func TestParseShodanResultsWrapper(t *testing.T) {
	raw := `{"results":[{"ip_str":"9.9.9.9","port":80}]}`
	cands, err := ParseShodanSearch(strings.NewReader(raw))
	if err != nil || len(cands) != 1 || cands[0].Service != "http" {
		t.Fatalf("wrapper parse broken: %+v %v", cands, err)
	}
}
