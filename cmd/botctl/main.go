// botctl — CLI del dispositivo: install/enroll/status/optout
// `status` imprime la identidad plana del nodo (T-0304 AC3: slug= de
// node.json) y el name del binario en ejecucion (T-1403 AC3: assert
// cruzado contra la entrada del manifest por clase).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/config"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

// stdoutWriter destino de la salida: os.Stdout en produccion, buffer en tests.
var stdoutWriter io.Writer = os.Stdout

const name = "botctl"

// archTags son los tags de la matriz F1: un binario distribuido como
// `botd-amd64` conserva ese sufijo y el name canónico es la raiz.
var archTags = []string{"amd64", "arm64", "armv5", "armv7", "mipsbe", "mipsle"}

// binaryName es el nombre del binario en ejecucion: base sin extension
// ni sufijo de arch (botda.exe -> botda; botd-amd64 -> botd).
func binaryName(exePath string) string {
	base := strings.TrimSuffix(filepath.Base(exePath), filepath.Ext(exePath))
	for _, tag := range archTags {
		if trimmed := strings.TrimSuffix(base, "-"+tag); trimmed != base {
			base = trimmed
			break
		}
	}
	return base
}

// runStatus imprime name/slug del nodo y devuelve el error de leer
// node.json (exePath es el binario en ejecucion, inyectable en tests).
func runStatus(nodeDir, exePath string) error {
	exe := exePath
	if exe == "" {
		p, perr := os.Executable()
		if perr != nil {
			return perr
		}
		exe = p
	}
	id, err := agent.LoadNodeIdentity(nodeDir)
	if err != nil {
		return err
	}
	out := stdoutWriter
	fmt.Fprintf(out, "name=%s\n", binaryName(exe))
	fmt.Fprintf(out, "node_id=%s\n", id.NodeID)
	fmt.Fprintf(out, "slug=%s\n", id.Slug)
	fmt.Fprintf(out, "arch=%s\n", id.Arch)
	fmt.Fprintf(out, "agent_ver=%s\n", id.AgentVer)
	return nil
}

func main() {
	var (
		showVer bool
		cfgPath string
	)
	flag.StringVar(&cfgPath, "config", "", "path to JSON config file")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.Parse()

	if showVer {
		fmt.Printf("%s %s\n", name, version)
		return
	}
	cmd := "status"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}
	if cmd != "status" {
		fmt.Fprintf(os.Stderr, "botctl: comando no implementado: %s\n", cmd)
		os.Exit(2)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := runStatus(cfg.NodeDir, ""); err != nil {
		fmt.Fprintf(os.Stderr, "botctl status: %v\n", err)
		os.Exit(1)
	}
}
