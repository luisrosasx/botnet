// botnet-gw — gateway backconnect: clientes, pools, streams H2
// `--serve` cablea las piezas vivas del gateway (EP-08 T-0803/T-0804):
// el CRL en Redis con refresco de 55 s (gatewayCRL), la validacion de
// api keys con warmup contra apikey:{sha256} (gatewayApiKeyCache) y el
// drenaje con GOAWAY sobre las sesiones abiertas del propio gateway
// (Drainer). Sin logica de negocio aqui: las piezas viven en
// internal/backend y el repo del plano de control (§9).
// --metrics-addr abre el /metrics Prometheus del componente (T-1201).
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/db"
	"github.com/botnet/botnet/internal/logx"
	"github.com/botnet/botnet/pkg/logger"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const name = "botnet-gw"

// DrainPoll: cadencia del vigilante de la bandera gw:drain:{id}. El
// presupuesto del drenaje (DrainBudget, 60 s) manda sobre esta mira.
const DrainPoll = 2 * time.Second

func main() {
	var (
		showVer     bool
		cfgPath     string
		metricsAddr string
		serve       bool
		gwID        string
	)
	flag.StringVar(&cfgPath, "config", "", "path to JSON config file")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.StringVar(&metricsAddr, "metrics-addr", "", "servir /metrics aqui (vacio = off)")
	flag.BoolVar(&serve, "serve", false, "gateway vivo: CRL, cache de api keys y drenaje GOAWAY (EP-08)")
	flag.StringVar(&gwID, "gw-id", envOr("GW_ID", "gw-local"), "id del gateway (clave gw:drain:{id})")
	flag.Parse()

	if showVer {
		fmt.Printf("%s %s\n", name, version)
		return
	}
	log := logx.New("info")
	if serve {
		if err := runServe(log, metricsAddr, gwID); err != nil {
			log.Error(name+" serve", "err", err)
			os.Exit(1)
		}
		return
	}
	// Suelo sin --serve (metrics-smoke T-1201): solo /metrics.
	if _, err := config.Load(cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if metricsAddr != "" {
		m := logger.NewMetrics(name)
		log.Info(name+": sirviendo /metrics", "addr", metricsAddr)
		if err := http.ListenAndServe(metricsAddr, m.Handler()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// runServe arranca el gateway vivo: Redis+Postgres del entorno, CRL con
// PollOnce inmediato + refresco de 55 s, warmup del cache de api keys y
// el vigilante de drenaje. Bloquea hasta senal de apagado.
func runServe(log *slog.Logger, metricsAddr, gwID string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rOpts, err := redis.ParseURL(envOr("REDIS_URL", "redis://127.0.0.1:6379"))
	if err != nil {
		return fmt.Errorf("%s serve redis url: %w", name, err)
	}
	rdb := redis.NewClient(rOpts)
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%s serve redis ping: %w", name, err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("%s serve: DATABASE_URL requerido", name)
	}
	sqlDB, err := db.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	return gwServe(ctx, log, rdb, sqlDB, repo.New(sqlDB), gwID, metricsAddr)
}

// gwServe es la mitad testable del arranque: conecta las piezas del
// contrato EP-08 sobre infraestructura ya abierta.
func gwServe(ctx context.Context, log *slog.Logger, rdb *redis.Client, sqlDB *sql.DB, rp *repo.Data, gwID, metricsAddr string) error {
	crl := gatewayCRL(rdb)
	if err := crl.PollOnce(ctx); err != nil {
		return fmt.Errorf("%s serve crl: %w", name, err)
	}
	go crl.PollLoop(ctx)

	keys := gatewayApiKeyCache(rdb, rp)
	if err := keys.Warmup(ctx); err != nil {
		return fmt.Errorf("%s serve warmup: %w", name, err)
	}

	drainer := &Drainer{
		GWID:  gwID,
		RDB:   rdb,
		Store: newGWSessions(sqlDB),
		Step:  50,
		Goaway: func(sessionID string) {
			log.Info("GOAWAY session", "session_id", sessionID, "gw_id", gwID)
		},
	}
	log.Info(name+" serve: piezas cableadas",
		"gw_id", gwID, "crl_refresh", crl.Interval().String(),
		"apikey_cache", "warmup ok", "apikey_warm", keys.Warmed())

	if metricsAddr != "" {
		m := logger.NewMetrics(name)
		go func() {
			log.Info(name+": sirviendo /metrics", "addr", metricsAddr)
			if err := http.ListenAndServe(metricsAddr, m.Handler()); err != nil {
				log.Error(name+" /metrics", "err", err)
			}
		}()
	}
	return drainWatch(ctx, drainer, log)
}

// drainWatch vigila la bandera gw:drain:{id}: cuando sube, drena con
// GOAWAY las sesiones vivas del gateway (0 huerfanos dentro del
// presupuesto) y sigue vivo por si la bandera baja. Vuelve solo con ctx
// cancelado.
func drainWatch(ctx context.Context, d *Drainer, log *slog.Logger) error {
	tick := time.NewTicker(DrainPoll)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !d.Draining(ctx) {
				continue
			}
			start := time.Now()
			remaining, err := d.DrainOnce(ctx)
			log.Info("gw drenado", "gw_id", d.GWID, "sesiones_vivas", remaining,
				"duracion_s", time.Since(start).Round(time.Millisecond).String(), "err", err)
			if remaining == 0 {
				log.Info("gw: 0 huerfanos tras el GOAWAY", "gw_id", d.GWID)
			}
		}
	}
}

// gwSessions es la cara `sessions` que consume el Drainer: las sesiones
// abiertas del gateway son sus streams vivos (ended_at IS NULL AND
// gw_id=$1); al cerrarlas queda closed_reason='goaway' (T-0803 AC2).
type gwSessions struct {
	db *sql.DB
}

func newGWSessions(db *sql.DB) *gwSessions { return &gwSessions{db: db} }

func (s *gwSessions) OpenSessions(gwID string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx,
		`SELECT session_id::text FROM sessions WHERE ended_at IS NULL AND gw_id=$1 ORDER BY session_id`, gwID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var sid string
		if rows.Scan(&sid) == nil {
			out = append(out, sid)
		}
	}
	return out
}

func (s *gwSessions) EndSession(ctx context.Context, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET ended_at=now(), closed_reason='goaway'
		  WHERE session_id=$1::bigint AND ended_at IS NULL`, sessionID)
	return err
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
