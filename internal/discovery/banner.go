package discovery

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Grab es un banner capturado por zgrab2 (o por su doble de fixture)
// listo para clasificar.
type Grab struct {
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Banner string `json:"banner"`
}

// NormalizeBanner colapsa CRLF/TAB y espacios multiples y recorta: dos
// banners que difieren solo en whitespace comparten banner_hash, que es
// la clave de regresion del corpus.
func NormalizeBanner(b string) string {
	b = strings.ReplaceAll(b, "\r\n", "\n")
	b = strings.ReplaceAll(b, "\r", "\n")
	b = strings.ReplaceAll(b, "\t", " ")
	var sb strings.Builder
	prevSpace := false
	for _, r := range strings.TrimSpace(b) {
		if r == ' ' || r == '\n' {
			if !prevSpace {
				sb.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		sb.WriteRune(r)
	}
	return strings.TrimSpace(sb.String())
}

// HashBanner devuelve sha256 hex del banner normalizado (64 chars).
func HashBanner(b string) string {
	sum := sha256.Sum256([]byte(NormalizeBanner(b)))
	return hex.EncodeToString(sum[:])
}

// classRule evalua tokens sobre el banner normalizado en minusculas.
// El orden importa: tokens especificos de producto antes que genericos
// (p. ej. "dcs-" de camaras D-Link antes del genérico "d-link").
type classRule struct {
	token string
	class string
}

var classRules = []classRule{
	// cameras / DVR / NVR
	{"ipcamera", ClassCamera}, {"ip camera", ClassCamera}, {"ipcam", ClassCamera},
	{"webcam", ClassCamera}, {"web camera", ClassCamera}, {"wifi camera", ClassCamera},
	{"mini-dvr", ClassCamera}, {"minidvr", ClassCamera}, {"mini dvr", ClassCamera},
	{"dvrip", ClassCamera}, {"dvr-ip", ClassCamera}, {"nvr", ClassCamera},
	{"hikvision", ClassCamera}, {"dahua", ClassCamera}, {"xmeye", ClassCamera},
	{"h264", ClassCamera}, {"axis ", ClassCamera}, {"axis-network", ClassCamera},
	{"bosch", ClassCamera}, {"jvc", ClassCamera}, {"panasonic", ClassCamera},
	{"sentry360", ClassCamera}, {"sophia", ClassCamera}, {"wificam", ClassCamera},
	{"dcs-", ClassCamera}, {"wv-", ClassCamera}, {"goahead-webs", ClassCamera},
	{"axis", ClassCamera}, {"dh-mt", ClassCamera}, {"h.264", ClassCamera},
	{"sophia css", ClassCamera}, {"webserver dcs", ClassCamera},
	// NAS
	{"synology", ClassNAS}, {"diskstation", ClassNAS}, {"dsm", ClassNAS},
	{"qnap", ClassNAS}, {"linkstation", ClassNAS}, {"linkstationpro", ClassNAS},
	{"buffalo", ClassNAS}, {"freenas", ClassNAS}, {"openmediacore", ClassNAS},
	{"asustor", ClassNAS}, {"readynas", ClassNAS}, {"my cloud", ClassNAS},
	{"thecus", ClassNAS}, {"nas4free", ClassNAS}, {"webarchive", ClassNAS},
	{"disklib", ClassNAS}, {"miniserver", ClassNAS},
	// routers / CPE
	{"mikrotik", ClassRouter}, {"routeros", ClassRouter}, {"tp-link", ClassRouter},
	{"tp_link", ClassRouter}, {"tplink", ClassRouter}, {"tl-w", ClassRouter},
	{"tl-m", ClassRouter}, {"tl-r", ClassRouter}, {"zyxel", ClassRouter},
	{"netgear", ClassRouter}, {"wnr", ClassRouter}, {"wgr", ClassRouter},
	{"wndr", ClassRouter}, {"wnt", ClassRouter}, {"dir-", ClassRouter},
	{"dsl-", ClassRouter}, {"d-link", ClassRouter}, {"huawei", ClassRouter},
	{"hg2", ClassRouter}, {"hg3", ClassRouter}, {"hg5", ClassRouter},
	{"zte", ClassRouter}, {"zxhn", ClassRouter}, {"zxv10", ClassRouter},
	{"speedtouch", ClassRouter}, {"speedport", ClassRouter}, {"technicolor", ClassRouter},
	{"tg5", ClassRouter}, {"tg6", ClassRouter}, {"tg7", ClassRouter},
	{"arris", ClassRouter}, {"draytek", ClassRouter}, {"vigor", ClassRouter},
	{"linksys", ClassRouter}, {"wrt", ClassRouter}, {"openwrt", ClassRouter},
	{"luci", ClassRouter}, {"broadcom", ClassRouter}, {"brcm", ClassRouter},
	{"home gateway", ClassRouter}, {"internet box", ClassRouter},
	{"internet x1", ClassRouter}, {"viptel", ClassRouter}, {"gpon", ClassRouter},
	{"rg-series", ClassRouter},
}

// ClassifyDevice adivina router/camera/nas desde el banner; unknown
// cuando no hay token reconocible (banners "Apache/nginx sueltos" que
// el corpus deja etiquetados por su origen real).
func ClassifyDevice(banner string) string {
	n := strings.ToLower(NormalizeBanner(banner))
	for _, r := range classRules {
		if strings.Contains(n, r.token) {
			return r.class
		}
	}
	return ClassUnknown
}

// vendorRule asocia token -> nombre de fabricante normalizado para la
// columna vendor de discovered.
var vendorRules = []classRule{ // reutiliza shape (token, value)
	{"mikrotik", "MikroTik"}, {"tp-link", "TP-Link"}, {"tp_link", "TP-Link"},
	{"tplink", "TP-Link"}, {"zyxel", "ZyXEL"}, {"netgear", "Netgear"},
	{"d-link", "D-Link"}, {"dir-", "D-Link"}, {"dcs-", "D-Link"},
	{"huawei", "Huawei"}, {"zte", "ZTE"}, {"technicolor", "Technicolor"},
	{"speedtouch", "Sagemcom"}, {"speedport", "Siemens"}, {"draytek", "DrayTek"},
	{"vigor", "DrayTek"}, {"linksys", "Linksys"}, {"arris", "ARRIS"},
	{"openwrt", "OpenWrt"}, {"hikvision", "Hikvision"}, {"dahua", "Dahua"},
	{"axis", "AXIS"}, {"bosch", "Bosch"}, {"jvc", "JVC"},
	{"panasonic", "Panasonic"}, {"sentry360", "Sentry360"},
	{"synology", "Synology"}, {"diskstation", "Synology"}, {"qnap", "QNAP"},
	{"buffalo", "Buffalo"}, {"linkstation", "Buffalo"}, {"freenas", "iXsystems"},
	{"asustor", "Asustor"}, {"readynas", "Netgear"}, {"my cloud", "Western Digital"},
	{"thecus", "Thecus"}, {"goahead-webs", "GoAhead"},
}

// Classify extrae el trio completo que consume discovered: clase,
// vendor y model. model es mejor-effort via patrones de SKU conocidos;
// vacío cuando el banner no lo porta.
func Classify(banner string) (class, vendor, model string) {
	class = ClassifyDevice(banner)
	n := strings.ToLower(NormalizeBanner(banner))
	for _, r := range vendorRules {
		if strings.Contains(n, r.token) {
			vendor = r.class // campo "class" = value en vendorRules
			break
		}
	}
	model = extractModel(NormalizeBanner(banner))
	return class, vendor, model
}

var modelPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(TL-[A-Za-z0-9-]{2,15})\b`),
	regexp.MustCompile(`(?i)\b(DIR-[0-9A-Za-z]{2,10})\b`),
	regexp.MustCompile(`(?i)\b(DSL-[0-9A-Za-z]{2,10})\b`),
	regexp.MustCompile(`(?i)\b(DCS-[0-9A-Za-z]{2,10})\b`),
	regexp.MustCompile(`(?i)\b(W[NP]?[DG]?R[0-9]{3}[A-Za-z0-9]*)\b`),
	regexp.MustCompile(`(?i)\b(WG[0-9]{3}[A-Za-z0-9]*)\b`),
	regexp.MustCompile(`(?i)\b(HG[0-9]{2,4}[A-Za-z]{0,6})\b`),
	regexp.MustCompile(`(?i)\b(TG[0-9]{3}[A-Za-z]{0,6})\b`),
	regexp.MustCompile(`(?i)\bVigor\s?([0-9]{3,4}[a-z]?)\b`),
	regexp.MustCompile(`(?i)\bZXHN\s?([A-Z][0-9]{3,4}[A-Za-z]{0,4})\b`),
	regexp.MustCompile(`(?i)\b(DS-[0-9A-Za-z-]{3,15})\b`),
	regexp.MustCompile(`(?i)\b(DHI-\S{3,15})\b`),
	regexp.MustCompile(`(?i)\b(AXIS\s?[0-9]{3,4})\b`),
	regexp.MustCompile(`(?i)\b(WV-[0-9A-Za-z]{3,10})\b`),
	regexp.MustCompile(`(?i)\bCS-[A-Z][0-9]{3}\b`),
}

// extractModel devuelve el SKU mas especifico (mayor longitud de match)
// para que "TL-WR841N v9" no se quede en un token generico.
func extractModel(banner string) string {
	best := ""
	for _, re := range modelPatterns {
		m := re.FindStringSubmatch(banner)
		if len(m) > 1 && len(m[1]) > len(best) {
			best = strings.TrimRight(m[1], "-_")
		}
	}
	return best
}

// ParseGrabsJSONL lee el doble de zgrab2: una linea por host
// {"ip","port","banner"} (testdata/banners/grabs.jsonl).
func ParseGrabsJSONL(r io.Reader) ([]Grab, error) {
	var out []Grab
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 0; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || !strings.HasPrefix(raw, "{") {
			continue
		}
		var g Grab
		if err := json.Unmarshal([]byte(raw), &g); err != nil {
			return nil, fmt.Errorf("discovery: grab line %d: %w", line+1, err)
		}
		if err := (Candidate{IP: g.IP, Port: g.Port}).Validate(); err != nil {
			return nil, fmt.Errorf("discovery: grab line %d: %w", line+1, err)
		}
		out = append(out, g)
	}
	return out, sc.Err()
}

// ParseZgrab2Output lee el JSON real de `zgrab2 run --output=json`:
// claves "tcp/<puerto>" con {status, data:{result:{ip, port,
// data:{banner}}}} segun protocolo. Solo se conservan los status
// "success" con banner no vacio.
func ParseZgrab2Output(r io.Reader) ([]Grab, error) {
	var doc map[string]json.RawMessage
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, fmt.Errorf("discovery: zgrab2 json: %w", err)
	}
	var out []Grab
	for key, raw := range doc {
		var entry struct {
			Status string          `json:"status"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, fmt.Errorf("discovery: zgrab2 %s: %w", key, err)
		}
		if entry.Status != "success" || len(entry.Data) == 0 {
			continue
		}
		g, ok := digZgrab2Banner(entry.Data)
		if !ok || g.Banner == "" {
			continue
		}
		if err := (Candidate{IP: g.IP, Port: g.Port}).Validate(); err != nil {
			return nil, fmt.Errorf("discovery: zgrab2 %s: %w", key, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// digZgrab2Banner baja de data:{result:{ip,port,data:{banner}}} a un
// Grab; el anidado extra "data" es por protocolo y puede faltar.
func digZgrab2Banner(data json.RawMessage) (Grab, bool) {
	var wrap struct {
		Result struct {
			IP   string          `json:"ip"`
			Port int             `json:"port"`
			Data json.RawMessage `json:"data"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return Grab{}, false
	}
	g := Grab{IP: wrap.Result.IP, Port: wrap.Result.Port}
	var inner struct {
		Banner string `json:"banner"`
	}
	if len(wrap.Result.Data) > 0 {
		if err := json.Unmarshal(wrap.Result.Data, &inner); err != nil {
			return Grab{}, false
		}
		g.Banner = inner.Banner
	}
	return g, g.IP != ""
}
