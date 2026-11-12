// fuzz_test.go — T-1205: el framing propio del tunel aguanta 72 h de
// ruido sin caer (condicion R1 del veredicto). El job caro corre en CI
// con `make fuzz FUZZTIME=72h` (-fuzz=FuzzFraming -fuzztime=72h); el
// -runs=1000000 del criterio literal no es flag del toolchain fijado
// (go1.27.4), asi que wall-clock manda. El guardan barato es
// TestFuzzFramingSeed: reproduce el corpus versionado de
// testdata/corpus en CI barata (< 5 min).
package tunnel

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// decodeFuzzStream trata el input como un flujo de frames: los errores
// de wire (version, CRC, truncado, tamano) son CONTROL, no panico — el
// receptor los descarta y el fuzz solo prohibe caidas (AC1: 0 caidas,
// 0 panicos). El payload se reencuadra y debe volver byte a byte.
func decodeFuzzStream(data []byte) (frames int, err error) {
	defer func() {
		if r := recover(); r != nil {
			frames = 0
			err = errors.New("tunnel: panico en decode")
		}
	}()
	fr := NewFrameReader(bytes.NewReader(data), MaxFramePayload)
	for {
		h, payload, rerr := fr.ReadFrame()
		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				return frames, nil // stream truncado: limite natural
			}
			if errors.Is(rerr, ErrFrameVersion) || errors.Is(rerr, ErrFrameChecksum) ||
				errors.Is(rerr, ErrFrameTooLarge) || errors.Is(rerr, ErrFrameCorrupted) {
				return frames, nil // frame malo: descartado como en produccion
			}
			return frames, rerr
		}
		frames++
		if h.Len > 0 && len(payload) != int(h.Len) {
			return frames, errors.New("tunnel: payload no coincide con header")
		}
		// round-trip del frame bueno: re-WriteFrame debe reproducir el
		// tramo original (el codec no reordena ni inventa bytes).
		rt, rerr := WriteFrameTo(payload, h)
		if rerr != nil {
			return frames, rerr
		}
		var back frameRT
		if err := back.feed(rt); err != nil {
			return frames, err
		}
		if back.payload != string(payload) || back.stream != h.Stream || back.kind != h.Kind {
			return frames, errors.New("tunnel: round-trip de frame alterado")
		}
		if frames > len(data)/(frameHeaderLen)+2 {
			return frames, errors.New("tunnel: frames imposibles para el tamano de entrada")
		}
	}
}

// frameRT decodifica UN frame de prueba para el round-trip.
type frameRT struct {
	kind     Kind
	stream   uint32
	payload  string
	feededOK bool
}

func (f *frameRT) feed(b []byte) error {
	fr := NewFrameReader(bytes.NewReader(b), MaxFramePayload)
	h, p, err := fr.ReadFrame()
	if err != nil {
		return err
	}
	f.kind, f.stream, f.payload, f.feededOK = h.Kind, h.Stream, string(p), true
	return nil
}

// corpusPaths: los hex versionados del T-0401 + los que el fuzz de CI
// va dejando en testdata/corpus (AC1: corpus hallado queda versionado).
func corpusPaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", "*.hex"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("corpus en testdata/corpus: %v (%d)", err, len(paths))
	}
	return paths
}

func readHexFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("corpus %s: %v", path, err)
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("corpus %s no hex: %v", path, err)
	}
	return raw
}

// AC3: el corpus minimo reproduce en CI barata (< 5 min) lo que el
// fuzz de 72 h guarda. Ademas aserta AC2 en chico: goroutines finales
// <= baseline + 10 % tras 20k decodificaciones (el framing no fuga
// gorutinas ni buffers vivos).
func TestFuzzFramingSeed(t *testing.T) {
	base := runtime.NumGoroutine()
	start := time.Now()

	for _, path := range corpusPaths(t) {
		raw := readHexFile(t, path)
		if _, err := decodeFuzzStream(raw); err != nil {
			t.Fatalf("corpus %s cae el framing: %v", filepath.Base(path), err)
		}
	}

	// Estres determinista con las semillas canonicas del framing.
	seeds := [][]byte{
		{},                             // payload vacio: caso valido
		{FrameVersion},                 // cabecera truncada
		{0, 0, 0},                      // version 0 descartada
		bytes.Repeat([]byte{0xff}, 13), // cabecera incompleta ruidosa
	}
	for _, seed := range seeds {
		if _, err := decodeFuzzStream(seed); err != nil {
			t.Fatalf("semija %x cae el framing: %v", seed, err)
		}
	}

	frames := 0
	for i := 0; i < 20000; i++ {
		seed := seeds[i%len(seeds)]
		if n, err := decodeFuzzStream(seed); err != nil {
			t.Fatalf("iteracion %d: %v", i, err)
		} else {
			frames += n
		}
	}
	after := runtime.NumGoroutine()
	limit := base + base/10 + 2
	if after > limit {
		t.Fatalf("goroutines tras el estres: %d > baseline(%d)+10%%", after, base)
	}
	if time.Since(start) > 5*time.Minute {
		t.Fatalf("corpus seed: %v, want < 5 min", time.Since(start))
	}
}

// FuzzFraming: el target del job de 72 h. Seeds = corpus hex + casos
// minimos; el corpus que Go descubra queda versionado en
// testdata/corpus/fuzz_*.hex (procedimiento documentado en el Makefile).
func FuzzFraming(f *testing.F) {
	paths, gerr := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", "*.hex"))
	if gerr != nil {
		f.Fatalf("corpus: %v", gerr)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			f.Fatalf("corpus %s: %v", path, err)
		}
		raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
		if err != nil {
			f.Fatalf("corpus %s no hex: %v", path, err)
		}
		f.Add(raw)
	}
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := decodeFuzzStream(data); err != nil {
			t.Fatalf("framing cayo con %d bytes: %v", len(data), err)
		}
	})
}
