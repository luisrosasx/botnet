// Command gen regenera los fixtures de testdata de EP-01 de forma
// determinista (seed fijo):
//
//	go run ./internal/discovery/gen
//
// Los ficheros generados van versionados; el generador existe para
// reproducir/extend el corpus sin reescribirlo a mano.
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// repoRoot se resuelve subiendo hasta go.mod: el gen corre igual desde
// la raiz del repo (go run ./internal/discovery/gen) que desde el
// directorio del paquete.
var repoRoot = findModuleRoot()

func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("gen: go.mod not found above cwd")
		}
		dir = parent
	}
}

func main() {
	mustWrite("testdata/shodan_1k.json", shodanJSON())
	mustWrite("testdata/banners/corpus.jsonl", bannerCorpusJSONL())
	mustWrite("testdata/banners/hosts.txt", hostsFile())
	mustWrite("testdata/banners/grabs.jsonl", grabsJSONL())
	mustWrite("testdata/discovery/masscan_sweep.ndjson", masscanNDJSON())
	mustWrite("testdata/discovery/report_fixture.csv", reportFixture)
	mustWrite("testdata/discovery/nodes_leaves_fixture.sql", nodesLeavesFixture)
	fmt.Println("gen: fixtures EP-01 escritos")
}

func mustWrite(rel string, content string) {
	p := filepath.Join(repoRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		panic(err)
	}
}

// --- Shodan ------------------------------------------------------------

type shodanProfile struct {
	vendor, model, product string
	port                   int
}

var shodanProfiles = []shodanProfile{
	{"MikroTik", "RB450Gx4", "MikroTik", 80},
	{"TP-Link", "TL-WR841N", "TP-Link router", 80},
	{"ZyXEL", "P2812HWNU", "ZyXEL HTTP Server", 80},
	{"Netgear", "WNR2000v3", "Netgear WNR2000", 80},
	{"D-Link", "DIR-615", "DIR-615 web", 80},
	{"Huawei", "HG532", "Huawei Voice Gateway", 80},
	{"ZTE", "ZXHN H108N", "ZXHN router", 80},
	{"Technicolor", "TG582n", "Home Gateway", 80},
	{"DrayTek", "Vigor2860", "Vigor router", 80},
	{"Hikvision", "DS-7204HVI-ST", "Hikvision DVR", 80},
	{"Dahua", "DHI-DAHUA-IPC", "DVR IP", 80},
	{"AXIS", "AXIS 213", "Network Camera", 80},
	{"D-Link", "DCS-930L", "myD-Link Camera", 80},
	{"JVC", "WV-SP300", "WJ camera", 554},
	{"", "", "IPCamera", 8080},
	{"", "", "miniDVR", 8080},
	{"Synology", "DS216play", "Synology DSM", 5000},
	{"QNAP", "TS-231", "QTS", 8080},
	{"Buffalo", "LS-VL", "LinkStation", 80},
	{"", "", "RTSP server", 554},
	{"", "", "Telnet", 23},
	{"", "", "RedStorm DVR", 32210},
}

var shodanASNs = []int{4134, 9829, 7922, 6181, 3352, 3320, 3329, 2856, 9009, 45609, 4755, 17923, 28608, 37457, 12322}
var shodanCountries = []string{"CN", "US", "DE", "BR", "IN", "RU", "MX", "IT", "ES", "TR", "ID", "TH", "ZA", "AR", "PL"}

func shodanJSON() string {
	rng := rand.New(rand.NewSource(20260114))
	// Bases /24: un grupo por cada bloque de 250 matches => (ip,port)
	// unico por match, requisito del AC T-0102.3 ("las 100 filas").
	bases := make([][3]byte, 0, 8)
	seen := map[[3]byte]bool{}
	for len(bases) < 4 {
		b := [3]byte{byte(rng.Intn(222) + 1), byte(rng.Intn(256)), byte(rng.Intn(256))}
		if seen[b] {
			continue
		}
		seen[b] = true
		bases = append(bases, b)
	}
	type match struct {
		IPNum     uint32            `json:"ip"`
		IPStr     string            `json:"ip_str"`
		Port      int               `json:"port"`
		Transport string            `json:"transport"`
		Product   string            `json:"product"`
		Server    string            `json:"server"`
		Device    map[string]string `json:"device,omitempty"`
		Location  map[string]any    `json:"location"`
		Timestamp string            `json:"timestamp"`
	}
	matches := make([]match, 0, 1000)
	for i := 0; i < 1000; i++ {
		base := bases[i/250]
		ip := net.IPv4(base[0], base[1], base[2], byte(i%250)+1).To4()
		num := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
		p := shodanProfiles[rng.Intn(len(shodanProfiles))]
		m := match{
			IPNum: num, IPStr: ip.String(), Port: p.port, Transport: "tcp",
			Product: p.product, Server: p.product,
			Location: map[string]any{
				"asn":          shodanASNs[rng.Intn(len(shodanASNs))],
				"country_code": shodanCountries[rng.Intn(len(shodanCountries))],
			},
			Timestamp: fmt.Sprintf("2026-01-%02dT%02d:15:00.000", 1+(i%27), i%24),
		}
		if p.vendor != "" && rng.Intn(10) < 7 {
			m.Device = map[string]string{"vendor": p.vendor, "model": p.model}
		}
		matches = append(matches, m)
	}
	doc := struct {
		Total   int     `json:"total"`
		Matches []match `json:"matches"`
	}{1000, matches}
	buf, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		panic(err)
	}
	return string(buf) + "\n"
}

// --- corpus de banners ---------------------------------------------------

type bannerTemplate struct {
	label string
	text  string
}

func bannerTemplates() []bannerTemplate {
	var t []bannerTemplate
	add := func(label, text string) { t = append(t, bannerTemplate{label, text}) }
	// routers
	add("router", "HTTP/1.1 401 Unauthorized\r\nServer: MikroTik 3.41\r\nWWW-Authenticate: Basic realm=\"MikroTik\"\r\nConnection: close\r\n\r\n")
	add("router", "HTTP/1.0 401 Authorization Required\r\nServer: OpenWrt-LuCI\r\nWWW-Authenticate: Basic realm=\"LuCI - OpenWrt\"\r\n\r\n")
	add("router", "HTTP/1.1 200 OK\r\nServer: Tp-LinkSystem/hi3\r\nContent-Type: text/html\r\nConnection: close\r\n\r\n<title>TP-LINK | TL-WR841N v9</title>")
	add("router", "HTTP/1.0 403 Forbidden\r\nServer: Zyxel HTTP Server\r\nConnection: close\r\n\r\n")
	add("router", "HTTP/1.1 200 OK\r\nServer: cheetah\r\nConnection: close\r\n\r\n<title>DIR-615 | Wireless N Router</title>")
	add("router", "HTTP/1.0 200 OK\r\nServer: HTTP Server 1.0\r\nConnection: close\r\n\r\n<title>Huawei Technologies - HG532</title>")
	add("router", "HTTP/1.1 200 OK\r\nServer: ZXH108 Router Webserver\r\nConnection: close\r\n\r\n<title>ZXHN H108N</title>")
	add("router", "HTTP/1.1 200 OK\r\nServer: DrayTek/1.2\r\nConnection: close\r\n\r\n<title>Vigor 2925</title>")
	add("router", "HTTP/1.1 200 OK\r\nServer: Apache\r\nConnection: close\r\n\r\n<title>Technicolor TG582n</title>")
	add("router", "HTTP/1.1 200 OK\r\nServer: KWebServer\r\nConnection: close\r\n\r\n<title>SpeedTouch 5x6</title>")
	add("router", "HTTP/1.0 401 Unauthorized\r\nServer: HTTP Server at 192.168.1.1\r\nConnection: close\r\n\r\n<title>WRT54G</title>")
	add("router", "HTTP/1.1 200 OK\r\nServer: Broadcom BCM963xx\r\nConnection: close\r\n\r\n<title>Home Gateway</title>")
	add("router", "HTTP/1.1 200 OK\r\nServer: Netgear WNR2000 V1.1.2\r\nConnection: close\r\n\r\n")
	add("router", "HTTP/1.1 200 OK\r\nServer: GPON ONT\r\nConnection: close\r\n\r\n<title>Netgear WGR614v10</title>")
	// camaras
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: IPCamera-1.05\r\nWWW-Authenticate: Basic realm=\"NETIPCAMERA-960H\"\r\n\r\n")
	add("camera", "HTTP/1.1 200 OK\r\nServer: GoAhead-Webs\r\nX-Frame-Options: SAMEORIGIN\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: DVRIP-Web V2.2\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nWWW-Authenticate: Digest realm=\"H.264 Net DVR\"\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 200 OK\r\nServer: AXIS213 Web Server/2.30\r\nConnection: close\r\n\r\n<title>AXIS 213 Network Camera</title>")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: JVC WJ-HDE400\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: Bosch DVR-4000\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: Hikvision WebServer\r\nX-Frame-Options: SAMEORIGIN\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: miniDVR\r\nWWW-Authenticate: Basic realm=\"miniDVR\"\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: WV-SP300 Web Server\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: DCS-930L\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: DH-MT1S-120188\r\nConnection: close\r\n\r\n")
	add("camera", "HTTP/1.1 200 OK\r\nServer: 3S-W20T_v1.0.1\r\nConnection: close\r\n\r\n<title>Sentry360</title>")
	add("camera", "HTTP/1.1 401 Unauthorized\r\nServer: Sophia_(Web) v1.00\r\nConnection: close\r\n\r\n")
	// NAS
	add("nas", "HTTP/1.1 401 Unauthorized\r\nServer: Synology-WebStations/2.3\r\nConnection: close\r\n\r\n<title>Synology DiskStation: DSM</title>")
	add("nas", "HTTP/1.1 200 OK\r\nServer: nginx\r\nX-Frame-Options: SAMEORIGIN\r\nConnection: close\r\n\r\n<title>QTS - QNAP Turbo NAS</title>")
	add("nas", "HTTP/1.1 401 Unauthorized\r\nServer: freenas (nginx)\r\nConnection: close\r\n\r\n<title>FreeNAS Web UI</title>")
	add("nas", "HTTP/1.1 200 OK\r\nServer: Apache/2.4\r\nConnection: close\r\n\r\n<title>ReadyNAS</title>")
	add("nas", "HTTP/1.1 200 OK\r\nServer: nginx\r\nConnection: close\r\n\r\n<title>WD My Cloud</title>")
	add("nas", "HTTP/1.1 401 Unauthorized\r\nServer: Asustor Wizard\r\nConnection: close\r\n\r\n<title>AS-320 V2</title>")
	add("nas", "HTTP/1.1 200 OK\r\nServer: Thecus N5550\r\nConnection: close\r\n\r\n<title>Thecus NAS</title>")
	add("nas", "HTTP/1.1 200 OK\r\nServer: Disklib\r\nConnection: close\r\n\r\n<title>Buffalo LinkStation LS-VL</title>")
	return t
}

// genericBanners son banners sin token reconocible: el clasificador los
// deja en unknown y marcan el techo honesto de accuracy del corpus.
var genericBanners = map[string]string{
	"router": "HTTP/1.1 200 OK\r\nServer: Apache/2.2.22\r\nContent-Type: text/html\r\nConnection: close\r\n\r\n",
	"camera": "HTTP/1.0 200 OK\r\nServer: miniweb/1.0E\r\nConnection: close\r\n\r\n",
	"nas":    "HTTP/1.1 200 OK\r\nServer: lighttpd/1.4.39\r\nConnection: close\r\n\r\n",
}

func bannerCorpusJSONL() string {
	rng := rand.New(rand.NewSource(20260115))
	byLabel := map[string][]bannerTemplate{}
	for _, t := range bannerTemplates() {
		byLabel[t.label] = append(byLabel[t.label], t)
	}
	targets := []struct {
		label string
		count int
	}{{"router", 190}, {"camera", 190}, {"nas", 120}}
	var sb strings.Builder
	n := 0
	for _, tg := range targets {
		tn := byLabel[tg.label]
		for i := 0; i < tg.count; i++ {
			n++
			var banner string
			if rng.Intn(100) < 15 {
				banner = genericBanners[tg.label]
			} else {
				banner = tn[(i+rng.Intn(1))%len(tn)].text
			}
			rec := map[string]any{
				"id":     fmt.Sprintf("b%03d", n),
				"label":  tg.label,
				"banner": banner,
			}
			line, err := json.Marshal(rec)
			if err != nil {
				panic(err)
			}
			sb.Write(line)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func hostsFile() string {
	return grabsFile(false)
}

func grabsJSONL() string {
	return grabsFile(true)
}

// grabsFile genera hosts.txt y grabs.jsonl alineados: los primeros 100
// banners del corpus sobre IPs publicas sintetizadas.
func grabsFile(jsonl bool) string {
	rng := rand.New(rand.NewSource(20260116))
	ports := []int{80, 80, 8080, 554, 23, 32210}
	corpus := strings.Split(strings.TrimRight(bannerCorpusJSONL(), "\n"), "\n")
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		ip := fmt.Sprintf("39.108.%d.%d", (i/17)%250, (i*7)%250+1)
		port := ports[rng.Intn(len(ports))]
		if jsonl {
			// reutiliza el banner del corpus: hosts y grabs apuntan a
			// banners ya etiquetados.
			var parsed map[string]any
			if err := json.Unmarshal([]byte(corpus[i]), &parsed); err != nil {
				panic(err)
			}
			line, err := json.Marshal(map[string]any{
				"ip":     ip,
				"port":   port,
				"banner": parsed["banner"],
			})
			if err != nil {
				panic(err)
			}
			sb.Write(line)
			sb.WriteByte('\n')
		} else {
			fmt.Fprintf(&sb, "%s:%d\n", ip, port)
		}
	}
	return sb.String()
}

func masscanNDJSON() string {
	rng := rand.New(rand.NewSource(20260117))
	ports := []int{23, 80, 8080, 554, 32210}
	bases := make([][3]byte, 0, 200)
	for len(bases) < 200 {
		b := [3]byte{byte(rng.Intn(222) + 1), byte(rng.Intn(256)), byte(rng.Intn(256))}
		for _, e := range bases {
			if e == b {
				b[2] ^= 0xAA
				break
			}
		}
		bases = append(bases, b)
	}
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		base := bases[rng.Intn(len(bases))]
		ip := fmt.Sprintf("%d.%d.%d.%d", base[0], base[1], base[2], rng.Intn(254)+1)
		fmt.Fprintf(&sb, `{"ip":"%s","port":%d,"protocol":"tcp","frame":"eth h"}`, ip, ports[rng.Intn(len(ports))])
		sb.WriteByte('\n')
	}
	return sb.String()
}

const reportFixture = `asn,country,ip,device_class_guess
4134,CN,1.20.30.10,router
4134,CN,1.20.30.99,router
4134,CN,1.20.31.5,camera
9829,BR,201.15.7.3,nas
9829,BR,201.15.7.4,nas
9829,BR,201.15.8.9,router
17923,ID,36.68.10.1,camera
17923,ID,36.68.10.2,camera
17923,ID,36.68.10.200,unknown
0,,10.0.0.1,unknown
`

const nodesLeavesFixture = `-- Nodes/leaves minimos para probar la seleccion T-0106 antes de que
-- EP-08 publique su DDL canonico (mismo shape que BLUEPRINT §9 en las
-- columnas que usa la consulta: public_ip, optout_at, reprobe_blocked_until).
CREATE TABLE nodes (
  node_id uuid PRIMARY KEY,
  public_ip inet NOT NULL,
  optout_at timestamptz
);
CREATE TABLE leaves (
  leave_id bigserial PRIMARY KEY,
  node_id uuid NOT NULL REFERENCES nodes (node_id),
  reprobe_blocked_until timestamptz NOT NULL
);
-- 5 000 nodos de ruido para que el planner use indices (no seq scan).
INSERT INTO nodes (node_id, public_ip)
SELECT md5(i::text)::uuid,
       ('197.' || (i % 250) || '.' || ((i / 250) % 256) || '.' || ((i * 7) % 256) || '.' || ((i * 13) % 256))::inet
FROM generate_series(1, 5000) i;
-- casos nominales: opt-out reciente, reprobe bloqueado y reprobe vencido.
INSERT INTO nodes (node_id, public_ip, optout_at) VALUES
  ('11111111-1111-4111-8111-111111111101', '8.8.4.4'::inet, now() - interval '30 days'),
  ('11111111-1111-4111-8111-111111111102', '8.8.5.5'::inet, NULL),
  ('11111111-1111-4111-8111-111111111103', '8.8.6.6'::inet, NULL),
  ('11111111-1111-4111-8111-111111111104', '8.8.7.7'::inet, NULL);
INSERT INTO leaves (node_id, reprobe_blocked_until) VALUES
  ('11111111-1111-4111-8111-111111111102', now() + interval '11 months'),
  ('11111111-1111-4111-8111-111111111103', now() - interval '1 day');
CREATE INDEX nodes_public_ip_idx ON nodes (public_ip);
CREATE INDEX nodes_optout_idx ON nodes (optout_at) WHERE optout_at IS NOT NULL;
CREATE INDEX leaves_node_idx ON leaves (node_id);
CREATE INDEX leaves_reprobe_idx ON leaves (reprobe_blocked_until);
ANALYZE;
`
