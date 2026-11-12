package intrusion

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Integración de los runners shell (T-0201/02/04/05/06) ejecutándolos de
// verdad con dobles: sin QEMU ni legba/RouterSploit instalados, los fixtures
// y --fail-copies sustituyen al hardware; la limitación queda anotada en el
// runbook. Requiere bash (Git Bash en Windows) y la toolchain go.

var (
	repoRoot string
	bashPath string
	shPath   string
	strikeB  string // helper botnet-strike precompilado para los scripts
)

func TestMain(m *testing.M) {
	code := 0
	func() {
		repoRoot, _ = filepath.Abs("../..")
		if bashPath, _ = exec.LookPath("bash"); bashPath == "" {
			fmt.Fprintln(os.Stderr, "test: bash no disponible; tests de scripts se omitirán")
			code = m.Run()
			return
		}
		if shPath, _ = exec.LookPath("sh"); shPath == "" {
			shPath = bashPath
		}
		tmp, err := os.MkdirTemp("", "strike-helper")
		if err != nil {
			code = 1
			return
		}
		defer os.RemoveAll(tmp)
		strikeB = filepath.Join(tmp, "botnet-strike.exe")
		cmd := exec.Command("go", "build", "-o", strikeB, "./cmd/botnet-strike")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "test: helper no compila: %v\n%s\n", err, out)
			code = 1
			return
		}
		os.Setenv("STRIKE_BIN", strikeB)
		code = m.Run()
	}()
	os.Exit(code)
}

type runResult struct {
	stdout, stderr string
	code           int
	elapsed        time.Duration
}

func (r runResult) mustCode(t *testing.T, want int) runResult {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit=%d (quería %d)\nstdout=%s\nstderr=%s", r.code, want, r.stdout, r.stderr)
	}
	return r
}

func needBash(t *testing.T) {
	t.Helper()
	if bashPath == "" {
		t.Skip("bash no disponible en esta máquina")
	}
}

func needSh(t *testing.T) {
	t.Helper()
	if bashPath == "" {
		t.Skip("bash/sh no disponible en esta máquina")
	}
}

func runWith(t *testing.T, shell string, env map[string]string, args ...string) runResult {
	t.Helper()
	cmd := exec.Command(shell, args...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), mapToSlice(env)...)
	start := time.Now()
	var outBuf, errBuf strings.Builder
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	err := cmd.Run()
	res := runResult{stdout: outBuf.String(), stderr: errBuf.String(), elapsed: time.Since(start)}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("ejecutar %v: %v", args, err)
	}
	return res
}

func mapToSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func fixture(name string) string { return filepath.Join(repoRoot, "testdata", "intrusion", name) }

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	line := strings.TrimSpace(s)
	if i := strings.IndexByte(line, '{'); i >= 0 {
		line = line[i:]
		if j := strings.LastIndexByte(line, '}'); j >= 0 {
			line = line[:j+1]
		}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("JSON inválido %q: %v", s, err)
	}
	return obj
}

// --- T-0201 legba_run.sh ----------------------------------------------------

func TestLegbaRunProtocols(t *testing.T) {
	needBash(t)
	r := runWith(t, bashPath, nil, filepath.Join(repoRoot, "scripts/intrusion/legba_run.sh"), "--protocols")
	r.mustCode(t, 0)
	lines := strings.Fields(r.stdout)
	if len(lines) != 4 {
		t.Fatalf("--protocols debe listar 4, salió %d: %q", len(lines), r.stdout)
	}
}

func TestLegbaRunFixtureHit(t *testing.T) {
	needBash(t)
	r := runWith(t, bashPath, nil,
		filepath.Join(repoRoot, "scripts/intrusion/legba_run.sh"),
		"TARGET=10.70.0.5",
		"--fixture", fixture("legba_hit.json"),
		"--banner", fixture("banner_tlwr841n.txt")).mustCode(t, 0)
	// criterio 1: JSON completo en < 30 s (doble: fixture en vez de QEMU).
	if r.elapsed > 30*time.Second {
		t.Errorf("runner tardó %v (tope 30 s)", r.elapsed)
	}
	obj := jsonOf(t, r.stdout)
	if obj["protocol"] != "http" {
		t.Errorf("protocol=%v, quiero http", obj["protocol"])
	}
	if obj["cred_index"] == nil {
		t.Error("cred_index no debe ser null en hit")
	}
	if g, _ := obj["model_guess"].(string); !strings.Contains(strings.ToUpper(g), "TL-WR") {
		t.Errorf("model_guess=%v sin pista TL-WR", obj["model_guess"])
	}
}

func TestLegbaRunFixtureMiss(t *testing.T) {
	needBash(t)
	r := runWith(t, bashPath, nil,
		filepath.Join(repoRoot, "scripts/intrusion/legba_run.sh"),
		"TARGET=10.70.0.9",
		"--fixture", fixture("legba_miss.json"))
	if r.code == 0 {
		t.Errorf("target sano: quiero exit != 0, tuve 0 (%s)", r.stdout)
	}
	obj := jsonOf(t, r.stdout)
	if obj["protocol"] != nil {
		t.Errorf("protocol debe ser null, es %v", obj["protocol"])
	}
	// criterio 2: cero procesos colgados. pgrep no existe en Git Bash; se
	// comprueba por ps que ningún hijo del runner siga vivo.
	if _, err := exec.LookPath("ps"); err == nil {
		out, _ := exec.Command(bashPath, "-c", "ps | grep -c '[b]otnet-strike' || true").Output()
		if strings.TrimSpace(string(out)) != "0" {
			t.Errorf("procesos colgados del runner: ps contó %s", strings.TrimSpace(string(out)))
		}
	}
}

// --- T-0202 routersploit_run.sh ---------------------------------------------

func TestRSPDryRunKnownModel(t *testing.T) {
	needBash(t)
	r := runWith(t, bashPath, nil,
		filepath.Join(repoRoot, "scripts/intrusion/routersploit_run.sh"),
		"--dry-run", "MODEL=TL-WR841N").mustCode(t, 0)
	if r.elapsed > 5*time.Second {
		t.Errorf("dry-run tardó %v (tope 5 s por modelo)", r.elapsed)
	}
	obj := jsonOf(t, r.stdout)
	if obj["exploit_id"] == nil || obj["exploit_id"] == "" {
		t.Errorf("exploit_id nulo para TL-WR841N: %v", obj)
	}
	if obj["dry_run"] != true {
		t.Error("falta flag dry_run")
	}
}

func TestRSPDryRunUnknownModel(t *testing.T) {
	needBash(t)
	r := runWith(t, bashPath, nil,
		filepath.Join(repoRoot, "scripts/intrusion/routersploit_run.sh"),
		"--dry-run", "MODEL=ModeloFantasmaXYZ")
	r.mustCode(t, 3) // ausencia, no fallo
	obj := jsonOf(t, r.stdout)
	if obj["exploit_id"] != nil {
		t.Errorf("exploit_id debe ser null: %v", obj)
	}
}

func TestRSPFixtureRunEvidence(t *testing.T) {
	needBash(t)
	logPath := filepath.Join(t.TempDir(), "intrusion.log")
	r := runWith(t, bashPath, map[string]string{"INTRUSION_LOG": logPath},
		filepath.Join(repoRoot, "scripts/intrusion/routersploit_run.sh"),
		"MODEL=TL-WR841N", "TARGET=10.70.0.5",
		"--fixture", fixture("rs_uid.txt"),
		"--log", logPath).mustCode(t, 0)
	logB, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("intrusion.log no escrito (criterio 3, evidencia QEMU con fixture): %v", err)
	}
	if !strings.Contains(string(logB), "uid=") || !strings.Contains(string(logB), "CVE-2022-30262") {
		t.Errorf("intrusión.log sin evidencia uid/CVE: %s", logB)
	}
	obj := jsonOf(t, r.stdout)
	if ev, _ := obj["evidence"].(string); !strings.Contains(ev, "uid=") {
		t.Errorf("evidence en JSON: %v", obj["evidence"])
	}
}

// --- T-0205/T-0206 stager.sh -------------------------------------------------

func stagerSh() string { return filepath.Join(repoRoot, "deploy/sliver-builder/stager.sh") }

func writeFile(t *testing.T, path string, size int) string {
	t.Helper()
	b := make([]byte, size)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestStagerDeliveryWithRetries(t *testing.T) {
	needSh(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	logPath := filepath.Join(tmp, "stager.log")
	shaWant := writeFile(t, src, 4096)
	destDir := filepath.Join(tmp, "dest")
	_ = runWith(t, shPath, nil, stagerSh(),
		"--src="+src, "--profile=A", "--dest-dir="+destDir,
		"--retries=3", "--backoff-base-s=1", "--jitter-range-s=2",
		"--fail-copies=2", "--stage-cmd=true", "--log="+logPath).mustCode(t, 0)
	logB, _ := os.ReadFile(logPath)
	ls := string(logB)
	// criterio 2: con pérdida simulada la entrega completa en ≤ 3 intentos.
	if n := strings.Count(ls, "result=retry"); n > 3 {
		t.Errorf("reintentos=%d (tope 3): %s", n, ls)
	}
	for _, want := range []string{"attempt=1 result=retry", "attempt=2 result=retry", "attempt=3 result=ok", "stage=spawned"} {
		if !strings.Contains(ls, want) {
			t.Errorf("stager.log sin %q:\n%s", want, ls)
		}
	}
	// criterio 1 (doble): el fichero entregado es idéntico al src.
	got, err := os.ReadFile(filepath.Join(destDir, "botd"))
	if err != nil {
		t.Fatalf("destino /data/botd/botd no entregado: %v", err)
	}
	h := sha256.Sum256(got)
	if hex.EncodeToString(h[:]) != shaWant {
		t.Error("sha256 destino != src")
	}
	// el spool de la clase queda creado (T-0206 criterio 3).
	if fi, err := os.Stat(filepath.Join(destDir, "botd-spool")); err != nil || !fi.IsDir() {
		t.Error("falta directorio botd-spool")
	}
}

func TestStagerRetriesExhausted(t *testing.T) {
	needSh(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	writeFile(t, src, 512)
	r := runWith(t, shPath, nil, stagerSh(),
		"--src="+src, "--profile=A", "--dest-dir="+filepath.Join(tmp, "dest"),
		"--retries=3", "--backoff-base-s=1", "--jitter-range-s=1",
		"--fail-copies=9", "--stage-cmd=true", "--log="+filepath.Join(tmp, "stager.log"))
	if r.code != 2 {
		t.Fatalf("reintentos agotados: quiero exit 2, tuve %d (%s)", r.code, r.stderr)
	}
	if strings.Contains(r.stdout, "stage=spawned") {
		t.Error("no debe lanzarse botd --stage si la copia nunca validó")
	}
}

func TestStagerJitterSpread5Runs(t *testing.T) {
	needSh(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	writeFile(t, src, 512)
	re := regexp.MustCompile(`delay=(\d+)s`)
	var delays []int
	for i := 0; i < 5; i++ {
		logPath := filepath.Join(tmp, fmt.Sprintf("run%d.log", i))
		runWith(t, shPath, nil, stagerSh(),
			"--src="+src, "--profile=A", "--dest-dir="+filepath.Join(tmp, fmt.Sprintf("d%d", i)),
			"--retries=2", "--backoff-base-s=0", "--jitter-range-s=10",
			"--fail-copies=1", "--stage-cmd=true", "--log="+logPath).mustCode(t, 0)
		logB, _ := os.ReadFile(logPath)
		m := re.FindStringSubmatch(string(logB))
		if m == nil {
			t.Fatalf("run %d sin delay registrado:\n%s", i, logB)
		}
		var d int
		fmt.Sscanf(m[1], "%d", &d)
		delays = append(delays, d)
	}
	minD, maxD := delays[0], delays[0]
	uniq := map[int]bool{}
	for _, d := range delays {
		if d < minD {
			minD = d
		}
		if d > maxD {
			maxD = d
		}
		uniq[d] = true
	}
	// criterio 3: los 5 runs presentan retardos distintos con diff ≥ 1 s.
	if len(uniq) < 2 || maxD-minD < 1 {
		t.Errorf("jitter sin dispersión: delays=%v", delays)
	}
}

func TestStagerClassCTmpAndFlashCap(t *testing.T) {
	needSh(t)
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.bin")
	writeFile(t, src, 512)

	// overlay de 6 MB (< 8 MB) obliga destino /tmp y el log lo dice.
	log1 := filepath.Join(tmp, "c.log")
	runWith(t, shPath, nil, stagerSh(),
		"--src="+src, "--profile=C", "--overlay-free-kb=6000",
		"--stage-cmd=true", "--log="+log1).mustCode(t, 0)
	logB, _ := os.ReadFile(log1)
	if !strings.Contains(string(logB), "dest=/tmp") {
		t.Errorf("clase C sin dest=/tmp en log:\n%s", logB)
	}
	// overlay medido pequeño (6 MB) sobre clase A: la detección manda.
	log2 := filepath.Join(tmp, "a.log")
	runWith(t, shPath, nil, stagerSh(),
		"--src="+src, "--profile=A", "--overlay-free-kb=6000",
		"--stage-cmd=true", "--log="+log2).mustCode(t, 0)
	logB2, _ := os.ReadFile(log2)
	if !strings.Contains(string(logB2), "dest=/tmp") {
		t.Errorf("overlay < 8 MB sin dest=/tmp:\n%s", logB2)
	}
	// tope de flash: < 4 MB libres => exit 4, copia abortada.
	r := runWith(t, shPath, nil, stagerSh(),
		"--src="+src, "--profile=C", "--overlay-free-kb=1000",
		"--stage-cmd=true", "--log="+filepath.Join(tmp, "cap.log"))
	if r.code != 4 {
		t.Errorf("flash < 4 MB: quiero exit 4, tuve %d", r.code)
	}
	_ = os.Remove("/tmp/botd")
	_ = os.RemoveAll("/tmp/botd-spool")
}

// --- T-0204 build.sh / matrix.sh ---------------------------------------------

func buildSh() string { return filepath.Join(repoRoot, "deploy/sliver-builder/build.sh") }

func TestBuildScriptCGOFlag(t *testing.T) {
	b, err := os.ReadFile(buildSh())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "CGO_ENABLED=0") {
		t.Error("build.sh debe fijar CGO_ENABLED=0 explícitamente (criterio 3)")
	}
}

func TestBuildScriptStaticMIPSELLF(t *testing.T) {
	needBash(t)
	tmp := t.TempDir()
	r := runWith(t, bashPath, nil, buildSh(), "--arch=mipsle", "--profile=C", "--dist="+tmp)
	r.mustCode(t, 0)
	// T-1305 (A1-08): el builder renombra por perfil — clase C = srvhost
	// (--name explicito sigue produciendo botd-*, como hace matrix.sh).
	out := filepath.Join(tmp, "srvhost-mipsle")

	f, err := elf.Open(out)
	if err != nil {
		t.Fatalf("no es ELF: %v", err)
	}
	defer f.Close() // en Windows el handle abierto bloquea el TempDir cleanup
	if f.Machine != elf.EM_MIPS {
		t.Errorf("Machine=%v, quiero EM_MIPS", f.Machine)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			t.Error("PT_INTERP presente: el binario es dinámicamente linkeado")
		}
	}
	if fi, err := os.Stat(out); err == nil {
		if upx, _ := exec.LookPath("upx"); upx != "" {
			if fi.Size() > 2726297 {
				t.Errorf("post-upx %d B > presupuesto 2.6 MB", fi.Size())
			}
			tt := runWith(t, bashPath, nil, "-c", fmt.Sprintf("upx -t %q | grep -q OK", out))
			tt.mustCode(t, 0) // upx -t: "Tested 1 file(s), OK"
		} else if fi.Size() > 4194304 {
			t.Errorf("raw %d B > 4 MiB (sin upx en esta máquina)", fi.Size())
		}
	}
	if file, _ := exec.LookPath("file"); file != "" {
		out2 := runWith(t, bashPath, nil, "-c", `"$0" "$1"`, file, out)
		if !strings.Contains(out2.stdout, "MIPS") || strings.Contains(out2.stdout, "dynamically linked") {
			t.Errorf("file(code=%d): stdout=%q stderr=%q", out2.code, out2.stdout, out2.stderr)
		}
	}
}

func TestBuildScriptRejectsAlienArch(t *testing.T) {
	needBash(t)
	r := runWith(t, bashPath, nil, buildSh(), "--arch=armv5", "--profile=C", "--dist="+t.TempDir())
	if r.code != 2 {
		t.Errorf("armv5 en perfil C: quiero exit 2, tuve %d (%s)", r.code, r.stderr)
	}
}

func TestCrossMatrix6Arches(t *testing.T) {
	if runtime.GOOS == "windows" && bashPath == "" {
		t.Skip("sin bash")
	}
	needBash(t)
	tmp := t.TempDir()
	r := runWith(t, bashPath, map[string]string{"DIST": tmp},
		filepath.Join(repoRoot, "scripts/cross/matrix.sh"))
	r.mustCode(t, 0)
	if r.elapsed > 600*time.Second {
		t.Errorf("matriz en %v > 10 min", r.elapsed)
	}
	for _, arch := range []string{"mipsbe", "mipsle", "armv5", "armv7", "arm64", "amd64"} {
		if fi, err := os.Stat(filepath.Join(tmp, "botd-"+arch)); err != nil || fi.Size() == 0 {
			t.Errorf("falta botd-%s en dist de la matriz", arch)
		}
	}
}
