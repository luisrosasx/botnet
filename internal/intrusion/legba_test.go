package intrusion

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocols(t *testing.T) {
	if len(Protocols) != 4 {
		t.Fatalf("el runner debe cubrir 4 protocolos, hay %d: %v", len(Protocols), Protocols)
	}
	for _, want := range []string{"http", "ssh", "telnet", "ftp"} {
		found := false
		for _, p := range Protocols {
			if p == want {
				found = true
			}
		}
		if !found {
			t.Errorf("falta %s en %v", want, Protocols)
		}
	}
}

const legbaJSONHit = `{"info":"x","credentials":[{"protocol":"HTTP","port":80,"username":"admin","password":"admin"},{"protocol":"telnet","port":23,"username":"root","password":"vizx"}]}`

func TestParseLegbaJSONHit(t *testing.T) {
	hit, err := ParseLegbaOutput([]byte(legbaJSONHit))
	if err != nil {
		t.Fatal(err)
	}
	if hit.Protocol != "http" || hit.CredIndex != 0 {
		t.Fatalf("hit: %+v", hit)
	}
}

func TestParseLegbaJSONMiss(t *testing.T) {
	if _, err := ParseLegbaOutput([]byte(`{"credentials":[],"results":[]}`)); err != ErrNoLegbaHit {
		t.Fatalf("target sano: quiero ErrNoLegbaHit, tengo %v", err)
	}
}

func TestParseLegbaJSONByProto(t *testing.T) {
	raw := `{"telnet":[{"username":"root","password":"admin"}],"http":[]}`
	hit, err := ParseLegbaOutput([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if hit.Protocol != "telnet" {
		t.Fatalf("byProto: %+v", hit)
	}
}

func TestParseLegbaText(t *testing.T) {
	hit, err := ParseLegbaOutput([]byte("scanning...\n[SUCCESS] http 10.70.0.5:80 admin:admin\n"))
	if err != nil {
		t.Fatal(err)
	}
	if hit.Protocol != "http" || hit.CredIndex != 0 {
		t.Fatalf("texto: %+v", hit)
	}
	if _, err := ParseLegbaOutput([]byte("[INFO] ftp 10.70.0.9:21 admin:admin (refused)\n")); err != ErrNoLegbaHit {
		t.Fatalf("sin SUCCESS: quiero ErrNoLegbaHit, tengo %v", err)
	}
}

func TestVerdictJSONNulls(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteVerdict(&buf, LegbaVerdict{OK: false}); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(buf.Bytes(), &obj); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"protocol", "cred_index", "model_guess"} {
		v, ok := obj[k]
		if !ok {
			t.Fatalf("falta clave %q en %s", k, buf.String())
		}
		if v != nil {
			t.Errorf("%q debería ser null: %s", k, buf.String())
		}
	}
}

func TestGuessModel(t *testing.T) {
	cases := []struct{ banner, want string }{
		{"<title>TP-LINK TL-WR841N(v14_0_9_x) Utility</title>", "TL-WR841"},
		{"Server: Boa — DIR-615 (Hardware D2)", "DIR-615"},
		{"ZyXEL P2812HNA-F login:", ""}, // sin patrón de modelo -> ok=false
	}
	for _, c := range cases {
		got, ok := GuessModel(c.banner)
		if c.want == "" {
			if ok {
				t.Errorf("%q: esperaba sin coincidencia, di %q", c.banner, got)
			}
			continue
		}
		if !ok || !strings.EqualFold(got, c.want) {
			t.Errorf("%q: guess %q ok=%v, quiero %q", c.banner, got, ok, c.want)
		}
	}
}
