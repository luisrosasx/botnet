package agent

import (
	"strings"
	"sync"
)

// CmdQueue encola los pending_cmds que dicta el backend (T-0409 AC2):
// el agente los encola en su canal local "ctl.{node_id}.cmd" y los ack
// en el heartbeat siguiente. El dedup es por id: el mismo id en dos
// respuestas seguidas se encola una sola vez.
//
// Formato del cmd: "kind:id" (p.ej. "update:v1.2.3-ab12"); "kind" a
// secas es valido con id vacio (se encola por kind, sin dedup).
type CmdQueue struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	kinds map[string]string
	order []string
}

func NewCmdQueue() *CmdQueue {
	return &CmdQueue{
		seen:  map[string]struct{}{},
		kinds: map[string]string{},
	}
}

// parseCmd separa kind e id.
func parseCmd(s string) (kind, id string) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// Enqueue encola los cmd nuevos (dedup por id) y devuelve los ids
// encajados — esos son los que se ack en el siguiente heartbeat. Un id
// repetido o un kind ya encolado sin id no se reencola.
func (q *CmdQueue) Enqueue(cmds []string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	var accepted []string
	for _, raw := range cmds {
		kind, id := parseCmd(strings.TrimSpace(raw))
		if kind == "" {
			continue
		}
		key := id
		if key == "" {
			key = "kind:" + kind
		}
		if _, dup := q.seen[key]; dup {
			continue
		}
		q.seen[key] = struct{}{}
		q.kinds[key] = kind
		q.order = append(q.order, key)
		if id != "" {
			accepted = append(accepted, id)
		}
	}
	return accepted
}

// Pop saca el proximo cmd encolado (el router local del plano de datos
// lo consume; tras el flag bye el router puede apagarse — la baja no
// depende de un POST saliente). kind/id vacios si la cola esta vacia.
func (q *CmdQueue) Pop() (kind, key string, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.order) == 0 {
		return "", "", false
	}
	key = q.order[0]
	q.order = q.order[1:]
	return q.kinds[key], key, true
}

// Len: streams de cmds en cola.
func (q *CmdQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.order)
}

// CTLCtlQueueName es el nombre logico del canal por nodo (NATS en el
// backend): ctl.{node_id}.cmd.
func CTLCtlQueueName(nodeID string) string { return "ctl." + nodeID + ".cmd" }
