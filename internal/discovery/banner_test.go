package discovery

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func newLineScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return sc
}

func TestNormalizeAndHash(t *testing.T) {
	a := "HTTP/1.0 200 OK\r\nServer: X \r\n\r\n"
	b := "HTTP/1.0 200 OK\n\nServer: X"
	if NormalizeBanner(a) != NormalizeBanner(b) {
		t.Fatal("normalizacion no colapsa CRLF/espacios")
	}
	h1, h2 := HashBanner(a), HashBanner(b)
	if h1 != h2 || len(h1) != 64 {
		t.Fatalf("hash inestable o mal tamano: %q %q", h1, h2)
	}
	if HashBanner("otro") == h1 {
		t.Fatal("hash de banners distintos coincide")
	}
}

func TestClassifyDeviceSamples(t *testing.T) {
	for _, tc := range []struct {
		banner string
		want   string
	}{
		{"HTTP/1.1 401 Unauthorized\r\nServer: MikroTik 3.41", ClassRouter},
		{"Server: TP-LINK/TL-WR841N", ClassRouter},
		{"Server: IPCamera-1.05", ClassCamera},
		{"<title>AXIS 213 Network Camera</title>", ClassCamera},
		{"Server: DH-MT1S-120188", ClassCamera},
		{"<title>Synology DiskStation: DSM</title>", ClassNAS},
		{"<title>WD My Cloud</title>", ClassNAS},
		{"Server: lighttpd/1.4.39", ClassUnknown},
	} {
		if got := ClassifyDevice(tc.banner); got != tc.want {
			t.Errorf("ClassifyDevice(%q)=%s want %s", tc.banner, got, tc.want)
		}
	}
}

func TestClassifyVendorModel(t *testing.T) {
	class, vendor, model := Classify("HTTP/1.1 200 OK\r\nServer: Tp-LinkSystem/hi3\r\n<title>TP-LINK | TL-WR841N v9</title>")
	if class != ClassRouter || vendor != "TP-Link" || model != "TL-WR841N" {
		t.Fatalf("classify router: %+v", []string{class, vendor, model})
	}
	class, vendor, model = Classify("Server: DCS-930L")
	if class != ClassCamera || vendor != "D-Link" || model != "DCS-930L" {
		t.Fatalf("classify camera: %+v", []string{class, vendor, model})
	}
	class, vendor, _ = Classify("Server: Hikvision WebServer")
	if class != ClassCamera || vendor != "Hikvision" {
		t.Fatalf("classify hikvision: %+v", []string{class, vendor})
	}
}

// T-0104.2: sobre el corpus etiquetado de 500 banners, la clasificacion
// router/camara/NAS acierta >= 70 %.
func TestCorpusAccuracyThreshold(t *testing.T) {
	f := openTestdata(t, "testdata/banners/corpus.jsonl")
	defer f.Close()
	var total, correct int
	sc := newLineScanner(f)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var rec struct {
			Banner string `json:"banner"`
			Label  string `json:"label"`
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatalf("corpus decode: %v", err)
		}
		total++
		if ClassifyDevice(rec.Banner) == rec.Label {
			correct++
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("corpus scan: %v", err)
	}
	if total != 500 {
		t.Fatalf("corpus total=%d want 500", total)
	}
	acc := float64(correct) / float64(total)
	if acc < 0.70 {
		t.Fatalf("accuracy %.3f < 0.70 (correct=%d)", acc, correct)
	}
	t.Logf("corpus accuracy=%.3f correct=%d/500", acc, correct)
}

func TestParseGrabsFixture(t *testing.T) {
	f := openTestdata(t, "testdata/banners/grabs.jsonl")
	defer f.Close()
	grabs, err := ParseGrabsJSONL(f)
	if err != nil {
		t.Fatalf("grabs: %v", err)
	}
	if len(grabs) != 100 {
		t.Fatalf("grabs=%d want 100", len(grabs))
	}
	cands := GrabsToCandidates(grabs)
	if len(cands) != 100 {
		t.Fatalf("cands=%d", len(cands))
	}
	var withClass, withHash int
	for _, c := range cands {
		if c.DeviceClassGuess != "" && c.DeviceClassGuess != ClassUnknown {
			withClass++
		}
		if len(c.BannerHash) == 64 {
			withHash++
		}
	}
	if withClass < 70 || withHash != 100 {
		t.Fatalf("con clase=%d hash=%d", withClass, withHash)
	}
}

// El parser del JSON real de zgrab2 (data.result.data.banner) debe
// extraer ip/puerto/banner solo en status success.
func TestParseZgrab2Output(t *testing.T) {
	raw := `{
	  "tcp/80": {"status":"success","data":{"result":{"ip":"5.6.7.8","port":80,"data":{"banner":"Server: MikroTik 3.41"}}}},
	  "tcp/23": {"status":"error","error":"connection refused","data":{"result":{"ip":"5.6.7.8","port":23,"data":{"banner":""}}}}
	}`
	grabs, err := ParseZgrab2Output(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("zgrab2: %v", err)
	}
	if len(grabs) != 1 || grabs[0].IP != "5.6.7.8" || grabs[0].Port != 80 {
		t.Fatalf("unexpected %+v", grabs)
	}
	if c := ClassifyDevice(grabs[0].Banner); c != ClassRouter {
		t.Fatalf("class %s", c)
	}
}
