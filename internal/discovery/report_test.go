package discovery

import (
	"bytes"
	"strings"
	"testing"
)

// T-0105.2: dos hosts del mismo /24 cuentan como 1 candidato.
func TestAggregateDedupesBySlash24(t *testing.T) {
	f := openTestdata(t, "testdata/discovery/report_fixture.csv")
	defer f.Close()
	rows, err := ParseReportFixture(f)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("rows=%d want 10", len(rows))
	}
	reps := AggregateReport(rows, 20)
	byASN := map[int]ASNReport{}
	for _, r := range reps {
		byASN[r.ASN] = r
	}
	// 4134: /24 1.20.30 (2 hosts router -> 1) + 1.20.31 (camera) = 2
	if r := byASN[4134]; r.Candidates != 2 || r.ByClass[ClassRouter] != 1 || r.ByClass[ClassCamera] != 1 || r.Country != "CN" {
		t.Errorf("asn 4134: %+v", r)
	}
	// 9829: 201.15.7 (2 nas -> 1) + 201.15.8 (router) = 2
	if r := byASN[9829]; r.Candidates != 2 || r.ByClass[ClassNAS] != 1 || r.ByClass[ClassRouter] != 1 {
		t.Errorf("asn 9829: %+v", r)
	}
	// 17923: un solo /24 con 3 hosts -> 1 candidato, clase mayoritaria camera
	if r := byASN[17923]; r.Candidates != 1 || r.ByClass[ClassCamera] != 1 {
		t.Errorf("asn 17923: %+v", r)
	}
}

// T-0105.3: cabecera fija; filas ordenadas por candidatos desc y ASN asc.
func TestReportCSVStable(t *testing.T) {
	f := openTestdata(t, "testdata/discovery/report_fixture.csv")
	defer f.Close()
	rows, err := ParseReportFixture(f)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	reps := AggregateReport(rows, 20)
	var first, second bytes.Buffer
	if err := WriteReportCSV(&first, reps); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if err := WriteReportCSV(&second, AggregateReport(rows, 20)); err != nil {
		t.Fatalf("csv: %v", err)
	}
	if first.String() != second.String() {
		t.Fatal("salida no determinista")
	}
	lines := strings.Split(strings.TrimRight(first.String(), "\n"), "\n")
	if lines[0] != ReportCSVHeader {
		t.Fatalf("cabecera %q", lines[0])
	}
	if len(lines) != 5 { // 4 ASNs del fixture
		t.Fatalf("filas=%d: %v", len(lines)-1, lines)
	}
	for _, ln := range lines[1:] {
		if strings.Count(ln, ",") != 2 {
			t.Errorf("csv columnas!=3: %q", ln)
		}
	}
	// top por candidatos: los dos ASNs con 2 van primero, empatados por ASN asc
	if got := lines[1]; !strings.HasPrefix(got, "4134,CN,") {
		t.Errorf("orden top-20 roto: %q", got)
	}
}

func TestAggregateTopN(t *testing.T) {
	var rows []ReportRow
	for i := 0; i < 25; i++ {
		rows = append(rows, ReportRow{ASN: 100 + i, Country: "US", IP: "10." + itoa(i) + ".0.1", Class: ClassRouter})
	}
	reps := AggregateReport(rows, 20)
	if len(reps) != 20 {
		t.Fatalf("top-20 roto: %d", len(reps))
	}
	if reps[0].ASN != 100 {
		t.Errorf("empate deberia ordenar por asn asc, primero %d", reps[0].ASN)
	}
}

func TestSlashKeyV6(t *testing.T) {
	k1, ok1 := slashKey("2001:db8:1234::1")
	k2, ok2 := slashKey("2001:db8:1234:0:bad::9")
	if !ok1 || !ok2 || k1 != k2 {
		t.Fatalf("v6 /64 dedupe roto: %s %s", k1, k2)
	}
	other, _ := slashKey("2001:db8:1234:ffff::9")
	if other == k1 {
		t.Fatal("otra /64 no deberia coincidir")
	}
}

func TestParseHostsFile(t *testing.T) {
	f := openTestdata(t, "testdata/banners/hosts.txt")
	defer f.Close()
	hosts, err := ParseHostsFile(f)
	if err != nil {
		t.Fatalf("hosts: %v", err)
	}
	if len(hosts) != 100 {
		t.Fatalf("hosts=%d want 100", len(hosts))
	}
	if _, err := ParseHostsFile(strings.NewReader("sin-puerto\n")); err == nil {
		t.Error("entrada sin puerto deberia error")
	}
}

func itoa(n int) string {
	var b [4]byte
	pos := len(b)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		pos--
		b[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(b[pos:])
}
