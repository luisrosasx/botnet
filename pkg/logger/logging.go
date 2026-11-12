package logger

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// LogLine es el registro JSON del spool (T-1201 AC3): una linea = un
// objeto JSON completo. Prohibido el multilinea crudo: journalctl -o
// json y jq por linea tienen que funcionar en el 100 % de las lineas,
// asi que el mensaje escapa por encoding/json (los \n del payload nunca
// salen crudos al fichero).
type LogLine struct {
	TS        time.Time      `json:"ts"`
	Level     string         `json:"level"`
	Msg       string         `json:"msg"`
	SessionID string         `json:"session_id,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// NewLine construye un registro con el timestamp actual.
func NewLine(level, msg, sessionID string) LogLine {
	return LogLine{TS: time.Now().UTC(), Level: level, Msg: msg, SessionID: sessionID}
}

// MarshalJSON deja el JSON en UNA linea: el contrato del spool.
func (l LogLine) MarshalJSONStr() (string, error) {
	b, err := json.Marshal(l)
	return string(b), err
}

// WriteJSON escribe el registro como una sola linea terminada en \n.
func WriteJSON(w io.Writer, l LogLine) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Spool es el log JSON con respaldo a disco (BLUEPRINT §12 "logs JSON
// con spool"): escribe en el fichero del spool y refleja en w (stderr)
// para el scrape del orquestador. Rotacion por tamano — el nodo de 8 MB
// de flash no admite un log sin techo; al rotar se conserva una unica
// copia .1. Con dir vacio, el spool es solo el writer (modo test).
type Spool struct {
	w        io.Writer
	dir      string
	maxBytes int64
	path     string
	f        *os.File
	written  int64
	lines0   int64
}

// NewSpool prepara el spool; maxBytes<=0 toma 1 MiB por fichero.
func NewSpool(dir string, w io.Writer, maxBytes int64) *Spool {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	if w == nil {
		w = io.Discard
	}
	return &Spool{w: w, dir: dir, maxBytes: maxBytes}
}

// WriteLine añade el registro al spool (una linea JSON) y al writer
// espejo. El primer Write abre (y crea el dir) del spool perezosamente.
func (s *Spool) WriteLine(l LogLine) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if s.dir != "" {
		if err := s.ensure(); err != nil {
			return err
		}
		if s.written+int64(len(b)) > s.maxBytes {
			if err := s.rotate(); err != nil {
				return err
			}
		}
		if _, err := s.f.Write(b); err != nil {
			return err
		}
		s.written += int64(len(b))
	}
	if _, err := s.w.Write(b); err != nil {
		return err
	}
	s.lines0++
	return nil
}

// Lines son las lineas servidas (asserts de test).
func (s *Spool) Lines() int64 { return s.lines0 }

// Path es la ruta del fichero activo del spool ("" si sin dir).
func (s *Spool) Path() string { return s.path }

func (s *Spool) ensure() error {
	if s.f != nil {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return err
	}
	s.path = filepath.Join(s.dir, "botnet-spool.log")
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	s.f = f
	s.written = statSize(f)
	return err
}

func (s *Spool) rotate() error {
	if err := s.f.Close(); err != nil {
		return err
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	s.f = f
	s.written = 0
	return err
}

// Close cierra el fichero del spool (el espejo w no se cierra: es la
// salida del proceso).
func (s *Spool) Close() error {
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

func statSize(f *os.File) int64 {
	if fi, err := f.Stat(); err == nil {
		return fi.Size()
	}
	return 0
}
