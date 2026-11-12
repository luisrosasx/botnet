// Test del cache de api keys del gateway (T-0804): gateway frio =
// exactamente 1 query a api_keys y luego apikey:{sha256} en Redis;
// warmup al arrancar con /healthz de puerta; key revocada cae al
// expirar el cache. Medida determinista con el contador db.Counted
// (pg_stat_statements no esta precargado en el cluster portable del
// host — limitacion anotada: el contador cuenta la misma ida).
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/handlers"
	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
	"github.com/botnet/botnet/internal/db"
)

func TestApiKeyCacheOneQueryColdThenZero(t *testing.T) {
	pgDB := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()

	tenant := testenv.SeedTenant(t, pgDB, "cache-tenant")
	for i := 0; i < 10; i++ {
		testenv.SeedKey(t, pgDB, tenant, "warm-key-"+string(rune('A'+i)))
	}
	counted := &db.Counted{Q: pgDB}
	cache := gatewayApiKeyCache(rdb, repo.New(counted))

	if err := cache.Warmup(ctx); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	if !cache.Warmed() {
		t.Error("Warmup debe dejar Warmed()=true")
	}
	// key posterior al warmup: el gateway frio la valida con 1 query.
	lateKey := "late-key-999"
	testenv.SeedKey(t, pgDB, tenant, lateKey)

	counted.Reset()
	info, err := cache.Validate(ctx, lateKey)
	if err != nil {
		t.Fatalf("validacion fria: %v", err)
	}
	if got := counted.Hits(); got != 1 {
		t.Errorf("gateway frio: %d queries a api_keys, quiero exactamente 1", got)
	}
	if info.TenantID != tenant {
		t.Errorf("tenant del hash: %q, quiero %q", info.TenantID, tenant)
	}
	hash := backend.HashKey(lateKey)
	if _, err := rdb.Get(ctx, "apikey:"+hash).Result(); err != nil {
		t.Errorf("apikey:{sha256} no quedo en Redis: %v", err)
	}
	if ttl := rdb.TTL(ctx, "apikey:"+hash).Val(); ttl <= 0 || ttl > 300*time.Second || ttl < 290*time.Second {
		t.Errorf("TTL del cache = %v, quiero 300 s", ttl)
	}

	counted.Reset()
	for i := 0; i < 1000; i++ {
		if _, err := cache.Validate(ctx, lateKey); err != nil {
			t.Fatalf("validacion #%d: %v", i, err)
		}
	}
	if got := counted.Hits(); got != 0 {
		t.Errorf("1.000 validaciones calientes: %d queries Postgres, quiero 0", got)
	}
}

// AC2: /healthz solo pasa con el cache de keys activas cargado
// (warmup de 10 keys de fixture).
func TestHealthzWaitsForWarmup(t *testing.T) {
	pgDB := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()
	tenant := testenv.SeedTenant(t, pgDB, "warmup-tenant")
	for i := 0; i < 10; i++ {
		testenv.SeedKey(t, pgDB, tenant, "hk-"+string(rune('A'+i)))
	}
	rp := repo.New(pgDB)
	cache := backend.NewApiKeyCache(rdb, rp, 300*time.Second)
	app := handlers.NewApp(&handlers.Deps{Cfg: backend.ConfigFromEnv(), Repo: rp, RDB: rdb, Keys: cache})

	resp, err := app.Test(httptestGet("/healthz"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("healthz pre-warmup: %d, quiero 503", resp.StatusCode)
	}
	if err := cache.Warmup(ctx); err != nil {
		t.Fatal(err)
	}
	resp2, err := app.Test(httptestGet("/healthz"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("healthz post-warmup: %d, quiero 200", resp2.StatusCode)
	}
}

// AC3: key revocada — status='revoked' en Postgres y expiracion del
// cache; la validacion falla al expirar (<300 s). El TTL se acorta a
// 1 s en el test para no cronometrar 5 minutos (mismo codigo).
func TestRevokedKeyFailsWhenCacheExpires(t *testing.T) {
	pgDB := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()
	tenant := testenv.SeedTenant(t, pgDB, "revoked-tenant")
	hash := testenv.SeedKey(t, pgDB, tenant, "revoking-key")
	rp := repo.New(pgDB)
	cache := backend.NewApiKeyCache(rdb, rp, time.Second)

	start := time.Now()
	if _, err := cache.Validate(ctx, "revoking-key"); err != nil {
		t.Fatalf("primera validacion: %v", err)
	}
	// revocacion en la fuente fria; el cache aun responde
	if _, err := pgDB.ExecContext(ctx, `UPDATE api_keys SET status='revoked' WHERE key_hash=$1`, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Validate(ctx, "revoking-key"); err != nil {
		t.Errorf("el cache deberia responder antes de expirar: %v", err)
	}
	// ventana de deteccion maxima = TTL del contrato (300 s)
	deadline := start.Add(300 * time.Second)
	_, err := cache.Validate(ctx, "revoking-key") // cache aun caliente (ttl 1 s)
	for err == nil && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		_, err = cache.Validate(ctx, "revoking-key")
	}
	if err != backend.ErrKeyRevoked {
		t.Errorf("tras expirar el cache: %v, quiero ErrKeyRevoked", err)
	}
	if elapsed := time.Since(start); elapsed >= 300*time.Second {
		t.Errorf("deteccion de revocacion en %v, quiero <300 s", elapsed)
	}
}

func httptestGet(path string) *http.Request {
	return httptest.NewRequest("GET", path, nil)
}
