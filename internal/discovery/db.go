package discovery

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/botnet/botnet/migrations"
	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" para database/sql
)

// Open abre la conexion Postgres via pgx (unica dependencia de BD ya
// fijada en go.mod; sin GORM ni pool extra).
func Open(uri string) (*sql.DB, error) {
	if strings.TrimSpace(uri) == "" {
		return nil, fmt.Errorf("discovery: empty database url (DATABASE_URL)")
	}
	db, err := sql.Open("pgx", uri)
	if err != nil {
		return nil, fmt.Errorf("discovery: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("discovery: ping: %w", err)
	}
	return db, nil
}

// Migrate aplica migrations/*.up.sql (el runner embebido del suelo).
func Migrate(db *sql.DB) error { return migrations.Apply(db) }

// upsertSQL es un multi-VALUES ON CONFLICT (ip, port): una sentencia
// por lote de hasta chunk candidatos. COALESCE preserva lo aprendido
// por otras fuentes (vendor de zgrab2 no lo pisa un masscan ciego) y
// last_seen_at siempre se renueva: idempotencia de T-0102.2.
func upsertSQL(cands []Candidate) (string, []any, error) {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO discovered (ip, port, service, vendor, model, asn, country, device_class_guess, banner_hash, source) VALUES `)
	var args []any
	for i, c := range cands {
		if i > 0 {
			sb.WriteString(",")
		}
		base := i * 10
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10)
		args = append(args, c.IP, c.Port,
			nullStr(c.Service), nullStr(c.Vendor), nullStr(c.Model),
			nullInt(c.ASN), nullStr(c.Country), nullStr(c.DeviceClassGuess),
			nullStr(c.BannerHash), c.Source)
	}
	sb.WriteString(` ON CONFLICT (ip, port) DO UPDATE SET
	  service = COALESCE(EXCLUDED.service, discovered.service),
	  vendor = COALESCE(EXCLUDED.vendor, discovered.vendor),
	  model = COALESCE(EXCLUDED.model, discovered.model),
	  asn = COALESCE(EXCLUDED.asn, discovered.asn),
	  country = COALESCE(EXCLUDED.country, discovered.country),
	  device_class_guess = COALESCE(EXCLUDED.device_class_guess, discovered.device_class_guess),
	  banner_hash = COALESCE(EXCLUDED.banner_hash, discovered.banner_hash),
	  source = EXCLUDED.source,
	  last_seen_at = now()`)
	return sb.String(), args, nil
}

// UpsertCandidates inserta por lotes dentro de una transaccion por
// chunk: 1000 candidatos del fixture caben en ~4 sentencias. Antes del
// batching se fusionan duplicados por (ip, port) porque Postgres
// prohibe que un ON CONFLICT DO UPDATE multi-fila afecte la misma fila
// dos veces (masscan/Shodan pueden repetir un par por retransmision).
func UpsertCandidates(db *sql.DB, cands []Candidate, chunk int) (int, error) {
	cands = dedupeCandidates(cands)
	if chunk <= 0 {
		chunk = 250
	}
	total := 0
	for start := 0; start < len(cands); start += chunk {
		end := start + chunk
		if end > len(cands) {
			end = len(cands)
		}
		q, args, err := upsertSQL(cands[start:end])
		if err != nil {
			return total, err
		}
		res, err := db.Exec(q, args...)
		if err != nil {
			return total, fmt.Errorf("discovery: upsert batch %d: %w", start, err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

// ScalarInt ejecuta una consulta de un entero (counts de los runbooks).
func ScalarInt(db *sql.DB, q string, args ...any) (int64, error) {
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("discovery: scalar: %w", err)
	}
	return n, nil
}

// hasTable sonda to_regclass para elegir SelectQuery segun existan
// nodes/leaves (los crea EP-08; EP-01 no puede depender de su orden).
func hasTable(db *sql.DB, name string) (bool, error) {
	var v *string
	if err := db.QueryRow(`SELECT to_regclass($1)::text`, "public."+name).Scan(&v); err != nil {
		return false, fmt.Errorf("discovery: probe %s: %w", name, err)
	}
	return v != nil && *v != "", nil
}

// SelectCandidates corre la seleccion T-0106 con o sin anti-join.
func SelectCandidates(db *sql.DB, limit int) ([]Candidate, error) {
	// anti-join solo cuando nodes/leaves existen (las crea EP-08).
	nodes, err := hasTable(db, "nodes")
	if err != nil {
		return nil, err
	}
	leaves, err := hasTable(db, "leaves")
	if err != nil {
		return nil, err
	}
	q := SelectQueryBasic()
	if nodes && leaves {
		q = SelectQueryWithOptout()
	}
	rows, err := db.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("discovery: select: %w", err)
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var (
			ip      string
			port    int
			service string
			asn     *int
			country *string
		)
		if err := rows.Scan(&ip, &port, &service, &asn, &country); err != nil {
			return nil, err
		}
		c := Candidate{IP: ip, Port: port, Service: service, Source: "select"}
		if asn != nil {
			c.ASN = *asn
		}
		if country != nil {
			c.Country = strings.TrimSpace(*country)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReportFromDB lee las filas crudas del informe T-0105.
func ReportFromDB(db *sql.DB) ([]ReportRow, error) {
	rows, err := db.Query(`SELECT COALESCE(asn, 0), COALESCE(country, ''), host(ip), COALESCE(device_class_guess, 'unknown') FROM discovered`)
	if err != nil {
		return nil, fmt.Errorf("discovery: report query: %w", err)
	}
	defer rows.Close()
	var out []ReportRow
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.ASN, &r.Country, &r.IP, &r.Class); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GrabsToCandidates traduce banners clasificados a filas discovered con
// hash, clase, vendor y model (T-0104.1).
func GrabsToCandidates(grabs []Grab) []Candidate {
	out := make([]Candidate, 0, len(grabs))
	for _, g := range grabs {
		class, vendor, model := Classify(g.Banner)
		out = append(out, Candidate{
			IP:               g.IP,
			Port:             g.Port,
			Vendor:           vendor,
			Model:            model,
			DeviceClassGuess: class,
			BannerHash:       HashBanner(g.Banner),
			Source:           "zgrab2",
		})
		if svc, ok := InferService(g.Port); ok {
			out[len(out)-1].Service = svc
		}
	}
	return out
}

// ExecFile ejecuta un archivo SQL multi-sentencia por el protocolo
// simple (sin args): lo usa el runner de pruebas de integracion.
func ExecFile(db *sql.DB, script string) error {
	if _, err := db.Exec(script); err != nil {
		return fmt.Errorf("discovery: exec script: %w", err)
	}
	return nil
}

// QueryText corre una consulta arbitraria de una o varias columnas de
// texto y devuelve las filas unidas por coma (el "psql -tA" del
// runbook sin psql instalado).
func QueryText(db *sql.DB, q string) ([]string, error) {
	rows, err := db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("discovery: query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		parts := make([]string, len(cols))
		for i := range vals {
			if vals[i] == nil {
				continue
			}
			parts[i] = fmt.Sprintf("%v", vals[i])
		}
		out = append(out, strings.Join(parts, ","))
	}
	return out, rows.Err()
}

// dedupeCandidates fusion candidatos duplicados por (ip, port): gana
// el ultimo valor no vacio por columna y el source mas reciente.
func dedupeCandidates(cands []Candidate) []Candidate {
	seen := map[[2]any]int{}
	out := cands[:0]
	for _, c := range cands {
		k := [2]any{c.IP, c.Port}
		if i, dup := seen[k]; dup {
			m := &out[i]
			pick := func(dst *string, v string) {
				if strings.TrimSpace(v) != "" {
					*dst = v
				}
			}
			pick(&m.Service, c.Service)
			pick(&m.Vendor, c.Vendor)
			pick(&m.Model, c.Model)
			pick(&m.Country, c.Country)
			pick(&m.DeviceClassGuess, c.DeviceClassGuess)
			pick(&m.BannerHash, c.BannerHash)
			pick(&m.Source, c.Source)
			if c.ASN != 0 {
				m.ASN = c.ASN
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, c)
	}
	return out
}

func nullStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}
