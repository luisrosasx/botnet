package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/botnet/botnet/internal/agent"
)

// T-0304 AC3: `botctl status` imprime slug= de node.json.
func TestStatusPrintsSlugFromNodeJSON(t *testing.T) {
	dir := t.TempDir()
	id := agent.NodeIdentity{
		NodeID:     "11111111-1111-2222-3333-444444444444",
		Slug:       "D5QtuP64bzWE",
		Arch:       "amd64",
		Rendezvous: []string{"rdv.botnet.example:443"},
		TTLS:       900,
		AgentVer:   "v0.1.0",
	}
	if err := agent.SaveNodeIdentity(dir, id); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runStatus(dir, filepath.Join("release", "botda.exe")); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "slug=D5QtuP64bzWE\n") {
		t.Fatalf("falta slug= en node.json:\n%s", out)
	}
	// T-1403 AC3: name del binario en ejecucion == entrada del manifest
	// por clase (botda para clase A).
	if !strings.Contains(out, "name=botda\n") {
		t.Fatalf("falta name=botda:\n%s", out)
	}
}

// T-1305: un artefacto distribuido como botd-amd64 reporta la raiz.
func TestBinaryNameStripsExtAndArchTag(t *testing.T) {
	cases := map[string]string{
		"dist/botd-amd64.exe": "botd",
		"/data/botd/botda":    "botda",
		"srvhost-mipsle":      "srvhost",
		"botctl":              "botctl",
	}
	for path, want := range cases {
		if got := binaryName(path); got != want {
			t.Errorf("binaryName(%q) = %q, want %q", path, got, want)
		}
	}
}

// Sin node.json valido no hay status: error, no salida vacia.
func TestStatusFailsWithoutNodeJSON(t *testing.T) {
	if err := runStatus(t.TempDir(), "botctl"); err == nil {
		t.Fatal("node.json inexistente debe dar error")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := stdoutWriter
	defer func() { stdoutWriter = old }()
	var buf strings.Builder
	stdoutWriter = &buf
	fn()
	return buf.String()
}
