package discovery

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
)

// ReportRow es una fila cruda de discovered necesaria para el informe
// T-0105: agregacion por ASN con dedupe por /24.
type ReportRow struct {
	ASN     int
	Country string
	IP      string
	Class   string
}

// ASNReport es el resultado agregado por ASN.
type ASNReport struct {
	ASN        int
	Country    string
	Candidates int // /24 distintos
	ByClass    map[string]int
	Total      int // suma de ByClass (= Candidates)
}

// ReportCSVHeader es la cabecera fija y parseable que exige T-0105.3.
const ReportCSVHeader = "asn,country,candidates_by_class"

// AggregateReport agrupa por ASN: cada /24 cuenta 1 candidato y se
// asigna a la clase mayoritaria del /24 (empate -> orden
// alfabetico), de modo que "dos hosts del mismo /24 cuentan como 1"
// (T-0105.2) sin borrar visibilidad por clase. Top-20 por candidatos,
// empate por ASN ascendente.
func AggregateReport(rows []ReportRow, top int) []ASNReport {
	if top <= 0 {
		top = 20
	}
	type bucket struct {
		by24    map[string]map[string]int // /24 -> clase -> n
		country map[string]int
	}
	asns := map[int]*bucket{}
	for _, r := range rows {
		key, ok := slashKey(r.IP)
		if !ok {
			continue // ip no parseable: sin /24, fuera del conteo
		}
		b := asns[r.ASN]
		if b == nil {
			b = &bucket{by24: map[string]map[string]int{}, country: map[string]int{}}
			asns[r.ASN] = b
		}
		if r.Country != "" {
			b.country[r.Country]++
		}
		cls := r.Class
		if cls == "" {
			cls = ClassUnknown
		}
		if b.by24[key] == nil {
			b.by24[key] = map[string]int{}
		}
		b.by24[key][cls]++
	}
	out := make([]ASNReport, 0, len(asns))
	for asn, b := range asns {
		rep := ASNReport{ASN: asn, ByClass: map[string]int{}}
		for _, cls := range b.by24 {
			dom := dominantClass(cls)
			rep.ByClass[dom]++
			rep.Total++
		}
		rep.Candidates = rep.Total
		rep.Country = dominantKey(b.country)
		out = append(out, rep)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Candidates != out[j].Candidates {
			return out[i].Candidates > out[j].Candidates
		}
		return out[i].ASN < out[j].ASN
	})
	if len(out) > top {
		out = out[:top]
	}
	return out
}

// WriteReportCSV emite cabecera fija + filas ordenadas; el desglose es
// un campo unico "router=..;camera=..;nas=..;unknown=..;total=.." para
// que `head -1` y csvtool vean siempre 3 columnas.
func WriteReportCSV(w io.Writer, reports []ASNReport) error {
	if _, err := io.WriteString(w, ReportCSVHeader+"\n"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	for _, r := range reports {
		var sb strings.Builder
		for _, cls := range []string{ClassRouter, ClassCamera, ClassNAS, ClassUnknown} {
			fmt.Fprintf(&sb, "%s=%d;", cls, r.ByClass[cls])
		}
		fmt.Fprintf(&sb, "total=%d", r.Total)
		if err := cw.Write([]string{
			strconv.Itoa(r.ASN), r.Country, sb.String(),
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// ParseReportFixture lee el CSV de testdata/discovery/report_fixture.csv
// (asn,country,ip,device_class_guess) con cabecera.
func ParseReportFixture(r io.Reader) ([]ReportRow, error) {
	recs, err := csv.NewReader(r).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("discovery: report fixture: %w", err)
	}
	var out []ReportRow
	for i, rec := range recs {
		if i == 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "asn") {
			continue
		}
		if len(rec) < 4 {
			return nil, fmt.Errorf("discovery: report fixture row %d: want 4 cols", i+1)
		}
		asn, err := strconv.Atoi(strings.TrimSpace(rec[0]))
		if err != nil {
			return nil, fmt.Errorf("discovery: report fixture row %d: %w", i+1, err)
		}
		out = append(out, ReportRow{ASN: asn, Country: iso2(rec[1]), IP: strings.TrimSpace(rec[2]), Class: strings.TrimSpace(rec[3])})
	}
	return out, nil
}

// slashKey reduce un IP a su prefijo /24 (v4) o /64 (v6): es la clave
// de dedupe del informe.
func slashKey(ip string) (string, bool) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return "", false
	}
	if v4 := parsed.To4(); v4 != nil {
		return "v4:" + v4[0:3].String(), true
	}
	return "v6:" + parsed[0:8].String(), true
}

func dominantClass(counts map[string]int) string {
	return dominantKey(counts)
}

// dominantKey elige el valor con mas votos; empate -> alfabetico para
// que el informe sea determinista.
func dominantKey(counts map[string]int) string {
	best, bestN := "", -1
	var keys []string
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	return best
}

// ParseHostsFile lee el "ip:port" por linea que consume
// zgrab2_grab.sh --input=hosts.txt.
func ParseHostsFile(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		if _, port, err := net.SplitHostPort(s); err != nil {
			return nil, fmt.Errorf("discovery: hosts entry %q: %w", s, err)
		} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("discovery: hosts entry %q: bad port", s)
		}
		out = append(out, s)
	}
	return out, sc.Err()
}
