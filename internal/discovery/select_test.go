package discovery

import (
	"strings"
	"testing"
)

// T-0106.2/3: la seleccion debe ordenar por last_seen_at DESC, limitar
// y referir los dos anti-joins (optout_at 12 meses, reprobe bloqueado)
// de forma que el indice parcial (last_seen_at DESC) WHERE service IS
// NOT NULL cubra el escaneo.
func TestSelectQueriesShape(t *testing.T) {
	with := SelectQueryWithOptout()
	basic := SelectQueryBasic()
	for _, frag := range []string{
		"service IS NOT NULL",
		"ORDER BY d.last_seen_at DESC",
		"LIMIT $1",
	} {
		if !strings.Contains(with, frag) || !strings.Contains(basic, frag) {
			t.Errorf("query sin %q", frag)
		}
	}
	for _, frag := range []string{
		"nodes n",
		"optout_at > now() - interval '12 months'",
		"leaves l",
		"reprobe_blocked_until > now()",
	} {
		if !strings.Contains(with, frag) {
			t.Errorf("query con optout sin %q", frag)
		}
		if strings.Contains(basic, frag) {
			t.Errorf("query basica deberia omitir %q", frag)
		}
	}
}

func TestFormatSelectRow(t *testing.T) {
	row := FormatSelectRow("1.2.3.4", 23, "telnet", 4134, "CN")
	if row != "1.2.3.4,23,telnet,4134,CN" {
		t.Fatalf("row %q", row)
	}
	row = FormatSelectRow("1.2.3.4", 80, "http", 0, "")
	if row != "1.2.3.4,80,http,," {
		t.Fatalf("row nulls %q", row)
	}
}

// T-0101.3: el upsert respeta UNIQUE (ip,port): segunda insercion del
// mismo par es UPDATE, no INSERT. La clausula ON CONFLICT debe estar
// dirigida a ese par exacto.
func TestUpsertSQLTargetsUniqueKey(t *testing.T) {
	q, args, err := upsertSQL([]Candidate{
		{IP: "1.2.3.4", Port: 80, Service: "http", Source: "shodan"},
		{IP: "1.2.3.5", Port: 23, Service: "telnet", Source: "shodan"},
	})
	if err != nil {
		t.Fatalf("sql: %v", err)
	}
	if !strings.Contains(q, "ON CONFLICT (ip, port) DO UPDATE") {
		t.Fatal("upsert sin ON CONFLICT (ip, port)")
	}
	if !strings.Contains(q, "last_seen_at = now()") {
		t.Fatal("upsert no renueva last_seen_at")
	}
	for _, c := range []string{"COALESCE(EXCLUDED.service", "COALESCE(EXCLUDED.vendor"} {
		if !strings.Contains(q, c) {
			t.Errorf("upsert sin %s (COALESCE preserva aprendizaje previo)", c)
		}
	}
	// 2 filas x 10 columnas = 20 placeholders; el ultimo debe ser $20
	if len(args) != 20 {
		t.Fatalf("args=%d want 20", len(args))
	}
	if !strings.Contains(q, "$20") || strings.Contains(q, "$21") {
		t.Errorf("numeracion de placeholders rota en %q", q[len(q)-120:])
	}
	if strings.Count(q, "),(") != 1 {
		t.Error("filas multi-VALUES mal separadas")
	}
}
