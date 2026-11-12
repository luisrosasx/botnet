package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func genRoot(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func sampleManifest(t *testing.T, dir string) *Manifest {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"amd64", "mipsbe"} {
		if err := os.WriteFile(filepath.Join(dir, "botd-"+tag),
			[]byte("elf fake "+tag), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m, err := NewManifest(dir, "1.2.3", "https://dl.botnet.example/1.2.3/{file}")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// T-1403.1: sign/verify round trip y alteracion de 1 byte del sig => rechazo.
func TestSignVerifyTamper(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest(t, dir)
	pub, priv := genRoot(t)
	if err := m.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if m.Root != base64.StdEncoding.EncodeToString(pub) {
		t.Fatalf("Root announcement %q != pubkey raiz", m.Root)
	}
	if err := m.Verify(pub); err != nil {
		t.Fatalf("Verify root: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(m.Entries[0].Sig)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] ^= 0x01 // 1 byte del sig alterado
	m.Entries[0].Sig = base64.StdEncoding.EncodeToString(raw)
	if err := m.Verify(pub); !errors.Is(err, ErrRejectedSig) {
		t.Fatalf("sig alterado: err %v, want ErrRejectedSig", err)
	}
}

// T-1403.2: un manifiesto firmado con clave ajena se rechaza ANTES de
// ejecutar (VerifyPinned con la raiz compilada).
func TestForeignRootRejectedBeforeExec(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest(t, dir)
	_, foreign := genRoot(t) // clave privada ajena a la raiz
	pinned, _ := genRoot(t)  // raiz fijada en el binario
	if err := m.Sign(foreign); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(pinned); !errors.Is(err, ErrRejectedSig) {
		t.Fatalf("clave ajena: err %v, want ErrRejectedSig", err)
	}
	t.Cleanup(func() { RootPubKeyBase64 = "" })
	RootPubKeyBase64 = ""
	if err := m.VerifyPinned(); !errors.Is(err, ErrNoRootKey) {
		t.Fatalf("sin raiz compilada: err %v, want ErrNoRootKey", err)
	}
}

// T-1403.2: print-root coincide byte a byte con el fichero de la raiz.
func TestPrintRootMatchesPubFile(t *testing.T) {
	pub, _ := genRoot(t)
	b64 := base64.StdEncoding.EncodeToString(pub)
	path := filepath.Join(t.TempDir(), "root_ed25519.pub")
	if err := os.WriteFile(path, []byte(b64+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RootPubKeyBase64 = "" })
	RootPubKeyBase64 = b64
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if PrintRoot() != string(raw[:len(raw)-1]) {
		t.Fatalf("print-root %q != contenido de root_ed25519.pub", PrintRoot())
	}
}

// T-1403.3 + renaming T-1305: entrada por clase con name canonico y
// sha256 de 64 hex por arch de la matriz.
func TestManifestEntriesByClass(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest(t, dir)
	if len(m.Entries) != 2 {
		t.Fatalf("entradas %d, want 2", len(m.Entries))
	}
	for _, e := range m.Entries {
		wantClass := ClassForArch(e.Arch)
		if e.Class != wantClass {
			t.Fatalf("%s: class %q want %q", e.Arch, e.Class, wantClass)
		}
		if e.Name != NameForClass(wantClass) {
			t.Fatalf("%s: name %q != manifest name por clase %q", e.Arch, e.Name, NameForClass(wantClass))
		}
		if len(e.SHA256) != 64 {
			t.Fatalf("%s: sha256 %q", e.Arch, e.SHA256)
		}
		if !strings.Contains(e.URL, "botd-"+e.Arch) {
			t.Fatalf("%s: url %q sin fichero", e.Arch, e.URL)
		}
	}
}

// T-1404.1: flujo e2e con download real (httptest) -> botd.new -> rename
// atomico (botd.new ya no existe) -> Commit: stable_version local nueva.
func TestStageCommitE2E(t *testing.T) {
	body := []byte("elf fake mipsbe") // mismo contenido que el fixture del manifest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1.2.3/botd-mipsbe" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := sampleManifest(t, dir)
	_, priv := genRoot(t)
	t.Cleanup(func() { RootPubKeyBase64 = "" })
	RootPubKeyBase64 = base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))

	nodeDir := t.TempDir()
	u := NewUpdater(nodeDir, "C")
	var e Entry
	for _, x := range m.Entries {
		if x.Arch == "mipsbe" {
			e = x
		}
	}
	if e.Arch != "mipsbe" {
		t.Fatal("manifest de fixture sin entrada mipsbe")
	}
	e.URL = srv.URL + "/1.2.3/botd-mipsbe"
	if err := m.Sign(priv); err != nil {
		t.Fatal(err)
	}
	active, err := u.Stage(m, &e, func(url string) ([]byte, error) {
		resp, err := http.Get(url)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if active != filepath.Join(nodeDir, "botd") {
		t.Fatalf("activa %q want botd", active)
	}
	got, err := os.ReadFile(active)
	if err != nil || string(got) != string(body) {
		t.Fatalf("binario activo no coincide: %q err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(nodeDir, "botd.new")); !os.IsNotExist(err) {
		t.Fatal("botd.new debe consumirse en el rename atomico")
	}
	if err := u.Commit("1.2.3"); err != nil {
		t.Fatal(err)
	}
	if u.StableVersion() != "1.2.3" {
		t.Fatalf("stable_version %q want 1.2.3", u.StableVersion())
	}
}

// T-1404.1 (rechazo antes de escribir): con sig de clave ajena, Stage falla
// con ErrRejectedSig y no toca el flash (ni botd ni botd.new).
func TestStageRejectsBeforeFlash(t *testing.T) {
	dir := t.TempDir()
	m := sampleManifest(t, dir)
	_, signKey := genRoot(t)
	pinnedPub, _ := genRoot(t)
	if err := m.Sign(signKey); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RootPubKeyBase64 = "" })
	RootPubKeyBase64 = base64.StdEncoding.EncodeToString(pinnedPub)
	nodeDir := t.TempDir()
	u := NewUpdater(nodeDir, "C")
	e := m.Entries[0]
	_, err := u.Stage(m, &e, func(string) ([]byte, error) { return []byte("x"), nil })
	if !errors.Is(err, ErrRejectedSig) {
		t.Fatalf("Stage con sig ajeno: err %v, want ErrRejectedSig", err)
	}
	if _, err := os.Stat(filepath.Join(nodeDir, "botd")); !os.IsNotExist(err) {
		t.Fatal("no debe escribirse botd tras rechazo de firma")
	}
	if _, err := os.Stat(filepath.Join(nodeDir, "botd.new")); !os.IsNotExist(err) {
		t.Fatal("no debe quedar botd.new tras rechazo de firma")
	}
}

// T-1404.3: A/B solo en clase A (2 slots) y slot unico en B/C (0 slots
// botd.[AB]); presupuesto de flash intacto.
func TestSlotPolicyByClass(t *testing.T) {
	nodeDir := t.TempDir()
	u := NewUpdater(nodeDir, "A")
	dir := t.TempDir()
	m := sampleManifest(t, dir)
	pub, priv := genRoot(t)
	if err := m.Sign(priv); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RootPubKeyBase64 = "" })
	RootPubKeyBase64 = base64.StdEncoding.EncodeToString(pub)
	e := m.Entries[0]
	fetch := func(string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, "botd-"+e.Arch))
	}
	for i := 0; i < 2; i++ {
		if _, err := u.Stage(m, &e, fetch); err != nil {
			t.Fatalf("stage A #%d: %v", i, err)
		}
	}
	if u.SlotCount() != 2 {
		t.Fatalf("clase A slots %d, want 2 (botd.[AB])", u.SlotCount())
	}
	uC := NewUpdater(t.TempDir(), "C")
	if _, err := uC.Stage(m, &e, fetch); err != nil {
		t.Fatal(err)
	}
	if uC.SlotCount() != 0 {
		t.Fatalf("clase C slots %d, want 0 (slot unico)", uC.SlotCount())
	}
}

// T-1404.2: crash-loop fixture (3 salidas 75 en 15 min, reloj acelerado) =>
// rollback server-side: el agente vuelve a stable_version (< 20 min simulados,
// assert de version final).
func TestCrashLoopRollbackAcceleratedClock(t *testing.T) {
	tr := NewCrashTracker()
	base := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	steps := []time.Duration{0, 4 * time.Minute, 8 * time.Minute}
	var final string
	current := "1.2.3"
	stable := "1.1.9"
	for _, s := range steps {
		now := base.Add(s)
		tr.RecordExit(now)
		if tr.ShouldRollback(now) {
			final = PlanRollback(current, stable)
			if now.Sub(base) >= 20*time.Minute {
				t.Fatalf("rollback a los %v, want < 20 min", now.Sub(base))
			}
		}
	}
	if final != stable {
		t.Fatalf("version final %q, want stable %q", final, stable)
	}
	// Fuera de ventana: una salida antigua ya no cuenta.
	tr2 := NewCrashTracker()
	tr2.RecordExit(base)
	if tr2.ShouldRollback(base.Add(16 * time.Minute)) {
		t.Fatal("salida unica fuera de la ventana no debe disparar rollback")
	}
}
