package proxy

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Contadores por stream (§7, A3-04): cada sesion es un stream con
// contadores propios de bytes_in/bytes_out. El SOCKS5 interno no traspasa
// auth entre sesiones: cerrar la sesion A no toca los contadores de B.
// El flush viaja por sesion con su session_id hacia el sujeto
// acct.{node_id}.flush; el backend lo vierte en sessions.bytes_in/out.

// FlushSubject forma el sujeto de contabilidad del nodo.
func FlushSubject(nodeID string) string { return "acct." + nodeID + ".flush" }

// SessionUsage es el registro de una sesion en el flush: los contadores
// viajan con su session_id (mezclar tenants en un byte es imposible por
// contrato).
type SessionUsage struct {
	SessionID string
	BytesIn   uint64
	BytesOut  uint64
}

// Counters son los contadores de una sesion; lock-free por direccion.
type Counters struct {
	In  atomic.Uint64
	Out atomic.Uint64
}

// CounterTable lleva los contadores vivos por session_id.
type CounterTable struct {
	mu sync.Mutex
	m  map[string]*Counters
}

// NewCounterTable crea la tabla vacia.
func NewCounterTable() *CounterTable {
	return &CounterTable{m: make(map[string]*Counters)}
}

// Get devuelve los contadores de la sesion, creandolos si no existen.
func (t *CounterTable) Get(sessionID string) *Counters {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.m[sessionID]
	if c == nil {
		c = &Counters{}
		t.m[sessionID] = c
	}
	return c
}

// AddIn registra bytes recibidos del cliente SOCKS (upstream).
func (t *CounterTable) AddIn(sessionID string, n uint64) {
	t.Get(sessionID).In.Add(n)
}

// AddOut registra bytes entregados al cliente SOCKS (downstream).
func (t *CounterTable) AddOut(sessionID string, n uint64) {
	t.Get(sessionID).Out.Add(n)
}

// Live informa si la sesion tiene contadores vivos (B sigue viva tras
// cerrar A).
func (t *CounterTable) Live(sessionID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.m[sessionID]
	return ok
}

// Flush toma el snapshot de todas las sesiones y libera las entradas:
// los contadores viajan al backend con el flush (acct.{node_id}.flush).
// Devuelve los registros ordenados por session_id para asserts
// deterministas.
func (t *CounterTable) Flush() []SessionUsage {
	t.mu.Lock()
	out := make([]SessionUsage, 0, len(t.m))
	for id, c := range t.m {
		out = append(out, SessionUsage{SessionID: id, BytesIn: c.In.Load(), BytesOut: c.Out.Load()})
		delete(t.m, id)
	}
	t.mu.Unlock()
	sortUsage(out)
	return out
}

// CloseSession libera los contadores de una sesion devolviendo su ultimo
// registro (el cierre de A no toca a B — T-0607 AC2/AC3).
func (t *CounterTable) CloseSession(sessionID string) SessionUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.m[sessionID]
	delete(t.m, sessionID)
	if c == nil {
		return SessionUsage{SessionID: sessionID}
	}
	return SessionUsage{SessionID: sessionID, BytesIn: c.In.Load(), BytesOut: c.Out.Load()}
}

// Count devuelve el numero de sesiones con contadores vivos.
func (t *CounterTable) Count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.m)
}

// WriteFlush emite el payload del flush (lineas session_id,bytes_in,bytes_out)
// — el doble del canal acct.{node_id}.flush en tests.
func WriteFlush(w io.Writer, nodeID string, usage []SessionUsage) error {
	if _, err := fmt.Fprintf(w, "# %s\n", FlushSubject(nodeID)); err != nil {
		return err
	}
	for _, u := range usage {
		if _, err := fmt.Fprintf(w, "%s,%d,%d\n", u.SessionID, u.BytesIn, u.BytesOut); err != nil {
			return err
		}
	}
	return nil
}

// sortUsage ordena por SessionID (insertion sort: n <= 150 por contrato
// de streams del nodo).
func sortUsage(us []SessionUsage) {
	for i := 1; i < len(us); i++ {
		for j := i; j > 0 && us[j].SessionID < us[j-1].SessionID; j-- {
			us[j], us[j-1] = us[j-1], us[j]
		}
	}
}
