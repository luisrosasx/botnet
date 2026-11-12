package state

import (
	"errors"
	"testing"
)

func TestValidLifecycle(t *testing.T) {
	m := New()
	if m.State() != Enroll {
		t.Fatalf("recien creado: state %s != enroll", m.State())
	}
	for _, to := range []State{Agent, Bye} {
		if err := m.Transition(to); err != nil {
			t.Fatalf("transicion a %s: %v", to, err)
		}
	}
	if m.State() != Bye {
		t.Fatalf("fin del ciclo: state %s != bye", m.State())
	}
}

func TestInvalidTransitionsRejected(t *testing.T) {
	cases := []struct {
		name string
		from State
		to   State
	}{
		{"enroll->bye", Enroll, Bye},
		{"enroll->enroll", Enroll, Enroll},
		{"agent->enroll", Agent, Enroll},
		{"agent->agent", Agent, Agent},
		{"bye->agent", Bye, Agent},
		{"bye->bye", Bye, Bye},
		{"bye->desconocido", Bye, State(9)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Machine{s: c.from}
			if err := m.Transition(c.to); !errors.Is(err, ErrTransition) {
				t.Fatalf("%s: expected ErrTransition, got %v", c.name, err)
			}
			if m.State() != c.from {
				t.Fatalf("%s: estado mutado a %s", c.name, m.State())
			}
		})
	}
}

func TestStateNames(t *testing.T) {
	for s, want := range map[State]string{Enroll: "enroll", Agent: "agent", Bye: "bye"} {
		if got := s.String(); got != want {
			t.Fatalf("String(): %q != %q", got, want)
		}
	}
}
