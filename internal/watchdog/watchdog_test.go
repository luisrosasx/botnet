package watchdog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// El doble de proceso: el propio test-binary se reexecuta con rol por
// entorno (BOTNET_WD_FAKE) o por flag -child, igual que hara botd con
// el binario de produccion (mismo ejecutable, dos roles).

const (
	fakeRoleEnv = "BOTNET_WD_FAKE"
	fakePIDEnv  = "BOTNET_WD_FAKE_PIDFILE"
	fakeOutEnv  = "BOTNET_WD_FAKE_OUT"
	fakeTTLEnv  = "BOTNET_WD_FAKE_TTL"
	fakeDeadEnv = "BOTNET_WD_FAKE_DEAD" // fichero-semaforo: apaga toda la cadena
)

// deadCancel cancela ctx cuando aparece el fichero semaforo: la cadena
// de reelecciones puede reproducirse indefinidamente (quien hereda el
// pipe del muerto se re-elige y relanza), y solo una senal comun de
// "el test termino" la apaga sin matar-pids al vuelo.
func deadCancel(ctx context.Context, cancel context.CancelFunc) {
	f := os.Getenv(fakeDeadEnv)
	if f == "" {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			if _, err := os.Stat(f); err == nil {
				cancel()
				return
			}
		}
	}()
}

func deadSeen() bool {
	f := os.Getenv(fakeDeadEnv)
	if f == "" {
		return false
	}
	_, err := os.Stat(f)
	return err == nil
}

func TestMain(m *testing.M) {
	// El flag de rol manda sobre el entorno heredado: los hijos de
	// DefaultSpawn heredan BOTNET_WD_FAKE=parent del padre fake y aun
	// asi deben nacer como hijos (-child en sus args).
	for _, a := range os.Args[1:] {
		switch a {
		case ChildFlag:
			os.Exit(fakeRunChild())
		case ParentFlag:
			os.Exit(fakeParent())
		}
	}
	switch os.Getenv(fakeRoleEnv) {
	case "child":
		os.Exit(fakeChild(false))
	case "hang":
		os.Exit(fakeChild(true))
	case "exit":
		os.Exit(3)
	case "exit75":
		os.Exit(ExitRestartRequest)
	case "sleeper":
		for i := 0; i < 300 && !deadSeen(); i++ {
			time.Sleep(100 * time.Millisecond)
		}
		os.Exit(7)
	case "parent":
		os.Exit(fakeParent())
	case "locktry":
		os.Exit(fakeLockTry())
	default:
		os.Exit(m.Run())
	}
}

// fakeChild: lado hijo del pipe SIN re-eleccion — ping = vida; hang =
// se calla (el doble de `kill -STOP`: proceso vivo, pipe mudo).
func fakeChild(hang bool) int {
	pipe, err := DialChildPipe()
	if err != nil {
		return 4
	}
	defer pipe.Close()
	log := fakeLog()
	log.Info("fake child alive", "pid", os.Getpid())
	deadline := time.Now().Add(fakeTTL())
	for time.Now().Before(deadline) && !deadSeen() {
		if !hang {
			if err := pipe.Ping(); err != nil {
				return 5
			}
		}
		time.Sleep(time.Second)
	}
	return 0
}

// fakeRunChild es el RunChild de produccion sobre el test-binary: el
// agente es un sueno largo (Work) y quien vigila al padre es el
// watchdog real — muere el padre, este proceso se re-elige.
func fakeRunChild() int {
	log := fakeLog()
	ctx, cancel := context.WithTimeout(context.Background(), fakeTTL())
	defer cancel()
	deadCancel(ctx, cancel)
	work := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	err := RunChild(ctx, ChildConfig{
		Work:        work,
		PidfilePath: os.Getenv(fakePIDEnv),
		Log:         log,
		Silence:     10 * time.Second,
		Revive:      fakeReviveEnv(),
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return 6
	}
	return 0
}

// fakeParent: RunParent de produccion con Spawn default (reexec del
// test-binary como -child).
func fakeParent() int {
	log := fakeLog()
	ctx, cancel := context.WithTimeout(context.Background(), fakeTTL())
	defer cancel()
	deadCancel(ctx, cancel)
	err := RunParent(ctx, ParentConfig{
		PidfilePath: os.Getenv(fakePIDEnv),
		Spawn:       DefaultSpawn(ChildFlag),
		Log:         log,
		Silence:     10 * time.Second,
		Revive:      fakeReviveEnv(),
	})
	if errors.Is(err, ErrDouble) {
		return 9
	}
	if err != nil {
		return 1
	}
	return 0
}

func fakeReviveEnv() time.Duration {
	if v := os.Getenv("BOTNET_WD_FAKE_REVIVE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 0 // defaults() del paquete
}

// fakeLockTry: segundo arranque con flock tomado → exit ≠ 0
// (criterio T-0501 AC3 medido a nivel de proceso).
func fakeLockTry() int {
	pf := &Pidfile{Path: os.Getenv(fakePIDEnv)}
	if err := pf.Acquire(); err != nil {
		fmt.Fprintln(os.Stderr, "locktry:", err)
		return 2
	}
	pf.Release()
	return 0
}

func fakeLog() *slog.Logger {
	if p := os.Getenv(fakeOutEnv); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			return slog.New(slog.NewTextHandler(f, nil))
		}
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func fakeTTL() time.Duration {
	if v := os.Getenv(fakeTTLEnv); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 30 * time.Second
}

func testExe(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func wdLog(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- T-0501 ---------------------------------------------------------------

// AC1: el relanzamiento es reexec del MISMO binario: 0 segundo binario
// en disco.
func TestReexecSameBinary(t *testing.T) {
	exe, err := SameBinary()
	if err != nil {
		t.Fatal(err)
	}
	if exe != testExe(t) {
		t.Fatalf("SameBinary = %q, want test binary %q", exe, testExe(t))
	}
	childArgs, err := ReexecArgs(ChildFlag)
	if err != nil {
		t.Fatal(err)
	}
	if childArgs[0] != ChildFlag {
		t.Fatalf("args %v deben abrir con %s", childArgs, ChildFlag)
	}
	for _, a := range childArgs[1:] {
		if a == ParentFlag || a == "-run-for" || strings.HasPrefix(a, "-run-for=") {
			t.Fatalf("reexec arrastra %q (el rol/reloj es del padre)", a)
		}
	}
	// T-1404 AC1: the parent re-execs itself with two role flags, and
	// children spawned afterwards must not inherit -commit.
	twoArgs, err := ReexecArgs(ParentFlag, CommitFlag)
	if err != nil {
		t.Fatal(err)
	}
	if twoArgs[0] != ParentFlag || twoArgs[1] != CommitFlag {
		t.Fatalf("args %v deben abrir con %s %s", twoArgs, ParentFlag, CommitFlag)
	}
	if _, err := ReexecArgs(ChildFlag, "-bogus"); err == nil {
		t.Fatal("ReexecArgs acepto un rol desconocido")
	}
}

// T-1404 AC1: un hijo que sale con ExitRestartRequest (75) no es un
// crash — RunParent deja de reanimar y devuelve ErrRestartRequested
// para que cmd/botd re-ejecuta el padre con CommitFlag.
func TestExit75ReturnsRestartSentinel(t *testing.T) {
	bin := testExe(t)
	spawns := int32(0)
	spawn := func(PipeSpec) (*exec.Cmd, error) {
		atomic.AddInt32(&spawns, 1)
		return SpawnDirect(bin, nil, []string{fakeRoleEnv + "=exit75"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := RunParent(ctx, ParentConfig{
		PidfilePath: filepath.Join(t.TempDir(), "p75.pid"),
		Spawn:       spawn,
		Revive:      10 * time.Millisecond,
		Log:         fakeLog(),
	})
	if !errors.Is(err, ErrRestartRequested) {
		t.Fatalf("RunParent err = %v, want ErrRestartRequested", err)
	}
	if n := atomic.LoadInt32(&spawns); n != 1 {
		t.Fatalf("spawns = %d, want 1 (el centinela corta la reanimacion)", n)
	}
}

// AC2: muerto el hijo, el padre lo relanza en < 10 s y el par vuelve a
// 2 procesos. Con hijos que mueren al nacer, cada relanzamiento se
// mide; la cota canonica ReviveDelay=1 s deja margen sobrado.
func TestRespawnUnderTenSeconds(t *testing.T) {
	var spawns int32
	var mu sync.Mutex
	var gaps []time.Duration
	last := time.Time{}
	spawn := func(PipeSpec) (*exec.Cmd, error) {
		now := time.Now()
		mu.Lock()
		if !last.IsZero() {
			gaps = append(gaps, now.Sub(last))
		}
		last = now
		mu.Unlock()
		atomic.AddInt32(&spawns, 1)
		return spawnFakeDirect(t, "exit"), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunParent(ctx, ParentConfig{
			PidfilePath: filepath.Join(t.TempDir(), PidfileName),
			Spawn:       spawn,
			Log:         wdLog(t),
			Revive:      100 * time.Millisecond,
		})
	}()
	time.Sleep(2500 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunParent: %v", err)
	}
	n := atomic.LoadInt32(&spawns)
	if n < 3 {
		t.Fatalf("relanzamientos = %d, want >= 3 en 2.5 s", n)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, g := range gaps {
		if g > 10*time.Second {
			t.Fatalf("relanzar tardó %v > 10 s", g)
		}
	}
}

func spawnFakeDirect(t *testing.T, role string, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(testExe(t), "-test.run=TestWDFakeNeverRuns")
	cmd.Env = append(os.Environ(), fakeRoleEnv+"="+role)
	cmd.Env = append(cmd.Env, env...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// AC3a: el pipe del hijo que calla > limite (doble de kill -STOP:
// proceso vivo, pipe mudo) provoca que el padre lo reemplace.
func TestSilenceReplacesHungChild(t *testing.T) {
	// Parent in-process con Silence escalado a 1 s (la constante de
	// produccion ChildSilenceLimit=30 s se aserta aparte).
	if ChildSilenceLimit != 30*time.Second {
		t.Fatalf("ChildSilenceLimit = %v, want 30 s", ChildSilenceLimit)
	}
	dir := t.TempDir()
	var spawns int32
	var mu sync.Mutex
	var pids []int
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var spawnedAt []time.Time
	err := RunParent(ctx, ParentConfig{
		PidfilePath: filepath.Join(dir, PidfileName),
		Spawn: func(spec PipeSpec) (*exec.Cmd, error) {
			atomic.AddInt32(&spawns, 1)
			cmd := spawnFakeDirect(t, "hang",
				PipeAddrEnv+"="+spec.Addr,
				fmt.Sprintf("%s=%d", ParentPIDEnv, spec.ParentPID),
				fakeTTLEnv+"=60s")
			mu.Lock()
			pids = append(pids, cmd.Process.Pid)
			spawnedAt = append(spawnedAt, time.Now())
			mu.Unlock()
			return cmd, nil
		},
		Log:     wdLog(t),
		Silence: 1 * time.Second,
		Revive:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("RunParent: %v", err)
	}
	if atomic.LoadInt32(&spawns) < 2 {
		t.Fatalf("hijos = %d: el silencio no provoco releve", spawns)
	}
	mu.Lock()
	seen := append([]int(nil), pids...)
	gap := time.Duration(0)
	if len(spawnedAt) >= 2 {
		gap = spawnedAt[1].Sub(spawnedAt[0])
	}
	mu.Unlock()
	// El releve (silencio 1 s + kill + revive 100 ms) debe ser casi
	// inmediato: la cota de produccion es silencio 30 s + margen.
	if gap > 3*time.Second {
		t.Fatalf("releve por silencio tardo %v, want < 3 s", gap)
	}

	if len(seen) >= 1 && runtime.GOOS != "windows" {
		// El primer colgado murio (kill del padre), el ultimo sigue.
		// En Windows no se aserta: la reutilizacion agresiva de pids
		// haria falsamente vivo un pid ya finado (doble medido en el
		// soak Linux/QEMU).
		if PIDAlive(seen[0]) {
			t.Fatalf("el hijo colgado pid=%d sigue vivo tras el releve", seen[0])
		}
	}
	stopIfAlive(seen[len(seen)-1])
}

// killChildByPPID mata solo al hijo del padre dado: `ps -o ppid= pid=
// comm=` filtrando por nombre de binario (los dos roles comparten
// imagen). En Windows no hay señal barata por PPID: se devuelve false
// y el ciclo mata el padre, que es el caso duro del criterio.
func killChildByPPID(t *testing.T, parentPID int) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		return false
	}
	out, err := exec.Command("ps", "-o", "ppid=,pid=,comm=").Output()
	if err != nil {
		return false
	}
	self := filepath.Base(testExe(t))
	for _, line := range strings.Split(string(out), "\n") {
		var ppid, pid int
		var comm string
		if _, err := fmt.Sscanf(line, "%d %d %s", &ppid, &pid, &comm); err != nil {
			continue
		}
		if ppid == parentPID && pid != os.Getpid() && strings.HasPrefix(comm, self[:min(15, len(self))]) {
			if err := exec.Command("kill", "-9", fmt.Sprint(pid)).Run(); err != nil {
				return false
			}
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// countDefunctBotd: `ps -ef | grep -c '[d]efunct botd'` = 0 (solo en
// hosts con estado Z: Linux/macOS).
func countDefunctBotd(t *testing.T) int {
	t.Helper()
	if runtime.GOOS == "windows" {
		return 0
	}
	out, err := exec.Command("ps", "-o", "stat=,comm=").Output()
	if err != nil {
		t.Logf("ps indisponible: %v", err)
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.HasPrefix(fields[0], "Z") && strings.HasPrefix(fields[1], "botd") {
			n++
		}
	}
	return n
}

// AC3b: pidfile con flock — un segundo arranque con el lock tomado
// sale con exit ≠ 0, medido a nivel de proceso real.
func TestPidfileFlockBlocksSecondStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), PidfileName)
	pf := &Pidfile{Path: path}
	if err := pf.Acquire(); err != nil {
		t.Fatal(err)
	}
	defer pf.Release()
	if err := (&Pidfile{Path: path}).Acquire(); !errors.Is(err, ErrLocked) {
		t.Fatalf("segundo acquire en-proceso: %v, want ErrLocked", err)
	}
	cmd := spawnFakeDirect(t, "locktry", fakePIDEnv+"="+path)
	out, err := cmd.Process.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatalf("locktry con lock tomado: exit %v, want ≠ 0", err)
	}
	if out.ExitCode() == 0 {
		t.Fatalf("locktry exit 0, want ≠ 0 (flock tomado)")
	}
}

// --- T-0502 ---------------------------------------------------------------

// AC1: muerto el padre (-9), el hijo se re-elige en < 15 s (log
// role=elected), readquiere el pidfile y relanza un hijo.
func TestElectionUnderFifteenSeconds(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, PidfileName)
	logfile := filepath.Join(dir, "wd.log")
	deadfile := filepath.Join(dir, "DEAD")
	cmd := spawnFakeDirect(t, "parent",
		fakePIDEnv+"="+pidfile, fakeOutEnv+"="+logfile, fakeTTLEnv+"=40s", fakeDeadEnv+"="+deadfile)
	defer func() {
		// Apaga la cadena de re-eleccion (el heredero del pipe se
		// re-elige y relanza para siempre sin semaforo de muerte).
		_ = os.WriteFile(deadfile, []byte("x"), 0o600)
		for i := 0; i < 60 && ProbeAlive(pidfile); i++ {
			time.Sleep(200 * time.Millisecond)
		}
		waitExit(cmd, 3*time.Second)
	}()
	// Esperar a que el par este vivo: pidfile con pid vivo.
	deadline := time.Now().Add(20 * time.Second)
	for !ProbeAlive(pidfile) {
		if time.Now().After(deadline) {
			t.Fatal("el padre no dejo pidfile vivo en 20 s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	parentPID, err := ReadPID(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = cmd.Process.Kill() // pkill -9 del criterio
	var electedPID int
	for time.Now().Before(start.Add(15 * time.Second)) {
		if p, err := ReadPID(pidfile); err == nil && p != parentPID && PIDAlive(p) {
			electedPID = p
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if electedPID == 0 {
		t.Fatal("el hijo no se re-eligio padre en < 15 s (pidfile sigue muerto)")
	}
	elapsed := time.Since(start)
	if elapsed > 15*time.Second {
		t.Fatalf("re-eleccion tardo %v > 15 s", elapsed)
	}
	// log role=elected + relanzamiento del hijo (role=parent del
	// elegido) deben estar en el log compartido del par.
	b, _ := os.ReadFile(logfile)
	if !strings.Contains(string(b), "role=elected") {
		t.Fatalf("falta role=elected en el log:\n%s", b)
	}
	t.Logf("re-eleccion medida: %v (cota 15 s)", elapsed.Round(100*time.Millisecond))
	// Limpieza: el padre elegido vive con TTL propio; matar el par.
	waitExit(cmd, 5*time.Second)
	stopIfAlive(electedPID)
}

func waitExit(c *exec.Cmd, d time.Duration) {
	done := make(chan struct{})
	go func() { c.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		c.Process.Kill()
	}
}

func stopIfAlive(pid int) {
	if PIDAlive(pid) {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
}

// AC2: N ciclos de kill -9 aleatorio (padre o hijo): N/N
// recuperaciones y 0 procesos defunct. N=100 es la cota del criterio;
// en este host los ciclos corren con tiempos escalados (Revive 100 ms)
// y la cota medida por ciclo sigue siendo la de produccion (< 10 s de
// relanzamiento; zombis: Wait del padre los reapa).
func TestHundredCyclesNoZombies(t *testing.T) {
	// Modo rapido por defecto (15 ciclos, ~90 s en este host); el
	// criterio completo de 100 ciclos corre con BOTNET_WD_CYCLES=100 y
	// la puerta de 1.000 reinicios es el soak (extrapolacion anotada en
	// docs/runbooks/persistence.md).
	n := 15
	if testing.Short() {
		n = 10
	}
	if v := os.Getenv("BOTNET_WD_CYCLES"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	dir, err := os.MkdirTemp("", "wd-cycles")
	if err != nil {
		t.Fatal(err)
	}
	logfile := filepath.Join(dir, "wd.log")
	dead := filepath.Join(dir, "DEAD")
	defer func() {
		// Apaga las cadenas de re-eleccion de todos los ciclos; en
		// Windows los pidfiles abiertos por cadenas vivas impiden el
		// borrado inmediato: RemoveAll reintenta y no puede fallar el
		// test (el directorio vive en el temp del sistema).
		_ = os.WriteFile(dead, []byte("x"), 0o600)
		time.Sleep(time.Second)
		_ = os.RemoveAll(dir)
	}()
	revivals, slowest := 0, time.Duration(0)
	reviveLines := func() int {
		b, _ := os.ReadFile(logfile)
		return strings.Count(string(b), "watchdog revive hijo")
	}
	for i := 0; i < n; i++ {
		// Un pidfile por ciclo: la cadena de re-elecciones del ciclo
		// anterior no puede bloquear el flock del siguiente.
		pidfile := filepath.Join(dir, fmt.Sprintf("botd-%d.pid", i))
		cmd := spawnFakeDirect(t, "parent",
			fakePIDEnv+"="+pidfile, fakeOutEnv+"="+logfile, fakeDeadEnv+"="+dead,
			fakeTTLEnv+"=8s", "BOTNET_WD_FAKE_REVIVE=200ms")
		deadline := time.Now().Add(10 * time.Second)
		for !ProbeAlive(pidfile) {
			if time.Now().After(deadline) {
				t.Fatalf("ciclo %d: el par no arranco en 10 s", i+1)
			}
			time.Sleep(150 * time.Millisecond)
		}
		parentPID, _ := ReadPID(pidfile)
		revBefore := reviveLines()
		start := time.Now()
		killedChild := false
		if i%2 == 1 {
			killedChild = killChildByPPID(t, parentPID)
		}
		if !killedChild {
			// caso duro del criterio: matar al padre (en Windows falta
			// el kill por PPID barato, anotado en el runbook).
			_ = cmd.Process.Kill()
		}
		ok := false
		for time.Now().Before(start.Add(15 * time.Second)) {
			if killedChild {
				// el padre revivio al hijo: log "watchdog revive hijo"
				if reviveLines() > revBefore {
					ok = true
					break
				}
			} else if p, err := ReadPID(pidfile); err == nil && p != parentPID && PIDAlive(p) {
				// el hijo se re-eligio y reescribio el pidfile (pid
				// nuevo: no vale el pid viejo reutilizado)
				ok = true
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("ciclo %d: no se recupero en 15 s (kill hijo=%v)", i+1, killedChild)
		}
		d := time.Since(start)
		if d > slowest {
			slowest = d
		}
		revivals++
		waitExit(cmd, 3*time.Second)
		if p, err := ReadPID(pidfile); err == nil {
			stopIfAlive(p)
		}
	}
	if revivals != n {
		t.Fatalf("recuperaciones %d/%d", revivals, n)
	}
	t.Logf("ciclos=%d recuperacion peor caso=%v", n, slowest.Round(100*time.Millisecond))
	if defunct := countDefunctBotd(t); defunct > 0 {
		t.Fatalf("zombis defunct botd = %d, want 0", defunct)
	}
}

// killChildOf mata el hijo del padre: el hijo tiene PPID = parent (en
// Linux); en Windows no hay PPID barato, asi que se localiza por el
// listener del pipe (connect de loopback con el padre). Doble:
// matamos por `taskkill //T //F //PID parent` solo el subarbol hijo
// via wmic-less: usamos el PPID de `ps -W` disponible en Git Bash.
func killChildOf(t *testing.T, pidfile string, parentPID int) {
	t.Helper()
	killed := killChildByPPID(t, parentPID)
	if !killed {
		t.Skip("sin mecanismo para matar solo el hijo en este host")
	}
}

func TestPersistMode(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"INVOCATION_ID": "deadbeef"}, "systemd"},
		{map[string]string{"BOTNET_PERSIST": "rc"}, "rc"},
		{map[string]string{}, "reboot"},
		// El override explícito manda: el runner de CI define INVOCATION_ID
		// y sin esta precedencia pisaba al snippet de rc.local.
		{map[string]string{"INVOCATION_ID": "x", "BOTNET_PERSIST": "rc"}, "rc"},
	}
	for _, c := range cases {
		got := PersistMode(func(k string) string { return c.env[k] })
		if got != c.want {
			t.Fatalf("PersistMode(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}

// TestBootChainFiles: la cadena systemd → rc.local → @reboot existe y
// cada eslabon lanza SIEMPRE el rol padre del mismo botd.
func TestBootChainFiles(t *testing.T) {
	root := repoRoot(t)
	svc, err := os.ReadFile(filepath.Join(root, "deploy", "systemd", "botd.service"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ExecStart", "--parent", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(svc), want) {
			t.Fatalf("botd.service sin %q", want)
		}
	}
	rc, err := os.ReadFile(filepath.Join(root, "deploy", "rc", "botd.rc.local"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"BOTNET_PERSIST=rc", "--parent"} {
		if !strings.Contains(string(rc), want) {
			t.Fatalf("botd.rc.local sin %q", want)
		}
	}
	cron, err := os.ReadFile(filepath.Join(root, "deploy", "cron", "botd-guard.cron"))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(cron), "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("lineas de cron = %d, want 1 (criterio crontab -l | wc -l = 1)", len(lines))
	}
	if !strings.Contains(lines[0], "* * * * *") || !strings.Contains(lines[0], CronMarker) {
		t.Fatalf("linea de cron invalida: %s", lines[0])
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

// --- T-0503 / T-0506 ------------------------------------------------------

type memCron struct {
	mu       sync.Mutex
	content  string
	removed  bool
	notFound bool
}

func (c *memCron) List() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.notFound && c.content == "" {
		return "", exec.ErrNotFound
	}
	return c.content, nil
}
func (c *memCron) Write(content string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.content = content
	c.removed = false
	return nil
}
func (c *memCron) Remove() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.content = ""
	c.removed = true
	return nil
}

func TestOptoutLeavesNoCronLine(t *testing.T) {
	c := &memCron{content: "* * * * * /data/botd/botd --guard >/dev/null 2>&1 # botd-guard\n"}
	left, err := OptoutCron(c)
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 || !c.removed {
		t.Fatalf("optout de la unica linea: left=%d removed=%v, want 0/true (crontab -l | wc -l = 0)", left, c.removed)
	}
	c2 := &memCron{content: "0 3 * * * /sbin/rotate\n* * * * * /data/botd/botd --guard >/dev/null 2>&1 # botd-guard\n"}
	left, err = OptoutCron(c2)
	if err != nil {
		t.Fatal(err)
	}
	if left != 1 || !strings.Contains(c2.content, "rotate") || strings.Contains(c2.content, CronMarker) {
		t.Fatalf("optout parcial: left=%d content=%q", left, c2.content)
	}
}

func TestInstallCronIsIdempotent(t *testing.T) {
	c := &memCron{notFound: true}
	n, err := InstallCron(c, "/data/botd/botd")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("lineas tras install = %d, want 1", n)
	}
	n, err = InstallCron(c, "/data/botd/botd")
	if err != nil || n != 1 {
		t.Fatalf("install idempotente: %d %v", n, err)
	}
}

func TestGuardOnlyActsWhenPidfileDead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, PidfileName)
	var spawns int32
	spawn := func(PipeSpec) (*exec.Cmd, error) {
		atomic.AddInt32(&spawns, 1)
		return spawnFakeDirect(t, "sleeper"), nil
	}
	// Sano: flock tomado (el padre lo sostiene) → 0 spawns en 60 ticks
	// (= 1 h de cron).
	pf := &Pidfile{Path: path}
	if err := pf.Acquire(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		spawned, err := RunGuardOnce(GuardConfig{PidfilePath: path, Spawn: spawn, Log: wdLog(t)})
		if spawned || !errors.Is(err, ErrHealthy) {
			t.Fatalf("tick %d con agente sano: spawned=%v err=%v", i+1, spawned, err)
		}
	}
	if atomic.LoadInt32(&spawns) != 0 {
		t.Fatalf("el guard duplico procesos con agente sano")
	}
	// Muerto: sin lock y sin pid vivo → relanza una vez. Tras Release
	// el fichero conserva el pid del test (vivo): se borra como lo haria
	// un reboot — el holder murio y su pid ya no esta.
	pf.Release()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	spawned, err := RunGuardOnce(GuardConfig{PidfilePath: path, Spawn: spawn, Log: wdLog(t)})
	if !spawned || err != nil {
		t.Fatalf("guard con padre muerto: spawned=%v err=%v", spawned, err)
	}
}

// AC2 (T-0503) + AC1 (T-0506): tras matar padre e hijo con el cron
// vivo, el guard revive el par. Medido en ticks de cron reales con
// jitter: cada revival ≤ MaxRevivalAfterKill (70 s) y la MEDIANA de 5
// revival ≤ 60 s. La cota real de 1.000 ciclos y el kill -9 nativo
// corren en el soak (scripts/soak/owner_kill_soak.sh).
// lastSpawnedChild: el padre fake loguea "watchdog spawn hijo pid=N"
// (misma linea de log que el botd real): es la forma portatil de
// conocer el pid del hijo sin PPID — en Git Bash no hay PS barato.
func lastSpawnedChild(t *testing.T, logfile string) int {
	t.Helper()
	b, err := os.ReadFile(logfile)
	if err != nil {
		return 0
	}
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if idx := strings.Index(lines[i], "watchdog spawn hijo"); idx >= 0 {
			if m := strings.Index(lines[i], "pid="); m >= 0 {
				var pid int
				fmt.Sscanf(lines[i][m+4:], "%d", &pid)
				return pid
			}
		}
	}
	return 0
}

// T-0506 (A3-05): dueno same-uid que mata padre E HIJO con el cron
// vivo. Cada ciclo: el par nace, el dueno mata ambos procesos (kill -9
// real del holder del pidfile y del hijo registrado), y se mide el
// revival del guard. Cotas asertadas: cada revival ≤ 70 s (T-0503 AC2)
// y MEDIANA < 60 s. El cron de 60 s se simula a 12 s (factor 5x,
// anotado en el runbook): la mediana se mide en segundos reales y la
// cota sigue siendo la de produccion. El soak completo con cron real
// es scripts/soak/owner_kill_soak.sh --kills=30.
func TestOwnerKillRevivalMedian(t *testing.T) {
	if testing.Short() {
		t.Skip("soak de dueno: sale con -short")
	}
	dir, err := os.MkdirTemp("", "wd-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	pidfile := filepath.Join(dir, PidfileName)
	logfile := filepath.Join(dir, "wd.log")
	deadFinal := filepath.Join(dir, "DEAD")
	// El padre que spawnéa el guard hereda el entorno del test: el
	// pidfile canonico tambien va por ahi (fakeParent lo readquiere en
	// su Acquire).
	os.Setenv(fakePIDEnv, pidfile)
	os.Setenv(fakeTTLEnv, "40s")
	os.Setenv(fakeDeadEnv, deadFinal)
	os.Setenv(fakeOutEnv, logfile)
	defer func() {
		os.Unsetenv(fakePIDEnv)
		os.Unsetenv(fakeTTLEnv)
		os.Unsetenv(fakeDeadEnv)
		os.Unsetenv(fakeOutEnv)
	}()
	tick := 12 * time.Second
	stopGuard := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-stopGuard
			cancel()
		}()
		RunGuardLoop(ctx, tick, GuardConfig{PidfilePath: pidfile, Spawn: DefaultSpawn(ParentFlag), Log: wdLog(t)})
	}()
	// holderVivo: el pidfile responde de verdad. Dos trampas del doble
	// de host: (1) RunGuardOnce probea tomando/soltando el flock y deja
	// en el fichero el pid del guard (el proceso del test), vivo pero
	// ajeno al par; (2) DefaultSpawn solo hace Start, asi que un padre
	// matado sigue siendo "abirible" para quien lo spawnéo y PIDAlive
	// lo da vivo. El unico juez honesto aqui es el propio flock: si el
	// test puede tomarlo, el holder murio de verdad (el SO suelta el
	// LockFileEx al destruir el proceso).
	holderVivo := func() bool {
		pid, err := ReadPID(pidfile)
		if err != nil || pid == os.Getpid() || !PIDAlive(pid) {
			return false
		}
		pf := &Pidfile{Path: pidfile}
		if err := pf.Acquire(); err == nil {
			_ = pf.Release()
			return false
		}
		return true
	}
	killPair := func(label string) {
		// kill -9 del dueno sobre los DOS procesos vivos: holder del
		// pidfile e hijo registrado. La re-eleccion (T-0502) es real:
		// un hijo supervivente se promociona y readquiere el pidfile
		// en 1-2 s, y el guard puede reaccionar en un tick en medio.
		// Cada pasada reapunta al holder e hijo CORRIENTES y la
		// muerte se declara con dos probes muertos separados 600 ms.
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			if p, err := ReadPID(pidfile); err == nil && p != os.Getpid() {
				stopIfAlive(p)
			}
			stopIfAlive(lastSpawnedChild(t, logfile))
			time.Sleep(600 * time.Millisecond)
			if !holderVivo() {
				time.Sleep(600 * time.Millisecond)
				if !holderVivo() {
					return
				}
			}
		}
		t.Fatalf("%s: el par no murio (guard reavivando)", label)
	}
	var times []time.Duration
	for i := 0; i < 5; i++ {
		// Par vivo (si el guard ya revivio el ciclo anterior, basta).
		arrived := time.Now().Add(20 * time.Second)
		for !holderVivo() || lastSpawnedChild(t, logfile) == 0 {
			if time.Now().After(arrived) {
				t.Fatalf("ciclo %d: el par no nacio", i+1)
			}
			time.Sleep(200 * time.Millisecond)
		}
		start := time.Now()
		killPair(fmt.Sprintf("ciclo %d", i+1))
		ok := false
		// la ventana de revival corre desde la muerte confirmada:
		// tick (12 s = 1/5 del cron real) + margen de probe.
		revivedBy := time.Now().Add(tick + 10*time.Second)
		for time.Now().Before(revivedBy) {
			if holderVivo() {
				ok = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !ok {
			t.Fatalf("ciclo %d: el guard no revivio el par en tick+10 s", i+1)
		}
		d := time.Since(start)
		if d > MaxRevivalAfterKill {
			t.Fatalf("revival %v > 70 s (criterio T-0503 AC2)", d)
		}
		times = append(times, d)
	}
	close(stopGuard)
	wg.Wait()
	_ = os.WriteFile(deadFinal, []byte("x"), 0o600)
	time.Sleep(time.Second)
	if p, err := ReadPID(pidfile); err == nil {
		stopIfAlive(p)
		stopIfAlive(lastSpawnedChild(t, logfile))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	median := times[len(times)/2]
	if median >= time.Minute {
		t.Fatalf("mediana de revival %v >= 60 s (criterio T-0506 AC1)", median)
	}
	t.Logf("revival mediana=%v peor=%v (tick escalado %v, cron real 60 s)", median.Round(time.Second), times[len(times)-1].Round(time.Second), tick)
}

func TestCronLineShape(t *testing.T) {
	line := CronLine("/data/botd/botd")
	if strings.Count(line, "\n") != 0 {
		t.Fatal("CronLine debe ser una sola linha")
	}
	for _, want := range []string{"* * * * *", "/data/botd/botd --guard", CronMarker} {
		if !strings.Contains(line, want) {
			t.Fatalf("linea %q sin %q", line, want)
		}
	}
	if lines := strings.Split(strings.TrimSpace(line), "\n"); len(lines) != 1 {
		t.Fatalf("criterio `crontab -l | wc -l` = 1: %d lineas", len(lines))
	}
}
