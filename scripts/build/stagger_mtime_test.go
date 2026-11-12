// Package scripts/build asienta los tests de los scripts de bundle
// (T-1305): mtimes escalonados y renombres deterministas por clase.
// Los builds reales de build.sh (linux, sin upx salvo que exista la
// herramienta) se ejecutan contra un dist temporal: son lentos solo la
// primera vez por GOARCH (cache de go). Ojo: las rutas Windows viajan a
// bash como argumentos posicionales de `bash -c` ($0), nunca interpoladas
// sin comillas — si no, los backslashes se comen.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func runBash(t *testing.T, script string, args ...string) (string, int) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash no disponible (Git Bash): los scripts corren en la CI Unix")
	}
	cmd := exec.Command(bash, append([]string{"-c", script}, args...)...)
	cmd.Dir = filepath.Join("..", "..")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, out)
	}
	return string(out), code
}

// AC1: stagger_mtime.sh --self-test — bundle de 4 ficheros con mtime
// comun -> todos los mtimes distintos con paso >= 10 s.
func TestT1305StaggerMTimeSelfTest(t *testing.T) {
	out, code := runBash(t, "scripts/build/stagger_mtime.sh --self-test")
	if code != 0 || !strings.Contains(out, "result=PASS") {
		t.Fatalf("self-test stagger_mtime: rc=%d out=%s", code, out)
	}
}

// AC2+AC3: renombre determinista por clase con -ldflags y guard de
// magia UPX: clase C (mipsle) se llama srvhost-*, lleva class-C y NO
// class-A; clase A (amd64) conserva botd-*, lleva class-A y no empaqueta.
func TestT1305ClassRenameAndUPXGuard(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go no disponible")
	}
	tmp := t.TempDir()
	build := func(arch, profile string) {
		t.Helper()
		out, code := runBash(t,
			`deploy/sliver-builder/build.sh --arch=$1 --profile=$2 --dist="$0" --version=t1305`,
			tmp, arch, profile)
		if code != 0 {
			t.Fatalf("build %s/%s rc=%d out=%s", profile, arch, code, out)
		}
	}
	has := func(path, needle string) bool {
		t.Helper()
		_, code := runBash(t, `grep -a -q "$1" "$0"`, path, needle)
		return code == 0
	}
	// unpacked: ruta de la imagen sin capa UPX (copia desempaquetada si
	// el builder empareto; la original intacta).
	unpacked := func(path string) string {
		t.Helper()
		out, code := runBash(t, `f="$0"; if grep -a -q 'UPX!' "$f"; then cp "$f" "$f.raw" && upx -d -q -f --no-backup "$f.raw" >/dev/null 2>&1 && echo "$f.raw" || echo "$f"; else echo "$f"; fi`, path)
		p := strings.TrimSpace(strings.ReplaceAll(out, "\r", ""))
		if p == "" || code != 0 {
			t.Fatalf("no se pudo obtener imagen legible de %s: %s", path, out)
		}
		return p
	}

	build("mipsle", "C")
	pathC := filepath.Join(tmp, "srvhost-mipsle")
	if _, err := os.Stat(pathC); err != nil {
		t.Fatalf("clase C: el artefacto se llama srvhost (rename del perfil), no botd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "botd-mipsle")); err == nil {
		t.Fatal("clase C no debio emitir botd-mipsle")
	}
	// strings de la etiqueta embebida por -ldflags: si el builder tenia
	// upx (B/C lo aceptan), la etiqueta se aserta sobre la imagen
	// desempaquetada — lo que `strings` veria en el proceso desplegado.
	labelC := unpacked(pathC)
	if !has(labelC, "class-C") {
		t.Fatalf("clase C sin etiqueta class-C embebida (%s)", labelC)
	}

	build("amd64", "A")
	pathA := filepath.Join(tmp, "botd-amd64")
	if _, err := os.Stat(pathA); err != nil {
		t.Fatalf("clase A: artefacto botd-amd64: %v", err)
	}
	// AC3: clase A sin empaquetar — sin magia UPX! (guard de build.sh).
	if has(pathA, "UPX!") {
		t.Fatal("clase A empaquetada sin necesidad (magia UPX! presente)")
	}
	if !has(pathA, "class-A") {
		t.Fatal("clase A sin etiqueta class-A embebida")
	}
}
