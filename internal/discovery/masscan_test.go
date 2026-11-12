package discovery

import (
	"strings"
	"testing"
)

// T-0103: el NDJSON de masscan se traduce a candidatos con service
// inferido por puerto; udp y lineas vacias se descartan.
func TestParseMasscanFixture(t *testing.T) {
	f := openTestdata(t, "testdata/discovery/masscan_sweep.ndjson")
	defer f.Close()
	cands, err := ParseMasscanNDJSON(f)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 500 {
		t.Fatalf("processed=%d want 500", len(cands))
	}
	for _, c := range cands {
		if c.Service == "" {
			t.Fatalf("service not inferred: %+v", c)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("candidate %+v: %v", c, err)
		}
	}
}

func TestParseMasscanEdgeCases(t *testing.T) {
	raw := "\n" +
		`{"ip":"1.1.1.1","port":554,"protocol":"tcp"}` + "\n" +
		`{"ip":"2.2.2.2","port":554,"protocol":"udp"}` + "\n" +
		`{"ip":"3.3.3.3","port":32210,"protocol":"tcp"}` + "\n"
	cands, err := ParseMasscanNDJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("udp/vacio no filtrados: %d", len(cands))
	}
	if _, err := ParseMasscanNDJSON(strings.NewReader("{no-json")); err == nil {
		t.Error("linea malformada deberia error")
	}
}
