// botnet-api — backend Fiber: control, credenciales, contabilidad
// Suelo compilable: la logica real la aportan las epicas; --version y
// --config son los unicos flags comprometidos por el esqueleto.
// Subcomandos EP-08: `migrate` (el "make migrate" del runbook) y
// `serve` (backend real: DB+Redis+NATS desde el entorno).
package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/cmd/botnet-api/handlers"
	"github.com/botnet/botnet/cmd/botnet-api/middleware"
	apinats "github.com/botnet/botnet/cmd/botnet-api/nats"
	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/db"
	"github.com/botnet/botnet/internal/logx"
	"github.com/botnet/botnet/migrations"
	"github.com/botnet/botnet/pkg/logger"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const name = "botnet-api"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "migrate":
			if err := runMigrate(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "serve":
			if err := runServe(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	var (
		showVer     bool
		cfgPath     string
		metricsAddr string
	)
	flag.StringVar(&cfgPath, "config", "", "path to JSON config file")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.StringVar(&metricsAddr, "metrics-addr", "", "servir /metrics aqui (vacio = off)")
	flag.Parse()

	if showVer {
		fmt.Printf("%s %s\n", name, version)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := logx.New("info")
	log.Info(name+" stub: logica pendiente de epica", "endpoint", cfg.Endpoint)
	if metricsAddr != "" {
		m := logger.NewMetrics(name)
		log.Info(name+": sirviendo /metrics", "addr", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, m.Handler()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// runMigrate: DDL solo adelante sobre DATABASE_URL (T-0801).
func runMigrate() error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("%s migrate: DATABASE_URL requerido", name)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := migrations.Apply(sqlDB); err != nil {
		return err
	}
	fmt.Println(name + ": migraciones aplicadas")
	return nil
}

// runServe monta el backend completo: Fiber sobre Postgres+Redis
// (NATS opcional). PORT default 8080.
func runServe() error {
	bcfg := backend.ConfigFromEnv()
	if bcfg.DatabaseURL == "" {
		return fmt.Errorf("%s serve: DATABASE_URL requerido", name)
	}
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, bcfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if err := migrations.Apply(sqlDB); err != nil {
		return fmt.Errorf("%s serve: %w", name, err)
	}
	rOpts, err := redis.ParseURL(bcfg.RedisURL)
	if err != nil {
		return fmt.Errorf("%s serve redis url: %w", name, err)
	}
	rdb := redis.NewClient(rOpts)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%s serve redis ping: %w", name, err)
	}
	ca, err := services.NewCA() // serve local: CA dev; produccion usa CA_CERT_PEM/CA_KEY_PEM
	if err != nil {
		return err
	}
	if bcfg.CACertPEM != "" {
		if ca, err = services.LoadCA(bcfg.CACertPEM, bcfg.CAKeyPEM); err != nil {
			return err
		}
	}
	var jsPub *apinats.Publisher
	if bcfg.NATSURL != "" {
		nc, nerr := nats.Connect(bcfg.NATSURL, nats.Timeout(3*time.Second))
		if nerr != nil {
			return fmt.Errorf("%s serve nats: %w", name, nerr)
		}
		defer nc.Close()
		js, jerr := nc.JetStream()
		if jerr != nil {
			return fmt.Errorf("%s serve jetstream: %w", name, jerr)
		}
		if berr := apinats.Bootstrap(js); berr != nil {
			return fmt.Errorf("%s serve nats bootstrap: %w", name, berr)
		}
		jsPub = &apinats.Publisher{NC: nc, JS: js}
	}
	rp := repo.New(sqlDB)
	keys := backend.NewApiKeyCache(rdb, rp, bcfg.APIKeyCacheTTL)
	period := time.Now().UTC().Format("2006-01-02")
	watch := backend.NewRedisWatch(rdb, rp, period, func() []string {
		ids, _ := rp.ActiveTenants(ctx)
		return ids
	})
	if err := keys.Warmup(ctx); err != nil {
		return fmt.Errorf("%s serve warmup: %w", name, err)
	}
	deps := &handlers.Deps{
		Cfg:   bcfg,
		Repo:  rp,
		RDB:   rdb,
		Keys:  keys,
		Watch: watch,
		CA:    ca,
		Alloc: handlers.InlineAllocator{},
		Log:   &bytes.Buffer{},
		// Ventanas vivas (EP-07 T-0706 / EP-11 T-1101): sin ellas el
		// binario desplegado nunca devuelve 429 — el middleware es
		// pasamanos con Limiter nil.
		LeaveLimiter:       newRateWindow(rdb),
		TransparentLimiter: newRateWindow(rdb),
		// Plano Stripe (T-1104): el secreto HMAC del webhook firmado
		// llega del entorno; vacio = webhook 503 «plano apagado».
		StripeSecret: stripeSecretFromEnv(),
		// Access log: una linea por peticion (ts ip metodo path
		// status duracion). Criterio EP-11: grep -c $TOKEN del
		// dueño sobre este fichero da 0 — la credencial no viaja
		// por la URL.
		AccessLog: accessLogFromEnv(),
	}
	if jsPub != nil {
		deps.JS = jsPub
	}
	if deps.AccessLog != nil {
		defer deps.AccessLog.Close()
	}
	app := handlers.NewApp(deps)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return app.Listen(":" + port)
}

// newRateWindow: ventana fija Redis 60 req/60 s por clave (rate:leave:*,
// rate:transparency:*) — la implementacion de produccion del Limiter
// que los tests corren con FixedWindow.
func newRateWindow(rdb *redis.Client) *middleware.RedisWindow {
	return &middleware.RedisWindow{
		RDB:    rdb,
		Limit:  middleware.LeaveRateLimitPerWindow,
		Window: middleware.LeaveWindow,
	}
}

// stripeSecretFromEnv: STRIPE_SECRET es la puerta canonica; se acepta
// tambien STRIPE_WEBHOOK_SECRET (nomenclatura Stripe) como alias.
func stripeSecretFromEnv() string {
	if s := os.Getenv("STRIPE_SECRET"); s != "" {
		return s
	}
	return os.Getenv("STRIPE_WEBHOOK_SECRET")
}

// accessLogFromEnv: BOTNET_ACCESS_LOG es la ruta del access.log (p.
// ej. /var/log/botnet-api/access.log). Vacio o fichero que no se
// puede abrir = registro apagado: el backend sigue sirviendo sin
// perdida de servicio.
func accessLogFromEnv() *os.File {
	path := os.Getenv("BOTNET_ACCESS_LOG")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return nil
	}
	return f
}
