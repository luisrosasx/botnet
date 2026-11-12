// Cableado vivo del gateway (EP-08 T-0803 AC2/T-0804): gatewayCRL y
// gatewayApiKeyCache trabajan desde gwServe, y la bandera gw:drain:{id}
// drena con GOAWAY las sesiones abiertas reales del gateway (streams
// vivos en `sessions`) dentro del presupuesto. Requiere Postgres+Redis
// (testenv skip si faltan).
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/db"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func gwLog(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// liveGwDB abre la base del entorno con reintentos: el Postgres portable
// del host pierde ~50 % de las conexiones nuevas (nota de la
// verificacion EP-08); el doble scratch de testenv multiplica esos
// intentos. La base ya viene migrada por `botnet-api migrate/serve`.
func liveGwDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL no definido: Postgres del entorno ausente")
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		db, err := db.Open(context.Background(), dsn)
		if err == nil {
			if err := db.Ping(); err == nil {
				return db
			}
			lastErr = err
			db.Close()
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Skipf("Postgres del entorno no alcanzable tras 5 intentos: %v", lastErr)
	return nil
}

// insertLiveSession inserta una sesion abierta del gateway (el stream
// vivo del criterio) y devuelve su session_id.
func insertLiveSession(t *testing.T, db *sql.DB, ctx context.Context, nodeID, tenantID, gwID string) int64 {
	t.Helper()
	var sid int64
	err := db.QueryRowContext(ctx,
		`INSERT INTO sessions (node_id, tenant_id, gw_id)
		 VALUES ($1::uuid, $2::uuid, $3) RETURNING session_id`,
		nodeID, tenantID, gwID).Scan(&sid)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return sid
}

// gatewayCRL y gatewayApiKeyCache, cableados como los usa gwServe: el
// serial revocado se ve en la puerta tras un PollOnce y la key
// calentada se lee desde Redis (0 queries).
func TestGWServeCRLAndApiKeyCache(t *testing.T) {
	rdb := testenv.Redis(t)
	db := liveGwDB(t)
	ctx := context.Background()

	tenant := testenv.SeedTenant(t, db, "gw-serve-tenant")
	plain := fmt.Sprintf("bot_gwkey_%d", time.Now().UnixNano())
	hash := testenv.SeedKey(t, db, tenant, plain)

	crl := gatewayCRL(rdb)
	if err := crl.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := backend.MarkRevoked(ctx, rdb, "SERIAL-GW-TEST", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := crl.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !crl.Revoked("SERIAL-GW-TEST") {
		t.Error("gatewayCRL no vio revoked:{serial} tras el refresco")
	}

	keys := gatewayApiKeyCache(rdb, repo.New(db))
	if err := keys.Warmup(ctx); err != nil {
		t.Fatal(err)
	}
	if !keys.Warmed() {
		t.Error("warmup no marcho el cache")
	}
	if v, err := rdb.Get(ctx, "apikey:"+hash).Result(); err != nil || v == "" {
		t.Errorf("apikey:{sha256} no quedó en Redis por el warmup: %v", err)
	}
	info, err := keys.Validate(ctx, plain)
	if err != nil || info.TenantID != tenant {
		t.Errorf("Validate tras warmup: %v (%+v)", err, info)
	}
}

// T-0803 AC2: con sesiones abiertas REALES del gateway en Postgres, la
// bandera gw:drain:{id} activa el drenaje GOAWAY de gwServe: el log
// porta GOAWAY, la fila queda closed_reason='goaway' con ended_at y
// 0 streams vivos. El ctx del monitor se cancela al terminar.
func TestGWServeDrainGOAWAYStreamVivo(t *testing.T) {
	rdb := testenv.Redis(t)
	db := liveGwDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tenant := testenv.SeedTenant(t, db, "gw-drain-tenant")
	node := seedGwNode(t, db)
	gwID := "gw-es-mad-test"
	sids := make([]int64, 0, 3)
	for i := 0; i < 3; i++ {
		sids = append(sids, insertLiveSession(t, db, ctx, node, tenant, gwID))
	}

	var logMu sync.Mutex
	logBuf := &bytes.Buffer{}
	drainer := &Drainer{GWID: gwID, RDB: rdb, Store: newGWSessions(db), Step: 50,
		Goaway: func(sid string) {
			logMu.Lock()
			logBuf.WriteString("GOAWAY " + sid + "\n")
			logMu.Unlock()
		}}
	if _, err := rdb.Set(ctx, DrainKey(gwID), "1", 0).Result(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := drainWatch(ctx, drainer, gwLog(&bytes.Buffer{}))
		_ = err
	}()

	deadline := time.Now().Add(15 * time.Second)
	var open int
	for time.Now().Before(deadline) {
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM sessions WHERE ended_at IS NULL AND gw_id=$1`, gwID).Scan(&open); err != nil {
			t.Fatal(err)
		}
		if open == 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if open != 0 {
		t.Fatalf("quedan %d sesiones vivas tras el drenaje, want 0", open)
	}
	logMu.Lock()
	got := logBuf.String()
	logMu.Unlock()
	if !containsGOAWAY(got, len(sids)) {
		t.Errorf("GOAWAY por stream vivo no emitido: %q", got)
	}
	var reason string
	var ended bool
	for _, sid := range sids {
		if err := db.QueryRowContext(ctx,
			`SELECT closed_reason, ended_at IS NOT NULL FROM sessions WHERE session_id=$1`, sid).Scan(&reason, &ended); err != nil {
			t.Fatal(err)
		}
		if reason != "goaway" || !ended {
			t.Errorf("session %d: closed_reason=%q ended=%v, want goaway/true", sid, reason, ended)
		}
	}
	cancel()

	// gwServe entero: CRL + warmup + monitor, apagado con ctx cancelado.
	buf := &bytes.Buffer{}
	sctx, scancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gwServe(sctx, gwLog(buf), rdb, db, repo.New(db), gwID, "") }()
	time.Sleep(500 * time.Millisecond)
	scancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("gwServe debio volver con ctx cancelado: %v", err)
	}
	var line map[string]any
	for _, ln := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(ln) > 0 && json.Unmarshal(ln, &line) == nil {
			break
		}
	}
	if line["msg"] == nil {
		t.Error("gwServe no registro el cableado en el log")
	}
}

// seedGwNode inserta un nodo live con slug y token hash unicos por
// ejecucion (la base del entorno es compartida y los contadores del
// fixture de testenv colisionan entre runs).
func seedGwNode(t *testing.T, db *sql.DB) string {
	t.Helper()
	ctx := context.Background()
	id, err := backend.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("gw-serve-" + id))
	slug := fmt.Sprintf("g%011d", time.Now().UnixNano()%100000000000)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class,
		                   country, city, asn, status, consent_at, last_seen_at)
		VALUES ($1::uuid, $2, $3, 'amd64', 'B', 'ES', 'Madrid', 12345, 'live', now(), now())`,
		id, slug, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("seedGwNode: %v", err)
	}
	return id
}

func containsGOAWAY(s string, want int) bool {
	return strings.Count(s, "GOAWAY ") >= want
}
