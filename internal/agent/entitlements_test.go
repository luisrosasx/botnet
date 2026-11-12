package agent

import (
	"testing"

	"github.com/botnet/botnet/pkg/proto"
)

// T-0306 AC2: precedencia stable_version server > local; valor ausente
// = default del perfil de clase; config_rev monotonico.
func TestEntitlementsPrecedence(t *testing.T) {
	m := NewEntitlementManager('B', Entitlements{StableVersion: "local-0.9"})
	snap := m.Snapshot()
	if snap.BandwidthCapKbps != 3000 || snap.MonthlyGbCap != 150 || snap.MaxStreams != 150 {
		t.Fatalf("defaults de clase B: %+v, want 3000/150/150", snap)
	}
	if snap.StableVersion != "local-0.9" {
		t.Fatalf("sin servidor, stable_version = %q != local", snap.StableVersion)
	}
	if !m.Apply(&proto.HeartbeatResponse{ConfigRev: 1, StableVersion: "srv-1.1", BandwidthCapKbps: 3072}) {
		t.Fatal("rev 1 no aplicado")
	}
	snap = m.Snapshot()
	if snap.StableVersion != "srv-1.1" {
		t.Fatalf("stable_version server > local: %q", snap.StableVersion)
	}
	if m.Apply(&proto.HeartbeatResponse{ConfigRev: 1, StableVersion: "srv-1.1"}) {
		t.Fatal("config_rev repetido debe ignorarse")
	}
	if m.Apply(&proto.HeartbeatResponse{ConfigRev: 1}) {
		t.Fatal("config_rev viejo debe ignorarse")
	}
	// rev 2 sin stable_version: el valor ausente cae al local, y lo
	// ausente de ancho al perfil (3000 kbps en clase B).
	if !m.Apply(&proto.HeartbeatResponse{ConfigRev: 2}) {
		t.Fatal("rev 2 no aplicado")
	}
	snap = m.Snapshot()
	if snap.StableVersion != "local-0.9" || snap.BandwidthCapKbps != 3000 {
		t.Fatalf("ausentes no cayeron a local/perfil: %+v", snap)
	}
	if !m.Apply(&proto.HeartbeatResponse{ConfigRev: 3, StableVersion: "srv-1.2"}) {
		t.Fatal("rev 3 no aplicado")
	}
	if m.Snapshot().StableVersion != "srv-1.2" {
		t.Fatal("server debe seguir ganando a local cuando esta presente")
	}
	// Clase desconocida cae al perfil C (el mas frugal).
	if got := NewEntitlementManager('Z', Entitlements{}).Snapshot().BandwidthCapKbps; got != 2000 {
		t.Fatalf("clase Z: ancho = %d != 2000 (perfil C)", got)
	}
}

// T-0306 AC1 (doble del caudal iperf3): un heartbeat con config_rev
// mayor aplica el tope nuevo en el mismo proceso, sin reinicio: la
// ventana de 120 s del criterio queda cubierta por la aplicacion
// inmediata del cap. El ±10 % de iperf3 es medicion de hardware.
func TestEntitlementsApplyLiveWithoutRestart(t *testing.T) {
	m := NewEntitlementManager('B', Entitlements{})
	// el 201 siembra 3.072 kbps (iperf3 medido por el probe)
	if !m.ApplyEnroll(&proto.EnrollAPIResponse{BandwidthCapKbps: 3072, MonthlyGbCap: 150, ConfigRev: 1}) {
		t.Fatal("entitlements del alta no aplicados")
	}
	if got := m.BandwidthBytesPerSec(); got != 3072*1024 {
		t.Fatalf("caudal inicial = %d B/s != 3072 kbps", got)
	}
	if !m.Apply(&proto.HeartbeatResponse{ConfigRev: 2, BandwidthCapKbps: 2048}) {
		t.Fatal("config_rev 2 no aplicado")
	}
	if got := m.BandwidthBytesPerSec(); got != 2048*1024 {
		t.Fatalf("tras hb rev2 caudal = %d B/s != 2048 kbps (debe bajar sin reinicio)", got)
	}
	if m.Snapshot().ConfigRev != 2 {
		t.Fatalf("config_rev = %d != 2", m.Snapshot().ConfigRev)
	}
}

// T-0306 AC3: monthly_gb_cap=150 presente en el 201 y adoptado por el
// agente. (El assert SQL nodes.monthly_gb_cap es tierra de EP-08.)
func TestEntitlementsMonthlyGB150InEnroll(t *testing.T) {
	m := NewEntitlementManager('A', Entitlements{})
	resp := proto.EnrollAPIResponse{MonthlyGbCap: 150, BandwidthCapKbps: 5000}
	if !m.ApplyEnroll(&resp) {
		t.Fatal("ApplyEnroll rechazado")
	}
	snap := m.Snapshot()
	if snap.MonthlyGbCap != 150 {
		t.Fatalf("monthly_gb_cap = %d != 150", snap.MonthlyGbCap)
	}
	if snap.BandwidthCapKbps != 5000 {
		t.Fatalf("ancho clase A = %d != 5000", snap.BandwidthCapKbps)
	}
}

// La cadena arch→clase→perfil: mips al tope C de 2 Mbps.
func TestDeviceClassForArch(t *testing.T) {
	for arch, want := range map[string]byte{
		"mipsbe": 'C', "mipsle": 'C', "armv5": 'B', "armv7": 'B', "arm64": 'A', "amd64": 'A', "x": 'C',
	} {
		if got := DeviceClassForArch(arch); got != want {
			t.Fatalf("DeviceClassForArch(%q) = %c != %c", arch, got, want)
		}
	}
}
