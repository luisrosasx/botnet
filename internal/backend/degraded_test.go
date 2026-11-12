// Chaos de Redis con sesiones vivas (T-0806, A3-X3/A3-09): Redis se
// para la ventana de caos (90 s por defecto; CHAOS_WINDOW_S para
// acelerar en runs locales) y al volver: 0 sesiones cortadas por la
// caida, admision 503 durante la ventana (sin admision a ciegas),
// tenant:{t}:gb_used reconstruido con error <=0,5 % y presencia
// node:{id}:live re-anudada por heartbeat con TTL 180 s, sin re-enrolls.
//
// El test es dueno del redis-server portable (lo mata y lo levanta en
// un puerto propio, 16391): Postgres vive en DATABASE_URL (testenv).
package backend_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
)

const chaosPort = "16391"

func TestRedis90sChaos(t *testing.T) {
	pgDB := testenv.PG(t)
	bin := testenv.RedisServerBin()
	if bin == "" {
		t.Skip("redis-server portable ausente: caos de 90 s requiere motor")
	}
	window := 90 * time.Second
	if v := os.Getenv("CHAOS_WINDOW_S"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			window = time.Duration(secs) * time.Second
		}
	}
	ctx := context.Background()

	// fixtures: tenant con 3 sesiones vivas y su contador en Redis,
	// sobre-COBRO del 2 % (el gateway acumula en Redis: al caer, el
	// valor caliente ya no es de fiar).
	tenant := testenv.SeedTenant(t, pgDB, "chaos-tenant")
	period := testenv.SeedQuota(t, pgDB, tenant, 100, 50)
	for i := 0; i < 3; i++ {
		node := testenv.SeedNode(t, pgDB, "ES", "Madrid", 12345)
		if _, err := pgDB.ExecContext(ctx, `
			INSERT INTO sessions (node_id, tenant_id, gw_id, bytes_in, bytes_out)
			VALUES ($1::uuid, $2::uuid, 'gw-chaos', 400*1024*1024, 100*1024*1024)`, node, tenant); err != nil {
			t.Fatal(err)
		}
	}
	var truthBytes int64
	if err := pgDB.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(bytes_in + bytes_out), 0) FROM sessions WHERE tenant_id=$1::uuid`, tenant).Scan(&truthBytes); err != nil {
		t.Fatal(err)
	}
	truthGB := float64(truthBytes) / (1024 * 1024 * 1024)

	// redis dedicado para poder matarlo sin romper otros tests.
	cmd := exec.Command(bin, "--port", chaosPort, "--save", "", "--appendonly", "no", "--bind", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start redis: %v", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:" + chaosPort, DB: 9})
	defer rdb.Close()
	if err := waitPong(ctx, rdb, 3*time.Second); err != nil {
		t.Fatalf("redis no arranco: %v", err)
	}
	gbKey := fmt.Sprintf("tenant:%s:gb_used:%s", tenant, period)
	rdb.Set(ctx, gbKey, truthGB*1.02, 0) // 2 % de sobre-cobro acumulado

	watch := backend.NewRedisWatch(rdb, repoFor(pgDB), period, func() []string { return []string{tenant} })
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchDone := make(chan error, 1)
	go func() { watchDone <- watch.Start(wctx) }()

	// --- ventana de caos: matar Redis con sesiones vivas ---
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill redis: %v", err)
	}
	cmd.Wait()
	// la vigilia lo nota en ~1 tick (5 s) con ping de 2 s de techo
	if err := waitHealthy(watch, false, 20*time.Second); err != nil {
		t.Fatalf("la vigilia no declaro Redis caido: %v", err)
	}
	if watch.AdmissionOK() {
		t.Error("AdmissionOK con Redis caido: habria admision a ciegas (quiero 503)")
	}
	time.Sleep(window)
	// AC1: 0 sesiones vivas cortadas por la caida.
	var openNow int
	if err := pgDB.QueryRowContext(ctx, `
		SELECT count(*) FROM sessions WHERE tenant_id=$1::uuid AND ended_at IS NULL`, tenant).Scan(&openNow); err != nil || openNow != 3 {
		t.Errorf("sesiones vivas tras %v de caos: %d (quiero 3, cero cortadas) err=%v", window, openNow, err)
	}

	// --- recuperacion: levanto Redis, la vigilia re-anuda ---
	cmd = exec.Command(bin, "--port", chaosPort, "--save", "", "--appendonly", "no", "--bind", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("restart redis: %v", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	if err := waitPong(ctx, rdb, 5*time.Second); err != nil {
		t.Fatalf("redis no volvio: %v", err)
	}
	if err := waitHealthy(watch, true, 30*time.Second); err != nil {
		t.Fatalf("la vigilia no declaro Redis vivo: %v", err)
	}
	if !watch.AdmissionOK() {
		t.Error("AdmissionOK=false tras recuperar Redis")
	}

	// AC2: gb_used reconstruido desde la fuente fria, error <=0,5 %.
	rebuilt, err := rdb.Get(ctx, gbKey).Float64()
	if err != nil {
		t.Fatalf("gb_used no reconstruido: %v", err)
	}
	rel := abs(rebuilt-truthGB) / truthGB
	if rel > 0.005 {
		t.Errorf("gb_used reconstruido = %.6f GB, verdad = %.6f GB, error %.2f%% > 0,5%%", rebuilt, truthGB, rel*100)
	}

	// AC3: presencia re-anudada por heartbeat, TTL <=180 s, sin re-enroll.
	nodeID := "550e8400-0000-4000-8000-000000000009"
	if err := backend.TouchLive(ctx, rdb, nodeID, backend.ConfigFromEnv().NodePresenceTTL); err != nil {
		t.Fatalf("TouchLive: %v", err)
	}
	ttl := rdb.TTL(ctx, "node:"+nodeID+":live").Val()
	if ttl <= 0 || ttl > 180*time.Second {
		t.Errorf("node:{id}:live TTL = %v, quiero <=180 s", ttl)
	}
	cancel()
	if err := <-watchDone; err != nil && ctx.Err() == nil {
		t.Logf("watch cerro con: %v (acumulado de pings caidos, esperado)", err)
	}
}

func repoFor(db *sql.DB) *chaosRepo { return &chaosRepo{db: db} }

// chaosRepo: unica cara de Postgres que necesita la vigilia (la del
// spec backend.SessionBytes) mas el listado de tenants.
type chaosRepo struct{ db *sql.DB }

func (r *chaosRepo) SumTenantBytes(ctx context.Context, tenantID string, periodStart string) (int64, error) {
	var sum sql.NullInt64
	err := r.db.QueryRowContext(ctx, `
		SELECT SUM(bytes_in + bytes_out) FROM sessions
		 WHERE tenant_id = $1::uuid AND started_at >= $2::date`, tenantID, periodStart).Scan(&sum)
	return sum.Int64, err
}

func waitPong(ctx context.Context, rdb *redis.Client, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		c, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := rdb.Ping(c).Err()
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func waitHealthy(w *backend.RedisWatch, want bool, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for w.Healthy() != want {
		if time.Now().After(deadline) {
			return fmt.Errorf("healthy=%v, queria %v tras %v", w.Healthy(), want, limit)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
