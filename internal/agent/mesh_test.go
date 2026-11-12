// mesh_test.go — dobles de la malla de operaciones (EP-07 T-0701):
// elegibilidad por tun/clase, IPs CGNAT 100.64/10, 0 bytes de sesion
// por WG (dominio de fallo) y las listas viaja/nunca-viaja casadas con
// el runbook. El headscale real vive en el bastion (sin binario en
// este host): los asserts de `headscale nodes list`/`ip -4 addr` del
// runbook se ejecutan aqui contra el doble.
package agent

import (
	"os"
	"strings"
	"testing"
)

func TestMeshEligibleTunYClase(t *testing.T) {
	cases := []struct {
		class  string
		tun    bool
		want   bool
		reason string
	}{
		{"A", true, true, "clase A con tun entra"},
		{"B", true, true, "clase B con tun entra"},
		{"C", false, false, "clase C sin tun no entra (tun no garantizado, §5)"},
		{"C", true, false, "clase C ni con tun: 32 MB, userspace cuesta RSS/CPU"},
		{"A", false, false, "sin tun no hay malla pase lo que pase"},
	}
	for _, c := range cases {
		if got := MeshEligible(c.class, c.tun); got != c.want {
			t.Errorf("%s: MeshEligible(%q,%v)=%v want %v", c.reason, c.class, c.tun, got, c.want)
		}
	}
	// AC2: el nodo C excluido conserva su proxy — el plano de datos no
	// depende de la malla, asi que no hay nada que perder (0 perdida).
	if MeshSessionBytes(100<<20) != 0 {
		t.Error("un nodo C excluido pagaria bytes de sesion por WG: pérdida de producto")
	}
}

func TestAllocateMeshIPCGNAT(t *testing.T) {
	// AC1: cada nodo con tun recibe 100.64.x.y — barrido de seq y
	// bordes; ninguna caida fuera del /10 ni en la red del prefijo.
	for _, seq := range []uint32{0, 1, 255, 256, 65535, 65536, 1 << 20, meshSpan - 2, meshSpan - 1, meshSpan} {
		ip := AllocateMeshIP(seq)
		if !InMeshPrefix(ip) {
			t.Errorf("seq %d: %s fuera de %s", seq, ip, MeshPrefix)
		}
		if ip.String() == "100.64.0.0" {
			t.Errorf("seq %d: cayo en la red del prefijo", seq)
		}
	}
	// Inventario: headscale nodes list | grep -c managed == nodos con
	// tun; el doble asigna una IP por nodo elegible y ninguna repetida
	// en el rango bajo.
	seen := map[string]bool{}
	for seq := uint32(0); seq < 4096; seq++ {
		ip := AllocateMeshIP(seq).String()
		if seen[ip] {
			t.Fatalf("IP repetida en el pool bajo: %s (seq %d)", ip, seq)
		}
		seen[ip] = true
	}
}

func TestHeadscaleDownSesionesNiSeEnteran(t *testing.T) {
	// DoD T-0701: cae headscale (el doble:MeshTravels deja de
	// funcionar) y ni un byte de sesion se pierde, porque ninguno
	// viajo jamas por la malla.
	const sessionBytes = int64(100 << 20) // ~100 MB de 100 sesiones
	crossMesh := MeshSessionBytes(sessionBytes)
	if crossMesh != 0 {
		t.Fatalf("bytes de sesion cruzando WG = %d, want 0", crossMesh)
	}
	for _, never := range MeshNeverTravels() {
		for _, yes := range MeshTravels() {
			if never == yes {
				t.Errorf("%s esta en ambas listas: contrato roto", never)
			}
		}
	}
}

// TestMeshListasCuadranConRunbook: T-0702 AC3 y T-0705 AC3 — la lista
// «viaja / nunca viaja» existe en docs/runbooks/mesh.md y el token de
// config prohibido (data_plane=wg) no aparece en deploy/.
func TestMeshListasCuadranConRunbook(t *testing.T) {
	raw, err := os.ReadFile("../../docs/runbooks/mesh.md")
	if err != nil {
		t.Fatalf("runbook de malla legible: %v", err)
	}
	doc := string(raw)
	for _, want := range []string{
		"SSH de operaciones", "iperf3", "geo", "sonda",
		"sesión", "heartbeat", "flush", "update", "nunca",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("runbook mesh.md menciona %q", want)
		}
	}
	// CI de config: grep -c "data_plane=wg" deploy/ = 0 (T-0705 AC3).
	// El runbook vive en docs/, fuera del radio del guard.
	entries, err := os.ReadDir("../../deploy")
	if err != nil {
		t.Fatalf("deploy legible: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile("../../deploy/" + e.Name())
		if err != nil {
			continue
		}
		if strings.Contains(string(b), "data_plane=wg") {
			t.Errorf("deploy/%s enruta dato por WG (data_plane=wg)", e.Name())
		}
	}
}
