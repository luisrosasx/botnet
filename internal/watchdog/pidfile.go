// Package watchdog es el doble proceso del nodo (BLUEPRINT §4):
// `botd --parent` relanza `botd --child` por reexec del mismo binario
// (0 segundo binario en disco), pipe mutuo de vigilancia y pidfile
// protegido con flock. Muere el hijo → el padre lo relanza; muere el
// padre → el hijo se re-elige (election.go); el cron guard (guard.go)
// revive al padre si el pidfile no responde — cubre al dueño same-uid
// que puede matar procesos (A3-05)—.
package watchdog

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// PidfileName es el nombre canónico bajo node_dir: /data/botd/botd.pid.
const PidfileName = "botd.pid"

// ErrLocked: el flock del pidfile ya lo sostiene otro proceso (segundo
// arranque → exit ≠ 0, T-0501 AC3).
var ErrLocked = errors.New("watchdog: pidfile flock tomado")

// Pidfile es el pidfile con flock exclusivo no-bloqueante. El dueño lo
// sostiene mientras vive: el kernel suelta el lock al morir, de modo
// que la re-eleccion (T-0502) y el guard (T-0503) lo readquieren sin
// carrera. El contenido (pid + arranque) es informativo y sirve al
// guard para sondear procesos vivos.
type Pidfile struct {
	Path string

	f *os.File
}

// Acquire crea/adopta el pidfile, toma el flock y escribe el pid
// propio. Si el lock esta tomado devuelve ErrLocked sin tocar el
// fichero ajeno.
func (p *Pidfile) Acquire() error {
	if p.Path == "" {
		return errors.New("watchdog: pidfile sin ruta")
	}
	f, err := os.OpenFile(p.Path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	if err := flockExclusiveNB(f); err != nil {
		f.Close()
		if errors.Is(err, errWouldBlock) {
			return ErrLocked
		}
		return err
	}
	p.f = f
	return p.writePID()
}

// Release suelta el lock y cierra: el proximo Arranque lo readquiere.
func (p *Pidfile) Release() error {
	if p.f == nil {
		return nil
	}
	unlock(flockFD(p.f))
	err := p.f.Close()
	p.f = nil
	return err
}

// Held dice si este proceso sostiene el lock.
func (p *Pidfile) Held() bool { return p.f != nil }

func (p *Pidfile) writePID() error {
	if p.f == nil {
		return errors.New("watchdog: pidfile sin lock")
	}
	if _, err := p.f.WriteAt([]byte(""), 0); err != nil {
		return err
	}
	body := fmt.Sprintf("%d\n", os.Getpid())
	if err := p.f.Truncate(int64(len(body))); err != nil {
		return err
	}
	_, err := p.f.WriteAt([]byte(body), 0)
	return err
}

// ReadPID lee el pid declarado en la ruta dada (sin lock): lo usa el
// guard para preguntar "hay alguien vivo detras de este pidfile?".
func ReadPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("watchdog: pidfile %s sin pid: %q", path, b)
	}
	return n, nil
}

// ProbeAlive: el pidfile responde — hay pid y el proceso existe. Un
// fichero roto o con pid muerto cuenta como sin respuesta (el guard
// actua solo entonces, T-0503 AC3).
func ProbeAlive(path string) bool {
	pid, err := ReadPID(path)
	if err != nil {
		return false
	}
	return PIDAlive(pid)
}
