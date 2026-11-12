package agent

import (
	"os"
	"runtime"
	"testing"
	"time"
)

// T-0301 AC2: GOMEMLIMIT=5MiB, GOGC=40, GOMAXPROCS=1 aplicados y
// exportados al entorno para que el reexec del watchdog los herede.
func TestRuntimeBudgetApplied(t *testing.T) {
	snap := ApplyRuntimeBudget()
	if snap.MemLimitBytes != 5<<20 || snap.GCPercent != 40 || snap.MaxProcs != 1 {
		t.Fatalf("snapshot = %+v, want 5MiB/40/1", snap)
	}
	lines := snap.Lines()
	want := map[string]bool{"GOMEMLIMIT=5MiB": false, "GOGC=40": false, "GOMAXPROCS=1": false}
	for _, l := range lines {
		if _, ok := want[l]; ok {
			want[l] = true
		}
	}
	for k, found := range want {
		if !found {
			t.Fatalf("--print-env debe contener %q en %v", k, lines)
		}
	}
	if os.Getenv("GOMEMLIMIT") != "5MiB" || os.Getenv("GOGC") != "40" || os.Getenv("GOMAXPROCS") != "1" {
		t.Fatalf("entorno no exportado: %s/%s/%s",
			os.Getenv("GOMEMLIMIT"), os.Getenv("GOGC"), os.Getenv("GOMAXPROCS"))
	}
	if runtime.GOMAXPROCS(-1) != 1 {
		t.Fatalf("GOMAXPROCS efectivo = %d != 1", runtime.GOMAXPROCS(-1))
	}
}

// T-0301 AC3 (doble del QEMU): el diff de runtime.NumGoroutine en
// reposo debe quedar < 5. El tope de RSS de 3,1 MB solo es medible en
// hardware; aqui se reporta el heap+stack en uso como proxy.
func TestIdleFootprintStable(t *testing.T) {
	g1, heap1 := IdleFootprint()
	time.Sleep(100 * time.Millisecond)
	g2, heap2 := IdleFootprint()
	if g2-g1 >= 5 || g1-g2 >= 5 {
		t.Fatalf("gorutinas inestables en reposo: %d -> %d", g1, g2)
	}
	if heap1 == 0 || heap2 == 0 {
		t.Fatal("heap en uso = 0: el doble no mide nada")
	}
}

// El tag arch debe mapearse a la matriz F1; BOTNET_ARCH manda (QEMU
// cross desde EP-02).
func TestArchFromRuntime(t *testing.T) {
	t.Setenv("BOTNET_ARCH", "mipsle")
	if got := ArchFromRuntime(); got != "mipsle" {
		t.Fatalf("BOTNET_ARCH ignorado: %q", got)
	}
	t.Setenv("BOTNET_ARCH", "")
	if got := ArchFromRuntime(); got == "" {
		t.Fatal("ArchFromRuntime vacio sin override")
	}
}

// node.json valido/invalido gobierna el modo de arranque (T-0301 AC1).
func TestNodeIdentityRoundTripAndValidity(t *testing.T) {
	dir := t.TempDir()
	id := NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012345", Slug: "k7qz2m4x9p1r", Arch: "mipsle",
		Rendezvous: []string{"rdv1:443"}, TTLS: 86400, AgentVer: "v0.1.0",
	}
	if _, err := LoadNodeIdentity(dir); err == nil {
		t.Fatal("node.json inexistente debe dar error (modo enrolado)")
	}
	if err := SaveNodeIdentity(dir, id); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadNodeIdentity(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.NodeID != id.NodeID || got.Slug != id.Slug || got.TTLS != id.TTLS {
		t.Fatalf("roundtrip: %+v", got)
	}
	bad := id
	bad.Slug = "token-de-dispositivo-largo"
	if err := SaveNodeIdentity(dir, bad); err == nil {
		t.Fatal("slug largo (credencial en la URL) aceptado")
	}
	bad = id
	bad.NodeID = "no-uuid"
	if err := SaveNodeIdentity(dir, bad); err == nil {
		t.Fatal("node_id sin uuid aceptado")
	}
}

func TestUUIDRoundTrip(t *testing.T) {
	id, err := ParseUUID("3f0d1c2b-0000-4000-8000-abcdef012345")
	if err != nil {
		t.Fatal(err)
	}
	if FormatUUID(id) != "3f0d1c2b-0000-4000-8000-abcdef012345" {
		t.Fatalf("FormatUUID = %q", FormatUUID(id))
	}
	if _, err := ParseUUID("3f0d1c2b-0000-4000-8000-abcdef01234"); err == nil {
		t.Fatal("uuid de 31 hex aceptado")
	}
}

// T-0307 AC3: todo lo que el agente escribe bajo state/ sale 0600 y el
// directorio 0700: find /data/botd/state -perm /077 | wc -l = 0.
func TestStateFilesPrivatePerm(t *testing.T) {
	if isWindows() {
		t.Skip("permisos POSIX: medibles en Unix/QEMU; Windows ignora los bits")
	}
	nodeDir := t.TempDir()
	stateDir, err := EnsureStateDir(nodeDir)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, name := range []string{"a.bin", "b.json"} {
		p := stateDir + string(os.PathSeparator) + name
		if err := WritePrivateFile(p, []byte("x")); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if err := SaveSecret(stateDir, pad32(9)); err != nil {
		t.Fatal(err)
	}
	if err := SaveNodeIdentity(nodeDir, NodeIdentity{
		NodeID: "3f0d1c2b-0000-4000-8000-abcdef012345", Slug: "k7qz2m4x9p1r",
	}); err != nil {
		t.Fatal(err)
	}
	paths = append(paths,
		stateDir+string(os.PathSeparator)+SecretFileName,
		nodeDir+string(os.PathSeparator)+NodeJSONName,
	)
	for _, p := range paths {
		if ok, err := PrivatePermOK(p); err != nil || !ok {
			t.Fatalf("%s con permisos de group/other (ok=%v err=%v)", p, ok, err)
		}
	}
}
