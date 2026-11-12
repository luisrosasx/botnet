package accounting

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/botnet/botnet/internal/proxy"
)

// Period de flush del planificador: 15 min. El jitter lo descentra de
// la hora fija para que la flota entera no publique picos rítmicos
// (A1-05).
const FlushPeriod = 15 * time.Minute

// FlushJitterMax: corrimiento maximo del flush periodico respecto del
// borde de 15 min. Con 60 s el plan NUNCA cae clavado en :00/:15/:30/:45.
const FlushJitterMax = 60 * time.Second

// CloseCoalesceWindow: los cierres de sesion dentro de una ventana de
// spool (60 s) se fusionan en un solo flush: el techo diario por nodo
// queda bajo 1.440 (criterio T-1002) sin perder el flush al cerrar.
const CloseCoalesceWindow = 60 * time.Second

// FlushEnvelope es el payload de `acct.{node_id}.flush` (stream
// ACCT_FLUSH durable del backend): el flush_id hace idempotente el
// reenvio del consumer redelivered (202 {dedup:true}).
type FlushEnvelope struct {
	FlushID  string               `json:"flush_id"`
	NodeID   string               `json:"node_id"`
	At       time.Time            `json:"at"`
	Sessions []proxy.SessionUsage `json:"sessions"`
}

// FlushIDEmitido forma el identificador durable que emite el nodo: el
// backend lo usa como clave de dedup (reenvio = misma clave).
func FlushIDEmitido(nodeID string, seq uint64) string {
	return fmt.Sprintf("%s-%08d", nodeID, seq)
}

// EncodeEnvelope serializa el flush para NATS/HTTP.
func EncodeEnvelope(env FlushEnvelope) ([]byte, error) { return json.Marshal(env) }

// DecodeEnvelope parsea el payload del durable; exige flush_id y
// node_id no vacios (un flush sin clave no es deduplicable).
func DecodeEnvelope(b []byte) (FlushEnvelope, error) {
	var env FlushEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return env, err
	}
	if env.FlushID == "" || env.NodeID == "" {
		return env, fmt.Errorf("spool: envelope sin flush_id/node_id")
	}
	return env, nil
}

// FlushTotalBytes suma la factura de un flush: invariante bajo dedup
// (un reenvio no mueve la suma).
func FlushTotalBytes(env FlushEnvelope) int64 {
	var n int64
	for _, s := range env.Sessions {
		n += int64(s.BytesIn) + int64(s.BytesOut)
	}
	return n
}

// DedupStore guarda los flush_id vistos. MarkSeen devuelve true la
// primera vez (aplicar) y false en el reenvio (dedup).
type DedupStore interface {
	MarkSeen(flushID string) (bool, error)
}

// MemDedup es el store in-process (tests y dev).
type MemDedup struct {
	seen map[string]bool
}

// NewMemDedup crea el store vacio.
func NewMemDedup() *MemDedup { return &MemDedup{seen: make(map[string]bool)} }

// MarkSeen implementa DedupStore.
func (m *MemDedup) MarkSeen(flushID string) (bool, error) {
	if m.seen[flushID] {
		return false, nil
	}
	m.seen[flushID] = true
	return true, nil
}

// FlushSink vierte un flush ya dedup: usage diario + sessions.bytes_*.
// El backend lo implementa sobre repo; los tests, sobre un acumulador.
type FlushSink interface {
	ApplyFlush(env FlushEnvelope) error
}

// ApplyFlush es el unico camino contable: dedup primero; si es
// reenvio, el sink NO se invoca y la suma de usage es invariante.
func ApplyFlush(env FlushEnvelope, sink FlushSink, dedup DedupStore) (deduped bool, err error) {
	ok, err := dedup.MarkSeen(env.FlushID)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	return false, sink.ApplyFlush(env)
}

// DiskSpool es el spool a disco del nodo (cada SpoolInterval): el
// ultimo pending sobrevive al kill -9 y se recupera al arrancar.
type DiskSpool struct {
	Dir string
}

func (s DiskSpool) path(nodeID string) string {
	return filepath.Join(s.Dir, "spool-"+nodeID+".json")
}

// Write vuelca el pending atomica mente (tmp + rename): o vive el
// spool anterior o el nuevo, nunca un truncado.
func (s DiskSpool) Write(nodeID string, env FlushEnvelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	tmp := s.path(nodeID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(nodeID))
}

// Read recupera el ultimo spool; (zero, nil) si no hay ninguno.
func (s DiskSpool) Read(nodeID string) (FlushEnvelope, error) {
	b, err := os.ReadFile(s.path(nodeID))
	if os.IsNotExist(err) {
		return FlushEnvelope{}, nil
	}
	if err != nil {
		return FlushEnvelope{}, err
	}
	return DecodeEnvelope(b)
}

// FlushPlan simula 24 h de disparadores con reloj controlado y
// devuelve los instantes de flush efectivos (criterio 1 T-1002).
type FlushPlan struct {
	NodeID    string
	Start     time.Time
	Hours     int
	Closes    []time.Time // cierres de sesion del churn simulado
	Seed      int64
	JitterMax time.Duration
}

// Simulate: periodicos a 15 min ±jitter con semilla determinista +
// cierres coalescados por ventana de spool. Devuelve los flushes
// ordenados; el test aserta ≤ 1.440 por nodo y 0 brotes a hora fija.
func (p FlushPlan) Simulate() []time.Time {
	jit := p.JitterMax
	if jit <= 0 {
		jit = FlushJitterMax
	}
	rng := rand.New(rand.NewSource(p.Seed))
	var ts []time.Time
	// Periodicos con jitter: NUNCA clavados en el borde de 15 min.
	for k := 1; k*int(FlushPeriod/time.Minute) <= p.Hours*60; k++ {
		base := p.Start.Add(time.Duration(k) * FlushPeriod)
		off := time.Duration(rng.Int63n(int64(jit))) + time.Second
		ts = append(ts, base.Add(off))
	}
	// Cierres de sesion: flush inmediato, coalescado con cualquier
	// flush ya planificado dentro de la ventana de spool.
	for _, c := range p.Closes {
		merged := false
		for _, t := range ts {
			d := c.Sub(t)
			if d < 0 {
				d = -d
			}
			if d <= CloseCoalesceWindow {
				merged = true
				break
			}
		}
		if !merged {
			ts = append(ts, c)
		}
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	return ts
}
