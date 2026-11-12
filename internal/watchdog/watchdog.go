package watchdog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Variables de entorno del par watchdog: el padre escucha un pipe TCP
// loopback (el "pipe mutuo" del BLUEPRINT §4: en dispositivos sin
// unix sockets fiables, un loopback TCP es el pipe que siempre anda)
// y pasa su direccion al hijo reexec por entorno.
const (
	PipeAddrEnv  = "BOTNET_WD_PIPE" // 127.0.0.1:port que el hijo debe marcar
	WDTokenEnv   = "BOTNET_WD_TOKEN"
	ParentPIDEnv = "BOTNET_WD_PPID"
	// RoleChildFlag/RoleParentFlag son los unicos dos roles de proceso
	// del mismo binario (T-0501 AC1: 0 segundo binario en disco).
	ChildFlag  = "-child"
	ParentFlag = "-parent"
	// CommitFlag marks the self-update commit run: when a child exits
	// with ExitRestartRequest the parent re-execs its own binary with
	// this flag, so the restarted process commits the staged version
	// before resuming its role (T-1404 AC1). It is stripped from every
	// child spawn: only the restarting process commits, never the
	// revived children.
	CommitFlag = "-commit"
)

// ExitRestartRequest is the exit code the agent uses to ask the whole
// process pair to restart once a self-update has been staged (BLUEPRINT
// §4: download -> botd.new -> rename -> exit 75; the value is
// internal/agent's ExitNeedRestart). The constant is local on purpose:
// watchdog must not import internal/agent, and importing the agent
// constant from here would point the dependency the wrong way.
const ExitRestartRequest = 75

// ErrRestartRequested is returned by RunParent when the child exited
// with ExitRestartRequest instead of crashing: reviving another child
// would keep running the stale image forever, so the caller (cmd/botd
// --parent) must re-exec the same binary with CommitFlag (T-1404 AC1).
var ErrRestartRequested = errors.New("watchdog: restart requested (exit 75)")

// Budgets canonicos del ciclo de vigilancia (T-0501/T-0502).
const (
	// PingEvery: latido del hijo por el pipe.
	PingEvery = 5 * time.Second
	// ChildSilenceLimit: el padre mata al hijo si su pipe calla mas de
	// 30 s (kill -STOP incluido: un STOP no cierra el socket, el
	// silencio si lo nota el deadline de lectura).
	ChildSilenceLimit = 30 * time.Second
	// ReviveDelay: margen entre muerte del hijo y relanzamiento — el
	// criterio pide < 10 s.
	ReviveDelay = time.Second
	// ParentWatchInterval: cada cuanto comprueba el hijo si el padre
	// sigue vivo (re-eleccion en < 15 s, T-0502).
	ParentWatchInterval = 2 * time.Second
	// MaxRevivalAfterKill: cota del guard (cron cada 60 s + spawn) para
	// el criterio "revived en ≤ 70 s" (T-0503 AC2).
	MaxRevivalAfterKill = 70 * time.Second
)

// ErrDouble: un segundo arranque del watchdog encuentra el flock del
// pidfile tomado — main lo traduce a exit ≠ 0 (T-0501 AC3).
var ErrDouble = ErrLocked

// PipeSpec es lo que el padre handoffea al hijo por entorno.
type PipeSpec struct {
	Addr      string
	Token     string
	ParentPID int
}

// Env devuelve el entorno extra para el reexec del hijo.
func (p PipeSpec) Env() []string {
	return []string{
		PipeAddrEnv + "=" + p.Addr,
		WDTokenEnv + "=" + p.Token,
		fmt.Sprintf("%s=%d", ParentPIDEnv, p.ParentPID),
	}
}

// SpawnFunc lanza el proceso hijo. DefaultSpawn reexecuta el MISMO
// binario; en tests se inyecta el test-binary con dobles rapidos.
type SpawnFunc func(PipeSpec) (*exec.Cmd, error)

// ParentConfig configura el rol padre.
type ParentConfig struct {
	PidfilePath string
	Spawn       SpawnFunc
	Silence     time.Duration // ChildSilenceLimit por defecto
	Revive      time.Duration // ReviveDelay por defecto
	Log         *slog.Logger
	Budget      *RestartBudget // nil → sin escalada de presupuesto
	Adopted     *Pidfile       // pidfile ya adquirido (hijo re-elegido)
}

func (c *ParentConfig) defaults() {
	if c.Silence <= 0 {
		c.Silence = ChildSilenceLimit
	}
	if c.Revive <= 0 {
		c.Revive = ReviveDelay
	}
	if c.Log == nil {
		c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
}

// RunParent es el bucle del rol padre: flock del pidfile, pipe mutuo y
// reexec del mismo binario. Muere el hijo (o calla su pipe) → revive
// en ~ReviveDelay respetando el RestartBudget (T-1301). Devuelve
// ErrDouble si otro proceso sostiene el pidfile, y ErrRestartRequested
// si el hijo salio con ExitRestartRequest en vez de crashar (T-1404).
func RunParent(ctx context.Context, cfg ParentConfig) error {
	cfg.defaults()
	pf := cfg.Adopted
	if pf == nil {
		pf = &Pidfile{Path: cfg.PidfilePath}
		if err := pf.Acquire(); err != nil {
			return err
		}
	}
	return parentLoop(ctx, cfg, pf)
}

func parentLoop(ctx context.Context, cfg ParentConfig, pf *Pidfile) error {
	defer pf.Release()
	if cfg.Spawn == nil {
		return errors.New("watchdog: ParentConfig sin Spawn")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("watchdog: pipe loopback: %w", err)
	}
	defer listener.Close()
	token := fmt.Sprintf("wd-%d-%d", os.Getpid(), time.Now().UnixNano())
	cfg.Log.Info("watchdog role=parent", "pid", os.Getpid(), "pipe", listener.Addr().String(), "pidfile", pf.Path)

	silence := make(chan struct{}, 1)
	go acceptLoop(listener, silence, cfg.Silence)

	for ctx.Err() == nil {
		select {
		case <-silence: // sena de la encarnacion anterior
		default:
		}
		spec := PipeSpec{Addr: listener.Addr().String(), Token: token, ParentPID: os.Getpid()}
		cmd, err := cfg.Spawn(spec)
		if err != nil {
			cfg.Log.Warn("watchdog spawn fallo", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(cfg.Revive):
			}
			continue
		}
		cfg.Log.Info("watchdog spawn hijo", "pid", cmd.Process.Pid)
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()

		start := time.Now()
		cause := ""
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil
		case err := <-exited:
			cause = fmt.Sprintf("hijo salio: %v", err)
		case <-silence:
			cause = "silencio de pipe > limite"
			_ = cmd.Process.Kill()
			<-exited
		}
		if ctx.Err() != nil {
			return nil
		}
		// T-1404 AC1: exit 75 is not a crash — the child staged a
		// self-update and wants the whole process restarted. Stop
		// reviving here (before the budget escalation, which belongs to
		// crash-loops, not to planned restarts) and hand the decision
		// to the caller via the sentinel.
		if ps := cmd.ProcessState; ps != nil && ps.ExitCode() == ExitRestartRequest {
			cfg.Log.Info("watchdog restart requested", "exit", ExitRestartRequest, "duracion", time.Since(start).Round(100*time.Millisecond))
			return ErrRestartRequested
		}
		delay := cfg.Revive
		if cfg.Budget != nil {
			delay = cfg.Budget.RecordKill(time.Now())
		}
		cfg.Log.Info("watchdog revive hijo", "causa", cause, "duracion", time.Since(start).Round(100*time.Millisecond), "delay", delay)
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil
		case <-time.After(delay):
		}
	}
	return nil
}

// acceptLoop sirve una conexion por encarnacion del hijo: cada linea
// leida reanima el deadline; si el pipe calla mas de silence, se
// notifica silences (un disparo por encarnacion) y se espera a que el
// socket muera con el hijo para aceptar al siguiente.
func acceptLoop(listener net.Listener, silences chan<- struct{}, silence time.Duration) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		fired := false
		deadline := time.Now().Add(silence)
		r := bufio.NewReader(conn)
		for {
			_ = conn.SetReadDeadline(deadline)
			line, rerr := r.ReadString('\n')
			if rerr != nil {
				if ne, ok := rerr.(net.Error); ok && ne.Timeout() && !fired {
					fired = true
					select {
					case silences <- struct{}{}:
					default:
					}
					continue // deadline renovado: el kill puede tardar y el pipe seguir vivo tras el releve
				}
				break
			}
			if strings.TrimSpace(line) == "quit" {
				break
			}
			deadline = time.Now().Add(silence)
		}
		conn.Close()
	}
}

// ErrParentLost: el pipe del padre murio — el hijo se re-elige (T-0502).
var ErrParentLost = errors.New("watchdog: padre perdido")

// ChildPipe es el lado del hijo del pipe mutuo.
type ChildPipe struct{ conn net.Conn }

// DialChildPipe abre el pipe con el padre descrito por el entorno. Sin
// BOTNET_WD_PIPE no hay padre: el agente corre standalone (modo
// historico de EP-01..EP-04).
func DialChildPipe() (*ChildPipe, error) {
	addr := os.Getenv(PipeAddrEnv)
	if addr == "" {
		return nil, nil
	}
	for attempt := 0; attempt < 10; attempt++ {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			return &ChildPipe{conn: conn}, nil
		}
		if attempt == 9 {
			return nil, fmt.Errorf("watchdog: pipe del padre %s inaccesible: %w", addr, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, nil
}

// Ping es el lazo vivo: un latido hijo→padre; nil mientras el pipe
// aguante. El padre no devuelve pong: la muerte del padre cierra el
// socket y el write peta (backstop: PIDAlive del ppid en WatchParent).
func (p *ChildPipe) Ping() error {
	if p == nil {
		return nil
	}
	if err := p.conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	_, err := io.WriteString(p.conn, "ping\n")
	return err
}

// Close corta el pipe limpiamente (bye del hijo).
func (p *ChildPipe) Close() error {
	if p == nil {
		return nil
	}
	_, _ = io.WriteString(p.conn, "quit\n")
	return p.conn.Close()
}

// WatchParent es el guard del hijo (T-0502 AC1): si el padre muere, su
// listener TCP muere con el — el Ping falla o el pid desaparece y
// devuelve ErrParentLost para que comience la re-eleccion.
func WatchParent(ctx context.Context, p *ChildPipe, log *slog.Logger) error {
	if p == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	ppid := os.Getppid
	if env := os.Getenv(ParentPIDEnv); env != "" {
		var n int
		if _, err := fmt.Sscanf(env, "%d", &n); err == nil && n > 0 {
			ppid = func() int { return n }
		}
	}
	for {
		if err := p.Ping(); err != nil {
			if log != nil {
				log.Info("watchdog: pipe del padre cae", "motivo", err)
			}
			return ErrParentLost
		}
		if pid := ppid(); pid > 0 && pid != os.Getpid() && !PIDAlive(pid) {
			return ErrParentLost
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ParentWatchInterval):
		}
	}
}

// ReexecArgs calcula el reexec del MISMO binario: se quitan los flags
// de rol y -run-for (el reloj de vida es del padre) y se anaden los
// roles pedidos. Es el mecanismo de T-0501 AC1: relanzar no instalar.
// It accepts more than one role flag so the parent can re-exec itself
// with both ParentFlag and CommitFlag (T-1404 AC1).
func ReexecArgs(roleFlags ...string) ([]string, error) {
	for _, rf := range roleFlags {
		if rf != ChildFlag && rf != ParentFlag && rf != CommitFlag {
			return nil, fmt.Errorf("watchdog: rol %q invalido", rf)
		}
	}
	var out []string
	skip := false
	for _, a := range os.Args[1:] {
		if skip {
			skip = false
			continue
		}
		switch {
		// El rol propio NO se hereda: el hijo del padre va con
		// ChildFlag (prepuesto abajo) y viceversa. Se toleran las dos
		// grafias del flag (-parent / --parent) porque `flag` del
		// stdlib acepta ambas y un --parent residual haria al doble
		// padre del padre (ErrDouble en bucle). --guard tambien se
		// limpia: si sobreviviera, el padre spawnéado por el guard se
		// creeria guard y spawnearia a su vez (cadena infinita).
		// -commit se limpia igual: solo el proceso que se reinicia
		// compromete la version, los hijos reanimados no (T-1404).
		case len(a) > 1 && a[0] == '-' &&
			(strings.TrimLeft(a, "-") == "parent" || strings.TrimLeft(a, "-") == "child" ||
				strings.TrimLeft(a, "-") == "guard" || strings.TrimLeft(a, "-") == "commit"):
			continue
		case a == "-run-for" || a == "--run-for":
			skip = true
			continue
		}
		if strings.HasPrefix(a, "-run-for=") || strings.HasPrefix(a, "--run-for=") {
			continue
		}
		out = append(out, a)
	}
	prepended := make([]string, 0, len(roleFlags)+len(out))
	prepended = append(prepended, roleFlags...)
	return append(prepended, out...), nil
}

// SameBinary: os.Executable con fallback tolerante (symlinks, test).
func SameBinary() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		if fi, serr := os.Stat(exe); serr == nil && !fi.IsDir() {
			return exe, nil
		}
	}
	if len(os.Args) > 0 {
		if fi, serr := os.Stat(os.Args[0]); serr == nil && !fi.IsDir() {
			return os.Args[0], nil
		}
	}
	return "", errors.New("watchdog: binario propio no localizable")
}

// DefaultSpawn es el reexec canonico: mismo binario, mismo config, rol
// hijo, pipe por entorno. Stdio heredado: el log del hijo vive donde
// vive el del padre (systemd journal / rc.log / cron).
func DefaultSpawn(roleFlag string) SpawnFunc {
	return func(spec PipeSpec) (*exec.Cmd, error) {
		exe, err := SameBinary()
		if err != nil {
			return nil, err
		}
		args, err := ReexecArgs(roleFlag)
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(exe, args...)
		cmd.Env = append(os.Environ(), spec.Env()...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return cmd, nil
	}
}

// SpawnDirect es el doble para tests y host sin fork limpio: lanza el
// mismo binario con los args heredados y el pipe por entorno.
func SpawnDirect(binary string, args []string, env []string) (*exec.Cmd, error) {
	if binary == "" {
		return nil, errors.New("watchdog: reexec sin binario")
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
