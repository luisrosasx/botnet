package watchdog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"
)

// ChildConfig configura el rol hijo: corre el agente (Work) y vigila
// al padre por el pipe. Si el padre muere, este mismo proceso se
// re-elige padre (T-0502 AC1, log role=elected) y relanza un hijo —
// matar al padre es irrelevante.
type ChildConfig struct {
	Work        func(context.Context) error // bucle del agente
	PidfilePath string                      // pidfile que se hereda al ser elegido
	Spawn       SpawnFunc                   // relanzador del hijo (DefaultSpawn por defecto)
	Silence     time.Duration
	Revive      time.Duration
	Log         *slog.Logger
	Budget      *RestartBudget
	Pipe        *ChildPipe // inyectable en tests; nil → DialChildPipe
}

func (c *ChildConfig) defaults() {
	if c.Log == nil {
		c.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.Spawn == nil {
		c.Spawn = DefaultSpawn(ChildFlag)
	}
}

// RunChild es el bucle del rol hijo. Termina cuando: (a) el agente
// termina (bye/stop → el padre lo relanza), o (b) el padre muere y la
// re-eleccion cede el testigo al bucle de padre dentro de este mismo
// proceso.
func RunChild(ctx context.Context, cfg ChildConfig) error {
	cfg.defaults()
	pipe := cfg.Pipe
	if pipe == nil {
		var err error
		pipe, err = DialChildPipe()
		if err != nil {
			// El padre murio entre el spawn y el dial: re-eleccion
			// directa; el agente aun no corre, el nuevo hijo lo hara.
			cfg.Log.Info("watchdog: pipe ausente, re-eleccion directa", "err", err)
			return promote(ctx, cfg)
		}
	}

	wctx, stop := context.WithCancel(ctx)
	defer stop()
	workDone := make(chan error, 1)
	if cfg.Work != nil {
		go func() { workDone <- cfg.Work(wctx) }()
	} else {
		close(workDone) // doble de test: hijo puro, solo el pipe importa
	}
	watchDone := make(chan error, 1)
	go func() { watchDone <- WatchParent(wctx, pipe, cfg.Log) }()

	select {
	case err := <-workDone:
		// bye/refresh/stop: el proceso hijo muere y el padre (si vive)
		// lo relanza. No hay re-eleccion por exit limpio.
		_ = pipe.Close()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	case werr := <-watchDone:
		if werr != nil && !errors.Is(werr, ErrParentLost) {
			// ctx global cancelado: el padre nos pidio parar.
			stop()
			_ = pipe.Close()
			return nil
		}
		// ErrParentLost: el pipe murio → re-eleccion. El agente propio
		// se detiene antes de relanzar: cero agentes dobles, la laguna
		// es ~1 s y la cota del criterio son 15 s.
		_ = pipe.Close()
		stop()
		return promote(ctx, cfg)
	case <-ctx.Done():
		stop()
		_ = pipe.Close()
		return nil
	}
}

// promote es la re-eleccion del T-0502: el hijo superviviente se
// declara padre (log role=elected), readquiere el flock del pidfile —
// el kernel lo solto al morir el anterior— y entra al bucle de padre
// relanzando un hijo. Si el cron guard se adelanto y ya hay padre con
// el lock tomado, este proceso simplemente se retira: la pareja nueva
// cubre el nodo y no quedan huespedes (pgrep -c botd sigue en 2).
func promote(ctx context.Context, cfg ChildConfig) error {
	cfg.Log.Info("watchdog role=elected", "pid", os.Getpid())
	pf := &Pidfile{Path: cfg.PidfilePath}
	if err := pf.Acquire(); err != nil {
		if errors.Is(err, ErrLocked) {
			cfg.Log.Info("watchdog: guard ya eligio padre, retiro", "pidfile", cfg.PidfilePath)
			return nil
		}
		return err
	}
	pc := ParentConfig{
		PidfilePath: cfg.PidfilePath,
		Spawn:       cfg.Spawn,
		Silence:     cfg.Silence,
		Revive:      cfg.Revive,
		Log:         cfg.Log,
		Budget:      cfg.Budget,
		Adopted:     pf,
	}
	// defaults() es obligatorio aqui: RunParent lo aplica y parentLoop
	// no; sin el, el re-elegido heredaria Silence=0 del ChildConfig, el
	// deadline del pipe venceria al nacer y el par entraria en bucle
	// spawn-kill de ~30 ms (duracion=0, delay=0 en el log).
	pc.defaults()
	return parentLoop(ctx, pc, pf)
}

// SpawnPromoted es el Spawn del hijo re-elegido: DefaultSpawn con el
// rol hijo — parentLoop ya le pasa el pipe nuevo por spec.
func SpawnPromoted() SpawnFunc { return DefaultSpawn(ChildFlag) }
