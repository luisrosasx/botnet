package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/agent"
)

// botdPath es el binario real construido una sola vez: T-0301 AC1/AC2
// se verifican contra el ejecutable, no contra un doble.
var botdPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "botd-build")
	if err != nil {
		panic(err)
	}
	botdPath = filepath.Join(dir, "botd")
	if isWindowsTest() {
		botdPath += ".exe"
	}
	out, err := exec.Command("go", "build", "-o", botdPath, "github.com/botnet/botnet/cmd/botd").CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("build botd: %v\n%s", err, out))
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func isWindowsTest() bool { return filepath.Separator == '\\' }

// runBotd ejecuta el binario con config/env dados y -run-for para que
// se retire solo; devuelve stdout+stderr.
func runBotd(t *testing.T, cfgPath string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command(botdPath, args...)
	if cfgPath != "" {
		cmd.Args = append(cmd.Args, "-config", cfgPath)
	}
	cmd.Env = append(os.Environ(), env...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("botd %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func writeCfg(t *testing.T, endpoint, nodeDir string, intervalS int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cfg.json")
	body := fmt.Sprintf(`{"endpoint":%q,"node_dir":%q,"interval_s":%d}`, endpoint, nodeDir, intervalS)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubAPI F1: /v1/enroll responde el 201 con las 7 claves;
// /v1/heartbeat responde 200 con cuerpo vacio (compatible).
type stubAPI struct {
	enrolls    int
	heartbeats int
}

func (s *stubAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/enroll":
			var req struct {
				Arch string `json:"arch"`
				CSR  string `json:"csr"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CSR == "" {
				http.Error(w, "csr", http.StatusBadRequest)
				return
			}
			s.enrolls++
			secret := base64.StdEncoding.EncodeToString(make([]byte, 32))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"node_id":     "3f0d1c2b-0000-4000-8000-abcdef012345",
				"slug":        "k7qz2m4x9p1r",
				"cert_pem":    "CERT",
				"ca_pem":      "CA",
				"node_secret": secret,
				"rendezvous":  []string{"rdv1:443"},
				"ttl_s":       86400,
			})
		case "/v1/heartbeat":
			body, _ := io.ReadAll(r.Body)
			if len(body) == 0 {
				t.Error("heartbeat sin payload")
			}
			s.heartbeats++
		default:
			http.NotFound(w, r)
		}
	}
}

// T-0301 AC1: ./botd --version → v0.1.0 y exit 0.
func TestVersionFlag(t *testing.T) {
	out := runBotd(t, "", nil, "-version")
	if strings.TrimSpace(out) != "v0.1.0" {
		t.Fatalf("--version = %q, want v0.1.0", strings.TrimSpace(out))
	}
}

// T-1403 AC2: `botd --print-root` existe y responde byte a byte con la
// raiz ed25519 fijada en build via -X internal/agent.RootPubKeyBase64.
func TestPrintRootFlagMatchesInjectedVar(t *testing.T) {
	fakeRoot := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	path := filepath.Join(t.TempDir(), "botd-rooted")
	if isWindowsTest() {
		path += ".exe"
	}
	cmd := exec.Command("go", "build",
		"-ldflags", "-X github.com/botnet/botnet/internal/agent.RootPubKeyBase64="+fakeRoot,
		"-o", path, "github.com/botnet/botnet/cmd/botd")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build botd-rooted: %v\n%s", err, out)
	}
	out, err := exec.Command(path, "-print-root").CombinedOutput()
	if err != nil {
		t.Fatalf("--print-root: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != fakeRoot {
		t.Fatalf("--print-root = %q, want %q", got, fakeRoot)
	}
	// Y con el binario sin inyectar: flag definido, var vacia (exit 0).
	if plain := strings.TrimSpace(runBotd(t, "", nil, "-print-root")); plain != "" {
		t.Fatalf("--print-root sin -X = %q, want vacio", plain)
	}
}

// EP-06 (hallazgo T-0606 AC3): el /metrics del agente sirve
// botnet_tenant_count — el gauge de densidad de la admision del proxy.
func TestAgentMetricsTenantGauge(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	nodeDir := t.TempDir()
	if err := agent.SaveNodeIdentity(nodeDir, agent.NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012348", Slug: "k7qz2m4x9p1v", Arch: "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeCfg(t, srv.URL, nodeDir, 1)
	metricsAddr := "127.0.0.1:19203"
	cmd := exec.Command(botdPath, "-run-for", "3s", "-socks5-addr", "",
		"-metrics-addr", metricsAddr, "-config", cfgPath)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()

	var body string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + metricsAddr + "/metrics")
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			if strings.Contains(body, "botnet_tenant_count") {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !strings.Contains(body, "# TYPE botnet_tenant_count gauge") || !strings.Contains(body, "botnet_tenant_count 0") {
		t.Fatalf("/metrics del agente sin gauge:\n%s\nbotd log:\n%s", body, out.String())
	}
}

// T-0601: el arranque del proxy es visible en el log del agente. En el
// build de test (sin tags socks5,tcp) el proxy informa socks5=off y el
// plano de control sigue latiendo — el proxy no puede tumbar botd.
func TestProxyStartupLogInAgentMode(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	nodeDir := t.TempDir()
	if err := agent.SaveNodeIdentity(nodeDir, agent.NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012346", Slug: "k7qz2m4x9p1s", Arch: "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeCfg(t, srv.URL, nodeDir, 1)
	out := runBotd(t, cfgPath, nil, "-run-for", "500ms", "-socks5-addr", "127.0.0.1:0")
	if !strings.Contains(out, "proxy socks5=off") {
		t.Fatalf("falta log proxy socks5=off (build sin tags): \n%s", out)
	}
	if !strings.Contains(out, `"mode":"agent"`) {
		t.Fatalf("agente no arranco: \n%s", out)
	}
}

// T-0301 AC2: GOMEMLIMIT, GOGC y GOMAXPROCS visibles en --print-env.
func TestPrintEnvShowsBudget(t *testing.T) {
	out := runBotd(t, "", nil, "-print-env")
	for _, want := range []string{"GOMEMLIMIT=5MiB", "GOGC=40", "GOMAXPROCS=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--print-env sin %q:\n%s", want, out)
		}
	}
}

// T-0301 AC1: sin node.json valido → log mode=enroll y request
// POST /v1/enroll al stub; el arranque resultante late OK (gate
// HEARTBEAT 200 ok de la epica).
func TestModeEnrollThenHeartbeat(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	nodeDir := t.TempDir()
	cfgPath := writeCfg(t, srv.URL, nodeDir, 1)
	out := runBotd(t, cfgPath, []string{"BOTNET_DEVICE_TOKEN=tok-xyz"}, "-run-for", "700ms")
	if stub.enrolls != 1 {
		t.Fatalf("POST /v1/enroll = %d, want 1\n%s", stub.enrolls, out)
	}
	if !strings.Contains(out, `"mode":"enroll"`) {
		t.Fatalf("falta log mode=enroll:\n%s", out)
	}
	if !strings.Contains(out, "HEARTBEAT 200 ok") {
		t.Fatalf("falta HEARTBEAT 200 ok:\n%s", out)
	}
	if stub.heartbeats < 1 {
		t.Fatalf("heartbeats = %d, want >= 1", stub.heartbeats)
	}
	// el estado del alta quedo en 0600/32B: secret.bin existe y node.json valida.
	if _, err := agent.LoadNodeIdentity(nodeDir); err != nil {
		t.Fatalf("node.json post-alta: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(nodeDir, "state", "secret.bin")); err != nil || fi.Size() != 32 {
		t.Fatalf("secret.bin post-alta: %v size=%d", err, fi.Size())
	}
}

// T-0301 AC2 (modo agente): con node.json valido → log mode=agent y
// late sin enrolar (cero POST /v1/enroll).
func TestModeAgentWithValidNodeJSON(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	nodeDir := t.TempDir()
	if err := agent.SaveNodeIdentity(nodeDir, agent.NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012345", Slug: "k7qz2m4x9p1r", Arch: "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeCfg(t, srv.URL, nodeDir, 1)
	out := runBotd(t, cfgPath, nil, "-run-for", "500ms")
	if stub.enrolls != 0 {
		t.Fatalf("modo agente enrolando: %d\n%s", stub.enrolls, out)
	}
	if !strings.Contains(out, `"mode":"agent"`) {
		t.Fatalf("falta log mode=agent:\n%s", out)
	}
	if stub.heartbeats < 1 {
		t.Fatalf("heartbeats = %d, want >= 1", stub.heartbeats)
	}
	if !strings.Contains(out, "HEARTBEAT 200 ok") {
		t.Fatalf("falta HEARTBEAT 200 ok:\n%s", out)
	}
}

// T-0501 AC3 (a nivel binario): un segundo `botd --parent` con el
// flock del pidfile tomado por el primero sale con exit ≠ 0.
func TestDoubleParentExitNonzero(t *testing.T) {
	nodeDir := t.TempDir()
	first := exec.Command(botdPath, "-parent", "-run-for", "20s", "-node-dir", nodeDir, "-socks5-addr", "")
	first.Stdout, first.Stderr = io.Discard, io.Discard
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		first.Process.Kill()
		first.Wait()
	}()
	pidfile := filepath.Join(nodeDir, "botd.pid")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if b, err := os.ReadFile(pidfile); err == nil && len(b) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("el primer parent no dej6 pidfile en 15 s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	second := exec.Command(botdPath, "-parent", "-run-for", "5s", "-node-dir", nodeDir, "-socks5-addr", "")
	var out strings.Builder
	second.Stdout, second.Stderr = &out, &out
	err := second.Run()
	if err == nil {
		t.Fatalf("segundo parent con flock tomado: exit 0\n%s", out.String())
	}
	if !strings.Contains(out.String(), "watchdog parent") {
		t.Fatalf("el segundo parent no report6 ErrDouble:\n%s", out.String())
	}
}

// T-0504 AC3: la detecci6n de la cadena de arranque se loguea
// (persist=systemd|rc|reboot). rc.local exporta BOTNET_PERSIST=rc:
// con ese entorno, el log debe decir persist=rc.
func TestPersistModeLogged(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	nodeDir := t.TempDir()
	if err := agent.SaveNodeIdentity(nodeDir, agent.NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012345", Slug: "k7qz2m4x9p1r", Arch: "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeCfg(t, srv.URL, nodeDir, 1)
	out := runBotd(t, cfgPath, []string{"BOTNET_PERSIST=rc"}, "-run-for", "300ms")
	// logx emite JSON estructurado: {"msg":"persist","mode":"rc"}.
	if !strings.Contains(out, `"msg":"persist"`) || !strings.Contains(out, `"mode":"rc"`) {
		t.Fatalf("falta persist=rc en el log:\n%s", out)
	}
}

// T-0503 AC1 (sin crontab real en este host): `botd optout` sale 0 y
// reporta las lineas restantes — sin root, sin drama si no hay
// crontab.
func TestOptoutRunsWithoutRoot(t *testing.T) {
	out := runBotd(t, "", nil, "optout")
	if !strings.Contains(out, "crontab lines:") {
		t.Fatalf("optout sin reporte:\n%s", out)
	}
}

// T-1404 AC1 (el cable real, sin dobles del watchdog): un hijo botd que
// sale con 75 hace que RunParent devuelva ErrRestartRequested; el padre
// se re-ejecuta a si mismo con --commit; el commit queda aplicado en
// node_dir (stable_version) y, como la version estable pinada difiere de
// la que corre, el binario del slot comprometido relanza la pareja y el
// hijo vuelve a latir contra el stub.
func TestSelfUpdateExit75CommitChainE2E(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	nodeDir := t.TempDir()
	if err := agent.SaveNodeIdentity(nodeDir, agent.NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012347", Slug: "u7p7d8a7t6e5", Arch: "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeCfg(t, srv.URL, nodeDir, 1)

	// Staged state a --commit run must apply: the slot pointer names A,
	// slot botdA holds the staged binary and stable_version pins a
	// version different from the running v0.1.0 (the "backend fija" of
	// T-1404 AC2 arriving through the update flow).
	slotExe := filepath.Join(nodeDir, "botdA")
	if isWindowsTest() {
		slotExe += ".exe"
	}
	bin, err := os.ReadFile(botdPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(slotExe, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "slot"), []byte("A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "stable_version"), []byte("v9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The marker makes the first real child exit with 75 and consumes
	// itself, so every later boot is a normal agent child.
	marker := filepath.Join(t.TempDir(), "restart-marker")
	if err := os.WriteFile(marker, []byte("pending"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runBotd(t, cfgPath, []string{RestartMarkerEnv + "=" + marker},
		"-parent", "-run-for", "9s", "-node-dir", nodeDir, "-socks5-addr", "")

	for _, want := range []string{
		"update=need_restart",        // el hijo real salio con 75
		"watchdog restart requested", // el watchdog real leyio el codigo
		`"msg":"update=commit"`,      // el padre reexecado con --commit
		"v9.9.9",                     // ... aplico la version estable
		"update=slot_relaunch",       // y relanzo el binario del slot
		"HEARTBEAT 200 ok",           // el hijo vuelvio a latir
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("falta %q en la cadena e2e:\n%s", want, out)
		}
	}
	if stub.heartbeats < 1 {
		t.Fatalf("heartbeats = %d, want >= 1 (hijo post-commit)\n%s", stub.heartbeats, out)
	}
	// El commit quedo aplicado en disco tras toda la cadena.
	got, err := os.ReadFile(filepath.Join(nodeDir, "stable_version"))
	if err != nil || strings.TrimSpace(string(got)) != "v9.9.9" {
		t.Fatalf("stable_version = %q (err %v), want v9.9.9", strings.TrimSpace(string(got)), err)
	}
}

// T-0206 AC2: el arranque de clase C con --stage deja copia del binario
// en <node_dir>/persist antes de correr, para que el guard de @reboot la
// recupere cuando el tmpfs no sobrevive al reinicio.
func TestStageCopiaEnPersistenciaAntesDeCorrer(t *testing.T) {
	stub := &stubAPI{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()
	dir := t.TempDir()
	if err := agent.SaveNodeIdentity(dir, agent.NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012349", Slug: "s1t2a3g4e5x6", Arch: "amd64",
	}); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeCfg(t, srv.URL, dir, 1)
	out := runBotd(t, cfgPath, nil, "--stage", "--run-for", "1s", "-socks5-addr", "")
	if !strings.Contains(out, `"msg":"stage"`) || !strings.Contains(out, `"copied":true`) {
		t.Fatalf("--stage no dejo copia: %s", out)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "persist"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("persist sin binario: err=%v entries=%d", err, len(entries))
	}
	// Idempotente: segundo arranque no recopia si el tamano coincide.
	out2 := runBotd(t, cfgPath, nil, "--stage", "--run-for", "1s", "-socks5-addr", "")
	if !strings.Contains(out2, `"copied":false`) {
		t.Fatalf("--stage recopio sin cambios: %s", out2)
	}
}

// T-1402: el modo --smoke demuestra bajo emulación que el ELF arranca,
// enrola (201 con contrato completo) y late (200 ok). Aquí contra el
// stub real del banco (scripts/qemu/stub_api.go) vía httptest, para que
// la ruta quede cubierta también sin QEMU.
func TestSmokeEnrollYLate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/enroll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Device-Token") == "" {
			http.Error(w, "token", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"node_id":"11111111-2222-3333-4444-555555555555","slug":"smokeslug1234",`+
			`"cert_pem":"C","ca_pem":"CA","node_secret":"aa","rendezvous":["rdv1:443"],"ttl_s":120}`)
	})
	mux.HandleFunc("/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	out := runBotd(t, "", nil, "--smoke", "--stub", srv.URL)
	if !strings.Contains(out, "ENROLL 201") || !strings.Contains(out, "HEARTBEAT 200 ok") {
		t.Fatalf("--smoke sin marcadores: %s", out)
	}
}

func TestSmokeSinStubFalla(t *testing.T) {
	cmd := exec.Command(botdPath, "--smoke")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("--smoke sin --stub debe fallar:\n%s", out)
	}
	if !strings.Contains(string(out), "--stub") {
		t.Fatalf("error poco claro:\n%s", out)
	}
}
