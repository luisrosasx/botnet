// Test del contrato POST /api/v1/sessions (T-0805): credencial, cuota,
// rate-limit por tenant y respuestas tipadas por codigo de estado. El
// contrato es el spec (docs/api/openapi.yaml); el script
// scripts/api/contract_test.sh lo repite con curl contra el `serve`
// real.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
)

type sessHarness struct {
	deps     *Deps
	tenant   string
	period   string
	keyPlain string
	nodeID   string
}

func newSessHarness(t *testing.T) *sessHarness {
	t.Helper()
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	cfg := backend.ConfigFromEnv()
	cfg.RateLimitRPS = 60
	cfg.GWAddr = "gw.test:1080"
	tenant := testenv.SeedTenant(t, db, "sess-tenant")
	period := testenv.SeedQuota(t, db, tenant, 10, 50)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	keyPlain := "sk-test-0001"
	testenv.SeedKey(t, db, tenant, keyPlain)
	rp := repo.New(db)
	keys := backend.NewApiKeyCache(rdb, rp, 300*time.Second)
	if err := keys.Warmup(context.Background()); err != nil {
		t.Fatal(err)
	}
	watch := backend.NewRedisWatch(rdb, rp, period, func() []string { return []string{tenant} })
	return &sessHarness{
		deps:     &Deps{Cfg: cfg, Repo: rp, RDB: rdb, Keys: keys, Watch: watch, Alloc: InlineAllocator{}},
		tenant:   tenant,
		period:   period,
		keyPlain: keyPlain,
		nodeID:   nodeID,
	}
}

func (h *sessHarness) postSessions(t *testing.T, bearer, body string) (int, map[string]any) {
	t.Helper()
	app := NewApp(h.deps)
	req := httptest.NewRequest("POST", "/api/v1/sessions", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

var socks5h = regexp.MustCompile(`^socks5h://`)

// AC1: 201 con {session_id, proxy_url, expires_at} y proxy_url
// socks5h://; nonce (T-0808) acompana.
func TestCreateSession201Contract(t *testing.T) {
	h := newSessHarness(t)
	code, out := h.postSessions(t, h.keyPlain, `{"country":"ES","city":"Madrid","mode":"rotating"}`)
	if code != 201 {
		t.Fatalf("quiero 201, tengo %d out=%v", code, out)
	}
	for _, k := range []string{"session_id", "proxy_url", "expires_at"} {
		if _, ok := out[k]; !ok {
			t.Errorf("falta %q en el 201: %v", k, out)
		}
	}
	if url, _ := out["proxy_url"].(string); !socks5h.MatchString(url) {
		t.Errorf("proxy_url=%q no matchea ^socks5h://", url)
	}
	if _, err := time.Parse(time.RFC3339, out["expires_at"].(string)); err != nil {
		t.Errorf("expires_at no es RFC3339: %v", out["expires_at"])
	}
	if n, _ := out["nonce"].(float64); n == 0 {
		t.Error("nonce de sesion ausente (T-0808 lo ecota el nodo)")
	}
	if id, _ := out["session_id"].(float64); id <= 0 {
		t.Error("session_id invalido")
	}
}

// AC2: sin api key -> 403; key desconocida -> 403; quota agotada
// (fixture) -> 402; rate-limit por tenant -> >=1 429.
func TestCreateSessionStatusCodes(t *testing.T) {
	h := newSessHarness(t)
	body := `{"country":"ES","city":"Madrid","mode":"rotating"}`

	if code, _ := h.postSessions(t, "", body); code != 403 {
		t.Errorf("sin api key: quiero 403, tengo %d", code)
	}
	if code, _ := h.postSessions(t, "sk-nadie", body); code != 403 {
		t.Errorf("key desconocida: quiero 403, tengo %d", code)
	}

	// fixture de cuota agotada: contador del periodo por encima del
	// allowance (10 GB).
	h.deps.RDB.Set(context.Background(), "tenant:"+h.tenant+":gb_used:"+h.period, 11.0, 0)
	if code, _ := h.postSessions(t, h.keyPlain, body); code != 402 {
		t.Errorf("cuota agotada: quiero 402, tengo %d", code)
	}
	if v, err := h.deps.RDB.Get(context.Background(), "quota:"+h.tenant+":state").Result(); err != nil || v != "exhausted" {
		t.Errorf("quota:{t}:state=%q err=%v, quiero exhausted", v, err)
	}
	h.deps.RDB.Set(context.Background(), "tenant:"+h.tenant+":gb_used:"+h.period, 1.0, 0)

	// 100 requests del mismo tenant en <1 s -> >=1 429 (rate 60/s,
	// concurrent 50: cualquiera de las dos puertas tipa 429).
	sent, codes429 := 0, 0
	start := time.Now()
	for i := 0; i < 100; i++ {
		code, _ := h.postSessions(t, h.keyPlain, body)
		sent++
		if code == 429 {
			codes429++
		}
	}
	if time.Since(start) > 5*time.Second {
		t.Logf("los 100 requests tardaron %v (la ventana de rate sigue siendo de 1 s por diseno)", time.Since(start))
	}
	if codes429 < 1 {
		t.Errorf("%d requests en %v: %d 429, quiero >= 1", sent, time.Since(start), codes429)
	}
}

// Redis caido = 503 (A3-X3): sin admision a ciegas.
func TestCreateSession503WhenRedisDown(t *testing.T) {
	h := newSessHarness(t)
	h.deps.Watch = backend.NewRedisWatch(h.deps.RDB, h.deps.Repo, h.period, func() []string { return []string{h.tenant} })
	// simular la bandera de vigilia: admission apagada sin tocar Redis
	if !h.deps.Watch.AdmissionOK() {
		t.Fatal("arranque: quiero admission OK")
	}
	h.deps.Watch.SetHealthyForTest(false)
	if code, _ := h.postSessions(t, h.keyPlain, `{"country":"ES","mode":"rotating"}`); code != 503 {
		t.Errorf("redis caido: quiero 503, tengo %d", code)
	}
}
