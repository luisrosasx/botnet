// Package state: aquí vivirá el kv plano /data/botd 0600 de BLUEPRINT §3;
// por ahora, la máquina de estados del ciclo de vida del agente (§4):
// enroll → agent → bye, sin atajos.
package state

import (
	"errors"
	"fmt"
)

type State uint8

const (
	Enroll State = iota // sin node.json: POST /v1/enroll en curso
	Agent               // enrolado: heartbeat, túnel H2, SOCKS5 local
	Bye                 // baja por pull (flag bye): drenar, flush y borrar claves; terminal
)

func (s State) String() string {
	switch s {
	case Enroll:
		return "enroll"
	case Agent:
		return "agent"
	case Bye:
		return "bye"
	}
	return fmt.Sprintf("state(%d)", uint8(s))
}

var ErrTransition = errors.New("state: invalid transition")

// Allowed: las únicas aristas legales. Volver de bye a agent (o a enroll)
// está prohibido: la baja revoca cert y node_secret y el proceso debe
// re-enrolarse desde cero.
func Allowed(from, to State) bool {
	switch {
	case from == Enroll && to == Agent:
		return true
	case from == Agent && to == Bye:
		return true
	default:
		return false
	}
}

// Machine arranca en Enroll y rechaza toda transición no listada.
type Machine struct{ s State }

func New() *Machine { return &Machine{s: Enroll} }

func (m *Machine) State() State { return m.s }

func (m *Machine) Transition(to State) error {
	if !Allowed(m.s, to) {
		return fmt.Errorf("%s -> %s: %w", m.s, to, ErrTransition)
	}
	m.s = to
	return nil
}
