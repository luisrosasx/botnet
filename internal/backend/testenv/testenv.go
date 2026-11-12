// Package testenv: motores de prueba del backend sobre este host —
// Postgres portable 16.4 (EP-01, DATABASE_URL), redis-server y
// nats-server portables en ~/tools. Si el motor no esta, el test se
// omite con la limitacion anotada (mismo patron que EP-01/EP-04).
package testenv

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/migrations"
)

const (
	RedisTestPort = "16379"
	NatsTestPort  = "14233"
)

// RedisServerBin: REDIS_SERVER_BIN manda; si no, la ruta portable del
// host (Redis 7.4.3 msys2). Vacio = motor ausente.
func RedisServerBin() string {
	if v := os.Getenv("REDIS_SERVER_BIN"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Windows portable primero; en Linux/CI el binario está en PATH.
	cand := filepath.Join(home, "tools", "redis", "Redis-7.4.3-Windows-x64-msys2", "redis-server.exe")
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	if p, err := exec.LookPath("redis-server"); err == nil {
		return p
	}
	return cand
}

// NatsServerBin: equivalente para nats-server (JetStream incluido).
func NatsServerBin() string {
	if v := os.Getenv("NATS_SERVER_BIN"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "tools", "nats-server", "nats-server-v2.15.2-windows-amd64", "nats-server.exe")
}

// PG crea una base scratch con las migraciones aplicadas y la devuelve
// abierta. Skip si DATABASE_URL no esta (el motor vive fuera del test).
func PG(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL no definido: Postgres portable ausente")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("testenv PG admin: %v", err)
	}
	defer admin.Close()
	seq := atomic.AddUint64(&scratchSeq, 1)
	name := fmt.Sprintf("botnet_ep08_%d_%d", os.Getpid(), seq)
	// 90 s: con el dial caido del motor portable un intento cuesta el
	// timeout TCP del host (~20 s); los reintentos de execRetry caben.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := execRetry(ctx, admin, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatalf("testenv PG drop: %v", err)
	}
	if err := execRetry(ctx, admin, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("testenv PG create: %v", err)
	}
	t.Cleanup(func() {
		c, err := sql.Open("pgx", dsn)
		if err == nil {
			c.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close()
		}
	})
	db, err := sql.Open("pgx", replaceDBName(dsn, name))
	if err != nil {
		t.Fatalf("testenv PG open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// El Postgres portable del host tiene ventanas de dial-timeout
	// transitorias (las documenta ep08_ddl_test): el migrate se reintenta
	// acotado para que el test no se ponga rojo por el motor. Apply es
	// idempotente por version, asi que reintentar es seguro.
	for attempt := 0; ; attempt++ {
		err = migrations.Apply(db)
		if err == nil || attempt >= 5 || !isEngineDial(err) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 400 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("testenv PG migrate: %v", err)
	}
	return db
}

// execRetry: DDL administrativo del scratch con reintento acotado — las
// ventanas de dial-timeout del motor portable (ep08_ddl_test) sin
// reintento ponen rojo el test por el motor, no por el contrato.
func execRetry(ctx context.Context, db *sql.DB, q string) error {
	var err error
	for i := 0; i < 6; i++ {
		if _, err = db.ExecContext(ctx, q); err == nil || strings.Contains(err.Error(), "already exists") {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 400 * time.Millisecond)
	}
	return err
}

// isEngineDial: errores de conexion del motor portable (dial caido,
// bad connection, conn closed) — transitorios por documentacion del
// repo; cualquier otro error es de verdad y el test debe fallar.
func isEngineDial(err error) bool {
	msg := err.Error()
	for _, needle := range []string{"failed to connect", "dial tcp", "bad connection", "conn closed", "context deadline exceeded"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

var scratchSeq uint64

// replaceDBName troca la base del DSN conservando el esquema y los
// params: postgres://u@h:p/OLD?x=1 -> postgres://u@h:p/NEW?x=1.
func replaceDBName(dsn, name string) string {
	params := ""
	base := dsn
	if i := strings.LastIndex(dsn, "?"); i > 0 {
		params, base = dsn[i:], dsn[:i]
	}
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[:i] + "/" + name
	}
	return base + params
}

// Redis devuelve un cliente sobre un redis-server local: reutiliza el
// que ya escucha en RedisTestPort (o REDIS_URL) y, si no hay ninguno,
// arranca el portable y lo mata al terminar el test. Skip sin motor.
// Usa DB 9 para no pisar el db0 de un redis compartido.
func Redis(t *testing.T) *redis.Client {
	t.Helper()
	if u := os.Getenv("REDIS_URL"); u != "" {
		client := redis.NewClient(&redis.Options{Addr: hostPortFromURL(u), DB: 9})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			t.Skipf("REDIS_URL=%s inalcanzable: %v", u, err)
		}
		client.FlushDB(context.Background())
		t.Cleanup(func() { client.FlushDB(context.Background()); client.Close() })
		return client
	}
	addr := RedisTestPort
	bin := RedisServerBin()
	if !dialOpen("127.0.0.1:" + addr) {
		if bin == "" {
			t.Skip("redis-server portable ausente (REDIS_SERVER_BIN) y nada en 127.0.0.1:" + addr)
		}
		cmd := exec.Command(bin, "--port", addr, "--save", "", "--appendonly", "no", "--bind", "127.0.0.1")
		if err := cmd.Start(); err != nil {
			t.Skipf("arrancar redis-server (%s): %v", bin, err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		deadline := time.Now().Add(3 * time.Second)
		for !dialOpen("127.0.0.1:" + addr) {
			if time.Now().After(deadline) {
				t.Skipf("redis-server no escucha en %s", addr)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:" + addr, DB: 9})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("ping redis 127.0.0.1:%s db9: %v", addr, err)
	}
	client.FlushDB(context.Background())
	t.Cleanup(func() { client.FlushDB(context.Background()); client.Close() })
	return client
}

// NATS abre JetStream contra un nats-server local (NATS_URL o el
// portable en NatsTestPort). Skip sin motor.
func NATS(t *testing.T) (*nats.Conn, nats.JetStreamContext) {
	t.Helper()
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = "nats://127.0.0.1:" + NatsTestPort
		if !dialOpen("127.0.0.1:" + NatsTestPort) {
			bin := NatsServerBin()
			if bin == "" {
				t.Skip("nats-server portable ausente (NATS_SERVER_BIN) y nada en 127.0.0.1:" + NatsTestPort)
			}
			sdir := t.TempDir()
			cmd := exec.Command(bin, "-js", "-p", NatsTestPort, "-sd", sdir)
			if err := cmd.Start(); err != nil {
				t.Skipf("arrancar nats-server (%s): %v", bin, err)
			}
			t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
			deadline := time.Now().Add(3 * time.Second)
			for !dialOpen("127.0.0.1:" + NatsTestPort) {
				if time.Now().After(deadline) {
					t.Skipf("nats-server no escucha en %s", NatsTestPort)
				}
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	nc, err := nats.Connect(url, nats.Timeout(2*time.Second))
	if err != nil {
		t.Skipf("conectar NATS %s: %v", url, err)
	}
	js, err := nc.JetStream()
	if err != nil {
		t.Skipf("JetStream: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc, js
}

func dialOpen(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// hostPortFromURL: redis://host:port/db -> host:port.
func hostPortFromURL(u string) string {
	s := strings.TrimPrefix(u, "redis://")
	s = strings.TrimPrefix(s, "rediss://")
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}
