package accounting

import (
	"math"
	"testing"
	"time"
)

// TestMetering1GB — criterio 1 T-1001: transferencia conocida de 1 GB
// por sesion de test; el medidor del nodo queda dentro de la tolerancia
// unica alineada a Stripe: drift_pct=0.42 (≤0.5).
func TestMetering1GB(t *testing.T) {
	base := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	step := base
	clock := func() time.Time {
		step = step.Add(time.Millisecond)
		return step
	}
	f := RunKnown1GB("node-001", clock)

	if f.MasterBytes != GB {
		t.Fatalf("el nodo debe medir exactamente 1 GB: %d", f.MasterBytes)
	}
	drift := DriftPct(f.MasterBytes, f.AuditedBytes)
	if math.Abs(drift-0.42) > 0.005 {
		t.Fatalf("drift_pct=%.4f, want ~0.42", drift)
	}
	if !WithinTolerance(f.MasterBytes, f.AuditedBytes) {
		t.Fatalf("drift %.2f%% fuera de la tolerancia unica %.1f%%", drift, TolerancePct)
	}
	t.Logf("drift_pct=%.2f(≤%.1f) master=%d audited=%d", drift, TolerancePct, f.MasterBytes, f.AuditedBytes)
}

// TestCrashLoss — criterio 3 T-1001: kill -9 a mitad de sesion pierde
// ≤ 90 s de bytes; el diff gateway-vs-usage reconstruido en fixture
// queda acotado por spool (60 s) + deteccion (30 s). Reloj controlado.
func TestCrashLoss(t *testing.T) {
	const rate = 1 << 20 // 1 MiB/s
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	now := base
	clock := func() time.Time { return now }
	m := NewNodeMeter("node-002", clock)

	// Sesion de 297 s con spool a disco cada SpoolInterval; kill -9 en
	// el segundo 297. El spool sobrevive al proceso: el usage del crash
	// es el contador volcado, y el gateway (auditoria) ya vio lo vivo.
	var lastSpoolBytes, seenByGateway int64
	for sec := 1; sec <= 297; sec++ {
		now = base.Add(time.Duration(sec) * time.Second)
		m.Add(rate)
		seenByGateway += rate
		if sec%int(SpoolInterval/time.Second) == 0 {
			m.Spool()
			lastSpoolBytes = m.Bytes()
		}
	}
	lastSpool := m.LastSpool()
	if lastSpool.IsZero() {
		t.Fatal("sin spools en la sesion")
	}
	// Diff gateway-vs-usage reconstruido: lo que el gateway conto y el
	// spool no alcanzo a volcar = bytes perdidos por el kill -9.
	lostBytes := seenByGateway - lastSpoolBytes
	if lostBytes < 0 {
		lostBytes = 0
	}
	lossSec := float64(lostBytes) / rate
	if lossSec > MaxCrashLossSeconds {
		t.Fatalf("perdida por crash %.1f s de bytes > cota %.0f s", lossSec, MaxCrashLossSeconds)
	}
	// La cota estructural del codigo: spool (60 s) + deteccion (30 s).
	if CrashDetectionMargin+SpoolInterval > MaxCrashLossSeconds*time.Second {
		t.Fatalf("cota estructural %s > %.0f s", SpoolInterval+CrashDetectionMargin, MaxCrashLossSeconds)
	}
	// Y CrashLossSeconds (diff gateway-vs-usage en segundos) respeta la
	// misma cota con el reloj del fixture.
	if s := CrashLossSeconds(rate, now, lastSpool); s > MaxCrashLossSeconds {
		t.Fatalf("CrashLossSeconds=%.1f > %.0f", s, MaxCrashLossSeconds)
	}
	t.Logf("crash a t=297s: perdida %.0f s de bytes (≤ %.0f s), diff=%d B",
		lossSec, MaxCrashLossSeconds, lostBytes)
}

// TestToleranceUnica blinda el DoD: una sola tolerancia listada.
func TestToleranceUnica(t *testing.T) {
	if TolerancePct != 0.5 {
		t.Fatalf("la tolerancia unica debe ser 0,5%%, hay %.2f", TolerancePct)
	}
	// 0,51%% de desvio ya es alarma: no existe un segundo umbral que
	// lo absorba (fuera el ±1%% y el 5%%).
	master := int64(1_000_000_000)
	audited := int64(float64(master) * 1.0051)
	if WithinTolerance(master, audited) {
		t.Fatalf("0.51%% de desvio no debe pasar la tolerancia")
	}
}
