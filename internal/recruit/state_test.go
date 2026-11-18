package recruit

import (
	"testing"
	"time"
)

// TestStateTable: los 14 estados existen y los terminales son los cuatro
// del blueprint.
func TestStateTable(t *testing.T) {
	if got := len(States()); got != 14 {
		t.Fatalf("estados=%d (quiero 14)", got)
	}
	if got := len(Terminales()); got != 4 {
		t.Fatalf("terminales=%d (quiero 4)", got)
	}
	for _, s := range States() {
		if s == StateVerified && Terminales()[s] {
			t.Fatal("verified no es terminal: puede volver por rollback")
		}
	}
}

// TestTransition cubre los ocho caminos del embudo y los rechazos.
func TestTransition(t *testing.T) {
	casos := []struct {
		name string
		from State
		to   State
		ok   bool
	}{
		{"new a eligible", StateNew, StateEligible, true},
		{"eligible a sweeping", StateEligible, StateSweeping, true},
		{"sweeping a bannered", StateSweeping, StateBannered, true},
		{"bannered a assessed", StateBannered, StateAssessed, true},
		{"assessed a staging", StateAssessed, StateStaging, true},
		{"staging a enrolling", StateStaging, StateEnrolling, true},
		{"enrolling a enrolled", StateEnrolling, StateEnrolled, true},
		{"enrolled a verified", StateEnrolled, StateVerified, true},
	}
	for _, c := range casos {
		c := c
		t.Run(c.name, func(t *testing.T) {
			if err := Transition(c.from, c.to); err != nil {
				t.Fatalf("%s -> %s: %v", c.from, c.to, err)
			}
		})
	}

	t.Run("salto invalido", func(t *testing.T) {
		if err := Transition(StateNew, StateVerified); err == nil {
			t.Fatal("new -> verified debe fallar")
		}
	})
	t.Run("mismo estado", func(t *testing.T) {
		if err := Transition(StateEligible, StateEligible); err == nil {
			t.Fatal("eligible -> eligible debe fallar")
		}
	})
	t.Run("terminal no sale", func(t *testing.T) {
		if err := Transition(StateBurned, StateEligible); err == nil {
			t.Fatal("burned -> eligible debe fallar")
		}
	})
	t.Run("deferred es reintento", func(t *testing.T) {
		if err := Transition(StateEnrolling, StateDeferred); err != nil {
			t.Fatalf("enrolling -> deferred: %v", err)
		}
	})
}

// TestReasonRequired: los terminales y deferred exigen motivo escrito.
func TestReasonRequired(t *testing.T) {
	for s := range Terminales() {
		if !ReasonRequired(s) {
			t.Errorf("%s debe exigir motivo", s)
		}
	}
	if !ReasonRequired(StateDeferred) {
		t.Error("deferred debe exigir motivo")
	}
	if ReasonRequired(StateEligible) {
		t.Error("eligible no exige motivo")
	}
}

// TestBackoff: 2^n horas, acotado a 8 intentos.
func TestBackoff(t *testing.T) {
	if d := Backoff(1); d != 2*time.Hour {
		t.Fatalf("Backoff(1)=%v (quiero 2h)", d)
	}
	if d := Backoff(3); d != 8*time.Hour {
		t.Fatalf("Backoff(3)=%v (quiero 8h)", d)
	}
	if d := Backoff(0); d != 2*time.Hour {
		t.Fatalf("Backoff(0)=%v (quiero 2h)", d)
	}
	if d := Backoff(99); d != 256*time.Hour {
		t.Fatalf("Backoff(99)=%v (quiero 256h)", d)
	}
}

// TestAgeClock: `skipped` no mueve el reloj de edad, `ok` si.
func TestAgeClock(t *testing.T) {
	if AdvanceAge("skipped") {
		t.Error("skipped no debe mover el reloj")
	}
	if AdvanceAge("no_budget") {
		t.Error("no_budget no debe mover el reloj")
	}
	if !AdvanceAge("ok") {
		t.Error("ok debe mover el reloj")
	}
	now := time.Now()
	if ExitRule(now, now.Add(-3*24*time.Hour), 14*24*time.Hour) {
		t.Error("3 dias no debe morir con tope de 14")
	}
	if !ExitRule(now, now.Add(-15*24*time.Hour), 14*24*time.Hour) {
		t.Error("15 dias debe morir con tope de 14")
	}
}

// TestLeaseDurationByPhase: cada fase tiene su lease y no hay uno unico.
func TestLeaseDurationByPhase(t *testing.T) {
	got := map[string]time.Duration{
		"sweep":   LeaseDurationByPhase("sweep"),
		"cred":    LeaseDurationByPhase("cred"),
		"deliver": LeaseDurationByPhase("deliver"),
	}
	if got["sweep"] != 30*time.Second {
		t.Errorf("sweep=%v (quiero 30s)", got["sweep"])
	}
	if got["cred"] != 300*time.Second {
		t.Errorf("cred=%v (quiero 300s)", got["cred"])
	}
	if got["deliver"] != 900*time.Second {
		t.Errorf("deliver=%v (quiero 900s)", got["deliver"])
	}
	t.Logf("sweep:30s")
	t.Logf("cred:300s")
	t.Logf("deliver:900s")
}

// TestTransitionNewEligible: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionNewEligible(t *testing.T) {
	if err := Transition(StateNew, StateEligible); err != nil {
		t.Fatalf("New -> Eligible: %v", err)
	}
}

// TestTransitionEligibleSweeping: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionEligibleSweeping(t *testing.T) {
	if err := Transition(StateEligible, StateSweeping); err != nil {
		t.Fatalf("Eligible -> Sweeping: %v", err)
	}
}

// TestTransitionSweepingBannered: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionSweepingBannered(t *testing.T) {
	if err := Transition(StateSweeping, StateBannered); err != nil {
		t.Fatalf("Sweeping -> Bannered: %v", err)
	}
}

// TestTransitionBanneredAssessed: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionBanneredAssessed(t *testing.T) {
	if err := Transition(StateBannered, StateAssessed); err != nil {
		t.Fatalf("Bannered -> Assessed: %v", err)
	}
}

// TestTransitionAssessedStaging: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionAssessedStaging(t *testing.T) {
	if err := Transition(StateAssessed, StateStaging); err != nil {
		t.Fatalf("Assessed -> Staging: %v", err)
	}
}

// TestTransitionStagingEnrolling: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionStagingEnrolling(t *testing.T) {
	if err := Transition(StateStaging, StateEnrolling); err != nil {
		t.Fatalf("Staging -> Enrolling: %v", err)
	}
}

// TestTransitionEnrollingEnrolled: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionEnrollingEnrolled(t *testing.T) {
	if err := Transition(StateEnrolling, StateEnrolled); err != nil {
		t.Fatalf("Enrolling -> Enrolled: %v", err)
	}
}

// TestTransitionEnrolledVerified: camino valido del embudo (nivel superior para el
// recuento del gate).
func TestTransitionEnrolledVerified(t *testing.T) {
	if err := Transition(StateEnrolled, StateVerified); err != nil {
		t.Fatalf("Enrolled -> Verified: %v", err)
	}
}
