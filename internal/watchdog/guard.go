package watchdog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CronMarker marca la unica linea de cron del nodo: hace de @reboot y
// de guard (T-0503, A1-04). `botd optout` la busca por este marker
// para borrarla sin root.
const CronMarker = "botd-guard"

// CronLine es la linea canónica de un solo `* * * * *`: el guard
// sondea el pidfile y solo actua si no responde, de modo que la misma
// linea cubre el arranque tras reboot (primer tick: pidfile ausente →
// lanza al padre) y el revival del dueño same-uid (T-0503/T-0506).
func CronLine(binPath string) string {
	return fmt.Sprintf("* * * * * %s --guard >/dev/null 2>&1 # %s", binPath, CronMarker)
}

// ErrHealthy: el pidfile responde (flock tomado o pid vivo) — el guard
// no duplica procesos (T-0503 AC3).
var ErrHealthy = errors.New("watchdog: pidfile responde, guard inactivo")

// GuardConfig configura `botd --guard`, el un-shot que cron lanza
// cada minuto.
type GuardConfig struct {
	PidfilePath string
	Spawn       SpawnFunc // relanza el rol padre; DefaultSpawn(ParentFlag) por defecto
	Log         *slog.Logger
}

// RunGuardOnce es el cuerpo del cron guard: intenta el flock del
// pidfile. Si esta tomado (o el pid del pidfile vive), el padre esta
// al pie → ErrHealthy, cero procesos extra. Si el lock esta libre —
// el kernel lo solto al morir el dueño, o no hay pidfile—, el padre
// murio (o nunca hubo) y el guard lo relanza. Este gesto es el mismo
// para @reboot: tras el reboot no hay flock y no hay pidfile vivo.
func RunGuardOnce(cfg GuardConfig) (bool, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.Spawn == nil {
		cfg.Spawn = DefaultSpawn(ParentFlag)
	}
	// 1) Hijo de la linea de cron ANTES de tocar el fichero: si el pid
	// declarado vive Y el flock esta tomado, hay padre al pie. En hosts
	// donde el spawner sostiene el handle (Git Bash), un pid matado
	// sigue siendo "abririble" y PIDAlive miente: el lock liberado por
	// el SO al morir es el juez. Si PIDAlive=true pero el flock esta
	// libre, el pidfile es huero y el guard actua (en Linux/Unix
	// kill -0 sobre el pid muerto da el mismo resultado).
	pf := &Pidfile{Path: cfg.PidfilePath}
	if pid, err := ReadPID(cfg.PidfilePath); err == nil && pid != os.Getpid() && PIDAlive(pid) {
		if lerr := pf.Acquire(); lerr != nil {
			return false, ErrHealthy // vivo Y lock tomado: padre al pie
		}
		_ = pf.Release() // PIDAlive dice vivo pero el lock esta libre:
		// pid muerto con handle ajeno (doble de host); seguimos abajo.
	}
	// 2) Lock libre: holder muerto o sin fichero. Si esta tomado, hay
	// padre al pie.
	err := pf.Acquire()
	switch {
	case errors.Is(err, ErrLocked):
		return false, ErrHealthy
	case err != nil:
		return false, err
	}
	// El lock se suelta ANTES de spawnear: si el guard lo sostuviera,
	// el padre recien nacido lo encontraria tomado y moriría de ErrDouble.
	_ = pf.Release()
	spec := PipeSpec{ParentPID: os.Getpid()}
	cmd, serr := cfg.Spawn(spec)
	if serr != nil {
		return false, serr
	}
	cfg.Log.Info("watchdog guard: padre relanzado", "pid_hijo_guard", cmd.Process.Pid, "pidfile", cfg.PidfilePath)
	return true, nil
}

// CronRunner ejecuta crontab sin depender de shell: args vacios y
// stdin no vacio → `crontab -` (reinstala desde stdin). En tests se
// inyecta un doble en memoria.
type CronRunner interface {
	List() (string, error)
	Write(content string) error
	Remove() error
}

// OptoutCron borra la linea del guard sin root (T-0503 AC1): lee el
// crontab del usuario, quita la linea marcada y reinstala lo que
// queda; si no queda nada, `crontab -r`. Devuelve las lineas
// restantes (0 = la puerta del criterio `crontab -l | wc -l` = 0).
func OptoutCron(c CronRunner) (int, error) {
	content, err := c.List()
	if err != nil && content == "" {
		return 0, nil // sin crontab: ya optado
	}
	if err != nil {
		return -1, err
	}
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, CronMarker) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		if err := c.Remove(); err != nil && !errors.Is(err, exec.ErrNotFound) {
			return -1, err
		}
		return 0, nil
	}
	if err := c.Write(strings.Join(kept, "\n") + "\n"); err != nil {
		return -1, err
	}
	return len(kept), nil
}

// InstallCron deja la linea del guard (idempotente: si ya existe, no
// duplica). Es best-effort: sin `crontab` en el PATH devuelve error y
// el runbook manda la linea manual.
func InstallCron(c CronRunner, binPath string) (int, error) {
	content, err := c.List()
	if err != nil && content == "" && !errors.Is(err, exec.ErrNotFound) {
		content = ""
	}
	found := false
	var kept []string
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, CronMarker) {
			found = true
			continue
		}
		kept = append(kept, line)
	}
	if found {
		return len(kept) + 1, nil
	}
	out := append(kept, CronLine(binPath))
	if err := c.Write(strings.Join(out, "\n") + "\n"); err != nil {
		return -1, err
	}
	return len(out), nil
}

// SystemCron es el CronRunner real (crontab del usuario admin, sin
// root: BRIEF-BASE restriccion 2).
type SystemCron struct{}

func (SystemCron) List() (string, error) {
	out, err := exec.Command("crontab", "-l").Output()
	return string(out), err
}

func (SystemCron) Write(content string) error {
	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func (SystemCron) Remove() error {
	return exec.Command("crontab", "-r").Run()
}

// PersistMode es la detencion de la cadena de arranque T-0504:
// systemd (INVOCATION_ID lo delata — lo inyecta systemd en cada
// unidad—), rc.local (nuestro snippet exporta BOTNET_PERSIST=rc) o
// cron @reboot (lo demas). El modo se loguea como persist=<modo>.
func PersistMode(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	if getenv("INVOCATION_ID") != "" {
		return "systemd"
	}
	if getenv("BOTNET_PERSIST") == "rc" {
		return "rc"
	}
	return "reboot"
}

// RunGuardLoop es el doble de `while :; do botd --guard; sleep 60;
// done` para hosts sin crond (y el reloj del soak T-0506): cada tick
// hace el un-shot del guard. Con crontab real la linea de T-0503 ya
// cubre este bucle; aqui vive el soak de duenos que matan procesos.
func RunGuardLoop(ctx context.Context, tick time.Duration, cfg GuardConfig) error {
	if tick <= 0 {
		tick = time.Minute
	}
	for {
		_, err := RunGuardOnce(cfg)
		if err != nil && !errors.Is(err, ErrHealthy) {
			if cfg.Log != nil {
				cfg.Log.Warn("guard", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tick):
		}
	}
}
