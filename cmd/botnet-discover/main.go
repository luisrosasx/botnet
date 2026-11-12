// botnet-discover — pipeline de descubrimiento e inventario (EP-01).
// Subcomandos: migrate, ingest-shodan, sweep-parse, classify, report,
// select, query, sql-file, stub-api. --version y --config conservan la
// firma del suelo; sin subcomando el binario se comporta como stub.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/discovery"
	"github.com/botnet/botnet/internal/logx"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const name = "botnet-discover"

func main() {
	args := os.Args[1:]
	var (
		showVer bool
		cfgPath string
	)
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.StringVar(&cfgPath, "config", "", "path to JSON config file")
	fs.BoolVar(&showVer, "version", false, "print version and exit")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fs.Parse(args)
		if showVer {
			fmt.Printf("%s %s\n", name, version)
			return
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fatal(err)
		}
		logx.New("info").Info(name+" stub: sin subcomando", "endpoint", cfg.Endpoint)
		return
	}
	if err := run(args[0], args[1:]); err != nil {
		fatal(err)
	}
}

func run(cmd string, args []string) error {
	switch cmd {
	case "migrate":
		return cmdMigrate(args)
	case "ingest-shodan":
		return cmdIngestShodan(args)
	case "sweep-parse":
		return cmdSweepParse(args)
	case "classify":
		return cmdClassify(args)
	case "report":
		return cmdReport(args)
	case "select":
		return cmdSelect(args)
	case "query":
		return cmdQuery(args)
	case "sql-file":
		return cmdSQLFile(args)
	case "stub-api":
		return cmdStubAPI(args)
	default:
		return fmt.Errorf("%s: subcomando desconocido %q", name, cmd)
	}
}

// --- flags comunes -----------------------------------------------------

func newFlagSet(fs *flag.FlagSet) *string {
	db := fs.String("db", os.Getenv("DATABASE_URL"), "postgres uri (DATABASE_URL)")
	return db
}

// --- comandos ----------------------------------------------------------

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := discovery.Migrate(db); err != nil {
		return err
	}
	fmt.Println("migrate: ok (schema_migrations al dia)")
	return nil
}

func cmdIngestShodan(args []string) error {
	fs := flag.NewFlagSet("ingest-shodan", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	fixture := fs.String("fixture", "", "shodan search JSON file")
	apiBase := fs.String("api-base", envOr("SHODAN_API_BASE", "https://api.shodan.io"), "shodan API base URL")
	key := fs.String("key", os.Getenv("SHODAN_KEY"), "shodan API key")
	limit := fs.Int("limit", envInt("LIMIT", 100), "max results per run")
	query := fs.String("query", envOr("SHODAN_QUERY", "port:23,80,8080,554,32210"), "shodan search query")
	chunk := fs.Int("chunk", 250, "rows per upsert statement")
	dry := fs.Bool("dry", false, "parse only, no database writes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var (
		cands []discovery.Candidate
		err   error
	)
	switch {
	case *fixture != "":
		f, ferr := os.Open(*fixture)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		cands, err = discovery.ParseShodanSearch(f)
	case *key != "":
		cands, err = fetchShodan(*apiBase, *query, *key, *limit)
	default:
		return fmt.Errorf("ingest-shodan: FIXTURE o SHODAN_KEY requerido")
	}
	if err != nil {
		return err
	}
	fmt.Printf("processed=%d\n", len(cands))
	if *dry {
		fmt.Println("dry=1 upserted=0")
		return nil
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	n, err := discovery.UpsertCandidates(db, cands, *chunk)
	if err != nil {
		return err
	}
	fmt.Printf("upserted=%d\n", n)
	return nil
}

func fetchShodan(base, query, key string, limit int) ([]discovery.Candidate, error) {
	url := strings.TrimRight(base, "/") + "/shodan/search?query=" + urlQueryEscape(query) +
		fmt.Sprintf("&limit=%d&key=%s", limit, urlQueryEscape(key))
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("shodan fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("shodan fetch: http %d", resp.StatusCode)
	}
	return discovery.ParseShodanSearch(resp.Body)
}

func urlQueryEscape(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == ' ':
			sb.WriteString("%20")
		case r == ':', r == ',':
			sb.WriteString(fmt.Sprintf("%%%X", r))
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func cmdSweepParse(args []string) error {
	fs := flag.NewFlagSet("sweep-parse", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	jsonPath := fs.String("json", "", "masscan NDJSON file ('-' = stdin)")
	chunk := fs.Int("chunk", 250, "rows per upsert statement")
	dry := fs.Bool("dry", false, "parse only, no database writes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var r io.Reader = os.Stdin
	if *jsonPath != "" && *jsonPath != "-" {
		f, err := os.Open(*jsonPath)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	cands, err := discovery.ParseMasscanNDJSON(r)
	if err != nil {
		return err
	}
	fmt.Printf("processed=%d\n", len(cands))
	if *dry {
		fmt.Println("dry=1 upserted=0")
		return nil
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	n, err := discovery.UpsertCandidates(db, cands, *chunk)
	if err != nil {
		return err
	}
	fmt.Printf("upserted=%d\n", n)
	return nil
}

func cmdClassify(args []string) error {
	fs := flag.NewFlagSet("classify", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	grabs := fs.String("grabs", "", "JSONL {ip,port,banner} (doble de zgrab2)")
	zgrab2 := fs.String("zgrab2", "", "JSON real de `zgrab2 run --output=json`")
	score := fs.String("score", "", "corpus JSONL {banner,label} para scoring")
	threshold := fs.Float64("threshold", 0.70, "accuracy minima exigida")
	emit := fs.Bool("emit", false, "imprimir JSON por host {banner_hash,device_class_guess}")
	chunk := fs.Int("chunk", 250, "rows per upsert statement")
	dry := fs.Bool("dry", false, "no escribir en BD")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *score != "" {
		return scoreCorpus(*score, *threshold)
	}
	var (
		gs  []discovery.Grab
		err error
	)
	switch {
	case *grabs != "":
		f, ferr := os.Open(*grabs)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		gs, err = discovery.ParseGrabsJSONL(f)
	case *zgrab2 != "":
		f, ferr := os.Open(*zgrab2)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		gs, err = discovery.ParseZgrab2Output(f)
	default:
		return fmt.Errorf("classify: --grabs o --zgrab2 requerido")
	}
	if err != nil {
		return err
	}
	cands := discovery.GrabsToCandidates(gs)
	if *emit {
		for _, c := range cands {
			b, merr := marshalLine(c)
			if merr != nil {
				return merr
			}
			fmt.Println(b)
		}
	}
	fmt.Printf("processed=%d\n", len(cands))
	if *dry {
		fmt.Println("dry=1 upserted=0")
		return nil
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	n, err := discovery.UpsertCandidates(db, cands, *chunk)
	if err != nil {
		return err
	}
	fmt.Printf("upserted=%d\n", n)
	return nil
}

func scoreCorpus(path string, threshold float64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var total, correct int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || !strings.HasPrefix(raw, "{") {
			continue
		}
		var rec struct {
			Banner string `json:"banner"`
			Label  string `json:"label"`
		}
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return fmt.Errorf("score corpus: %w", err)
		}
		total++
		if discovery.ClassifyDevice(rec.Banner) == rec.Label {
			correct++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	accuracy := 0.0
	if total > 0 {
		accuracy = float64(correct) / float64(total)
	}
	fmt.Printf("accuracy=%.3f correct=%d total=%d threshold=%.2f\n", accuracy, correct, total, threshold)
	if accuracy < threshold {
		return fmt.Errorf("classify: accuracy %.3f < threshold %.2f", accuracy, threshold)
	}
	return nil
}

func cmdReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	fixture := fs.String("fixture", "", "CSV asn,country,ip,device_class_guess (modo fixture)")
	top := fs.Int("top", 20, "top N ASN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var rows []discovery.ReportRow
	if *fixture != "" {
		f, err := os.Open(*fixture)
		if err != nil {
			return err
		}
		defer f.Close()
		rows, err = discovery.ParseReportFixture(f)
		if err != nil {
			return err
		}
	} else {
		db, err := discovery.Open(*dbURL)
		if err != nil {
			return err
		}
		defer db.Close()
		rows, err = discovery.ReportFromDB(db)
		if err != nil {
			return err
		}
	}
	return discovery.WriteReportCSV(os.Stdout, discovery.AggregateReport(rows, *top))
}

func cmdSelect(args []string) error {
	fs := flag.NewFlagSet("select", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	limit := fs.Int("limit", envInt("LIMIT", 100), "max filas")
	if err := fs.Parse(args); err != nil {
		return err
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := discovery.SelectCandidates(db, *limit)
	if err != nil {
		return err
	}
	for _, c := range rows {
		fmt.Println(discovery.FormatSelectRow(c.IP, c.Port, c.Service, c.ASN, c.Country))
	}
	return nil
}

func cmdQuery(args []string) error {
	fs := flag.NewFlagSet("query", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	q := fs.String("sql", "", "consulta de lectura")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *q == "" {
		return fmt.Errorf("query: --sql requerido")
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := discovery.QueryText(db, *q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fmt.Println(r)
	}
	return nil
}

func cmdSQLFile(args []string) error {
	fs := flag.NewFlagSet("sql-file", flag.ExitOnError)
	dbURL := newFlagSet(fs)
	file := fs.String("file", "", "archivo SQL multi-sentencia")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	db, err := discovery.Open(*dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := discovery.ExecFile(db, string(raw)); err != nil {
		return err
	}
	fmt.Println("sql-file: ok")
	return nil
}

// cmdStubAPI sirve el fixture Shodan respetando ?limit=: el doble de
// SHODAN_API_BASE para T-0102.3 sin clave real ni red.
func cmdStubAPI(args []string) error {
	fs := flag.NewFlagSet("stub-api", flag.ExitOnError)
	fixture := fs.String("fixture", "", "shodan search JSON a servir")
	addr := fs.String("addr", "127.0.0.1:0", "host:puerto de escucha")
	if err := fs.Parse(args); err != nil {
		return err
	}
	raw, err := os.ReadFile(*fixture)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer ln.Close()
	fmt.Printf("addr=%s\n", ln.Addr().String())
	return http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reempaqueta el fixture original (preserva el campo "ip"
		// numerico de Shodan) respetando ?limit=N.
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(doc["matches"], &arr); err != nil {
			json.Unmarshal(doc["results"], &arr)
		}
		if lim := r.URL.Query().Get("limit"); lim != "" {
			if n, cerr := strconv.Atoi(lim); cerr == nil && n < len(arr) {
				arr = arr[:n]
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"total": len(arr), "matches": arr})
	}))
}

// --- utilidades ---------------------------------------------------------

func marshalLine(c discovery.Candidate) (string, error) {
	var sb strings.Builder
	sb.WriteString(`{"ip":"` + c.IP + `","port":` + fmt.Sprint(c.Port))
	fmt.Fprintf(&sb, `,"banner_hash":"%s"`, c.BannerHash)
	fmt.Fprintf(&sb, `,"device_class_guess":"%s"`, c.DeviceClassGuess)
	fmt.Fprintf(&sb, `,"vendor":"%s"`, c.Vendor)
	fmt.Fprintf(&sb, `,"model":"%s"`, c.Model)
	sb.WriteString(`,"source":"` + c.Source + `"}`)
	return sb.String(), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, name+":", err)
	os.Exit(1)
}
