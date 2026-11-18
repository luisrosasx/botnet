// Package recruit: maquina de estados del candidato y su auditoria.
//
// El estado vive en Postgres (recruit_candidates.state) y cada transicion
// escribe una fila en recruit_candidate_events con actor y motivo. Nada
// de memoria: el proceso puede morir en cualquier punto sin perder estado
// (docs/recruit/BLUEPRINT.md §5).
package recruit

import (
	"context"
	"fmt"
	"math"
	"time"
)

// State es uno de los 14 estados del blueprint §5.
type State string

const (
	StateNew        State = "new"
	StateExcluded   State = "excluded"
	StateEligible   State = "eligible"
	StateSweeping   State = "sweeping"
	StateBannered   State = "bannered"
	StateAssessed   State = "assessed"
	StateStaging    State = "staging"
	StateEnrolling  State = "enrolling"
	StateEnrolled   State = "enrolled"
	StateVerified   State = "verified"
	StateDeferred   State = "deferred"
	StateDiscarded  State = "discarded"
	StateBurned     State = "burned"
	StateRolledBack State = "rolled_back"
)

// States lista los 14 estados en el orden del embudo.
func States() []State {
	return []State{StateNew, StateExcluded, StateEligible, StateSweeping,
		StateBannered, StateAssessed, StateStaging, StateEnrolling,
		StateEnrolled, StateVerified, StateDeferred, StateDiscarded,
		StateBurned, StateRolledBack}
}

// Terminales son los estados en los que el candidato deja de trabajar.
func Terminales() map[State]bool {
	return map[State]bool{StateExcluded: true, StateDiscarded: true,
		StateBurned: true, StateRolledBack: true}
}

// transitions son los caminos permitidos del blueprint §5. `verified` es
// el unico exito; `deferred` es reintento, no muerte.
var transitions = map[State][]State{
	StateNew:        {StateExcluded, StateEligible, StateDiscarded},
	StateEligible:   {StateExcluded, StateSweeping, StateDeferred, StateDiscarded},
	StateSweeping:   {StateBannered, StateDeferred, StateDiscarded, StateBurned},
	StateBannered:   {StateAssessed, StateDeferred, StateDiscarded},
	StateAssessed:   {StateStaging, StateExcluded, StateDeferred, StateDiscarded, StateBurned},
	StateStaging:    {StateEnrolling, StateDeferred, StateDiscarded},
	StateEnrolling:  {StateEnrolled, StateDeferred, StateDiscarded, StateBurned},
	StateEnrolled:   {StateVerified, StateDeferred, StateDiscarded, StateBurned, StateRolledBack},
	StateDeferred:   {StateSweeping, StateExcluded, StateDiscarded, StateBurned, StateRolledBack},
	StateVerified:   {StateRolledBack, StateBurned},
	StateExcluded:   {},
	StateDiscarded:  {},
	StateBurned:     {},
	StateRolledBack: {},
}

// Transition valida el paso. Un camino no declarado es error, no aviso:
// el embudo no se improvisa.
func Transition(from, to State) error {
	if from == to {
		return fmt.Errorf("recruit: transicion a si mismo (%s)", from)
	}
	for _, ok := range transitions[from] {
		if ok == to {
			return nil
		}
	}
	return fmt.Errorf("recruit: transicion no permitida %s -> %s", from, to)
}

// ReasonRequired dice si el estado destino exige motivo escrito: los
// cuatro terminales y `deferred` (siempre hay una causa).
func ReasonRequired(to State) bool {
	return Terminales()[to] || to == StateDeferred
}

// Backoff devuelve el respaldo exponencial de un reintento: 2^n horas con
// n = intentos de etapa (1 -> 2 h, 3 -> 8 h). Techo de 8 intentos.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		attempts = 8
	}
	return time.Duration(math.Pow(2, float64(attempts))) * time.Hour
}

// ExitRule decide la muerte por edad. El reloj de edad (age_clock_at) solo
// avanza con un ciclo que trabajo de verdad: `skipped` no lo mueve, asi
// que quedarse sin presupuesto no envejece al candidato (injerto V2).
func ExitRule(now, ageClock time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 {
		maxAge = 14 * 24 * time.Hour
	}
	return now.Sub(ageClock) > maxAge
}

// AdvanceAge decide si el ciclo mueve el reloj de edad.
func AdvanceAge(outcome string) bool {
	switch outcome {
	case "ok", "fail", "timeout":
		return true
	default: // skipped, vacio, no_budget
		return false
	}
}

// NextRateClass decide la clase de ritmo del tick. Por ahora respeta la
// politica horaria y la pausa manual; el lazo de histeresis completo llega
// en T-1607 y se apoya en esta misma funcion.
func NextRateClass(ctx context.Context, st *Store) (int, error) {
	class, manual, paused, err := st.ControlState(ctx)
	if err != nil {
		return 0, err
	}
	if paused {
		return 0, nil
	}
	if manual >= 0 {
		return manual, nil
	}
	hour, err := st.WindowRateClass(ctx)
	if err != nil {
		return class, err
	}
	return hour, nil
}

// SiguienteEstado es el avance del embudo cuando el ciclo salio bien.
func SiguienteEstado(actual State) (State, bool) {
	switch actual {
	case StateSweeping:
		return StateBannered, true
	case StateBannered:
		return StateAssessed, true
	case StateAssessed:
		return StateStaging, true
	case StateStaging:
		return StateEnrolling, true
	case StateEnrolling:
		return StateEnrolled, true
	}
	return "", false
}
