package loss

import "testing"

func TestForLossEscalonaPorUmbral(t *testing.T) {
	cases := []struct {
		bps  uint16
		want int
	}{
		{0, 1}, {100, 1}, {101, 2}, {300, 2}, {301, 3}, {600, 3}, {601, 4}, {900, 4},
	}
	for _, c := range cases {
		if got := ForLoss(c.bps); got != c.want {
			t.Fatalf("ForLoss(%d) = %d, want %d", c.bps, got, c.want)
		}
	}
}

func TestForLossRespetaElTecho(t *testing.T) {
	if got := ForLoss(5000); got != MaxConnsPerNode {
		t.Fatalf("ForLoss saturado = %d, want %d", got, MaxConnsPerNode)
	}
}

func TestApplySubeSinEsperar(t *testing.T) {
	if got := Apply(1, 150); got != 2 {
		t.Fatalf("Apply(1, 150) = %d, want 2: la pérdida sube ya", got)
	}
	if got := Apply(2, 700); got != MaxConnsPerNode {
		t.Fatalf("Apply(2, 700) = %d, want %d", got, MaxConnsPerNode)
	}
}

func TestApplyBajaSoloConMargen(t *testing.T) {
	// 2 conexiones con pérdida justo en el borde (101): no baja, no hay margen.
	if got := Apply(2, 101); got != 2 {
		t.Fatalf("Apply(2, 101) = %d, want 2 (sin margen no baja)", got)
	}
	// Con 60 bps la pérdida queda 41 por debajo del suelo del escalón: baja.
	if got := Apply(2, 60); got != 1 {
		t.Fatalf("Apply(2, 60) = %d, want 1", got)
	}
}

func TestApplyNoOscilaEnElUmbral(t *testing.T) {
	// Serie oscilando alrededor de 101: tras subir a 2 no debe volver a 1
	// hasta bajar de 51.
	cur := 1
	for _, bps := range []uint16{150, 105, 99, 95, 60} {
		cur = Apply(cur, bps)
	}
	if cur != 1 {
		t.Fatalf("serie oscilante terminó en %d, want 1 al bajar a 60", cur)
	}

	cur = 1
	for _, bps := range []uint16{150, 105, 99, 103, 101} {
		cur = Apply(cur, bps)
	}
	if cur != 2 {
		t.Fatalf("serie en el borde terminó en %d, want 2", cur)
	}
}

func TestApplySaneaEntradas(t *testing.T) {
	if got := Apply(0, 0); got != 1 {
		t.Fatalf("Apply(0,0) = %d, want 1", got)
	}
	if got := Apply(99, 900); got != MaxConnsPerNode {
		t.Fatalf("Apply(99,900) = %d, want %d", got, MaxConnsPerNode)
	}
}
