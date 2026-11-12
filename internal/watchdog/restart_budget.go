// Package watchdog es el presupuesto de reinicios del agente (T-1301,
// A1-03): el auto-kill por heap revive al hijo, pero con presion cronica
// el revive deja de ser un ciclo visible — la tasa cae de 3/h a 1/h con
// intervalos monotonos y 0 rafagas de DNS+handshake desde la misma IP.
package watchdog

import (
	"sort"
	"time"
)

// RestartBudget modela la politica del guard: los auto-kills dentro de una
// ventana de 1 h tienen presupuesto de 3; agotarlo escala el presupuesto a
// 1/h para siempre (monotonia asertada en log) y el siguiente revive se
// retrasa hasta que la ventana gira.
type RestartBudget struct {
	Window        time.Duration // ventana deslizante (1 h)
	Rate          int           // presupuesto actual por ventana: 3 -> 1
	FastRevival   time.Duration // revive rapido mientras hay presupuesto (10 s)
	killThreshold int           // Rate original para la escalada (3)

	escalated bool
	kills     []time.Time
}

// NewRestartBudget fija la politica canonica: 3/h -> 1/h, revive rapido
// 10 s, ventana de 1 h.
func NewRestartBudget() *RestartBudget {
	return &RestartBudget{
		Window:        time.Hour,
		Rate:          3,
		FastRevival:   10 * time.Second,
		killThreshold: 3,
	}
}

// Escalated dice si el presupuesto ya cayo a 1/h (nunca vuelve a subir).
func (b *RestartBudget) Escalated() bool { return b.escalated }

// Allowed es el presupuesto actual de auto-kills por ventana.
func (b *RestartBudget) Allowed() int { return b.Rate }

// killsInWindow son los kills en (t-Window, t] (frontera izquierda abierta).
func (b *RestartBudget) killsInWindow(t time.Time) []time.Time {
	sort.Slice(b.kills, func(i, j int) bool { return b.kills[i].Before(b.kills[j]) })
	var out []time.Time
	for _, k := range b.kills {
		if k.After(t.Add(-b.Window)) && !k.After(t) {
			out = append(out, k)
		}
	}
	return out
}

// RecordKill registra un auto-kill en t y devuelve el intervalo de revive
// que el watchdog debe respetar hasta el proximo arranque del hijo. Con
// presupuesto restante es FastRevival; agotado (p. ej. 4 kills en 1 h con
// 3/h), escala a 1/h y retrasa el revive hasta que la ventana gira — los
// intervalos resultantes son monotonos no decrecientes.
func (b *RestartBudget) RecordKill(t time.Time) time.Duration {
	b.kills = append(b.kills, t)
	win := b.killsInWindow(t)
	n := len(win)
	if n <= b.Rate {
		return b.FastRevival
	}
	// Presupuesto agotado: unica escalada 3/h -> 1/h (monotona).
	if !b.escalated && b.Rate > 1 {
		b.escalated = true
		b.Rate = 1
		n = len(win)
	}
	// Revive en r tal que en (r-Window, r] queden como mucho Rate-1 kills
	// viejos (el revive producira el kill nuevo): r = L[n-Rate] + Window.
	oldest := win[n-b.Rate]
	r := oldest.Add(b.Window)
	delay := r.Sub(t)
	if delay < b.FastRevival {
		delay = b.FastRevival
	}
	return delay
}
