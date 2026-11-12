package tunnel

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AC3 (regla): sin UDP propio la cadencia pasa; un beacon de 30 s o de
// exactamente 300 s (techo inclusivo) no.
func TestT1303OwnUdpRule(t *testing.T) {
	if !OwnUdpCadenceOK(nil) {
		t.Fatal("UDP propio ausente debe pasar la regla")
	}
	ok := []time.Duration{301 * time.Second, 400 * time.Second, 24 * time.Hour}
	if !OwnUdpCadenceOK(ok) {
		t.Fatalf("huecos > 300 s deben pasar: %v", ok)
	}
	for _, bad := range []time.Duration{0, 30 * time.Second, MaxOwnUdpGap} {
		if OwnUdpCadenceOK([]time.Duration{bad}) {
			t.Fatalf("hueco %v <= %v no debe pasar", bad, MaxOwnUdpGap)
		}
	}
}

func TestT1303UdpGaps(t *testing.T) {
	if UdpGaps([]int64{7}) != nil {
		t.Fatal("un paquete no produce huecos")
	}
	g := UdpGaps([]int64{0, 30, 90})
	if len(g) != 2 || g[0] != 30*time.Second || g[1] != 60*time.Second {
		t.Fatalf("huecos: %v, want [30s 60s]", g)
	}
}

// AC3 (CI con fixture pcap): el script da exit 0 solo si el UDP propio es
// ausente/esporadico (quiet) y exit 1 con beacon de 30 s. Requiere bash;
// sin bash (rara CI Windows sin Git Bash) se omite con aviso.
func TestT1303UdpCadenceCheckScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash no disponible: la regla la ejecuta la CI con fixture pcap")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "detect", "udp_cadence_check.sh")
	run := func(t *testing.T, fixture string) (string, int) {
		t.Helper()
		cmd := exec.Command(bash, script, "--fixture="+filepath.Join(root, fixture))
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("script %s: %v\n%s", fixture, err, out)
		}
		return string(out), code
	}

	if out, code := run(t, filepath.Join("testdata", "detect", "udp_quiet.pcap")); code != 0 || !strings.Contains(out, "result=PASS") {
		t.Fatalf("fixture quiet: rc=%d out=%s, want exit 0 PASS", code, out)
	}
	if out, code := run(t, filepath.Join("testdata", "detect", "udp_c2_beacon.pcap")); code != 1 || !strings.Contains(out, "result=FAIL") {
		t.Fatalf("fixture beacon 30 s: rc=%d out=%s, want exit 1 FAIL", code, out)
	}
}

// AC1/AC2 (parte estructural): el agente no abre ningun socket UDP — ni
// ListenPacket, ni DialUDP, ni "udp" como red de dial en el plano del
// nodo. Todo el control (heartbeat, cmds, bye) sale por el mux TLS/H2:
// un nodo, una cadencia, un destino visible.
func TestT1303NoUdpSocketsInAgent(t *testing.T) {
	dirs := []string{".", "../agent", "../proxy"}
	needle := []string{`net.ListenPacket`, `Dial("udp"`, `DialUDP`, `ListenUDP`, `"udp", `}
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("dir %s: %v", dir, err)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range needle {
				if strings.Contains(string(b), n) {
					t.Errorf("%s: found %q — el plano del nodo no debe abrir UDP propio (A1-07)",
						filepath.Join(dir, e.Name()), n)
				}
			}
		}
	}
}
