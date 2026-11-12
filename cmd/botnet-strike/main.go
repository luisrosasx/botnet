// botnet-strike — intrusión y entrega del binario (EP-02).
// Subcomandos que consumen los runners shell (scripts/intrusion/*.sh):
//
//	protocols                 lista los 4 protocolos del diccionario
//	legba-parse [--banner=F]  stdin = salida cruda de legba -> veredicto JSON
//	cve-lookup --model=M      busca el modelo en cve-models.yaml -> JSON
//	cve-validate              valida el esquema del YAML (exit 2 si inválido)
//
// Sin subcomando conserva el comportamiento del suelo: --version y --config.
// La lógica vive en internal/intrusion; este main es fina capa de CLI.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/intrusion"
	"github.com/botnet/botnet/internal/logx"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const name = "botnet-strike"

// códigos de salida: 3 = CVE ausente para el modelo (T-0202 criterio 2: no es
// fallo, es ausencia); 2 = esquema inválido; 1 = sin credencial válida.
const (
	exitNoCred  = 1
	exitNoCVE   = 3
	exitSchema  = 2
	defaultCVEY = "scripts/intrusion/cve-models.yaml"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout))
}

func run(args []string, stdin io.Reader, stdout *os.File) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stdout)
	var (
		showVer bool
		cfgPath string
		banner  string
		model   string
		cveFile string
	)
	fs.StringVar(&cfgPath, "config", "", "path to JSON config file")
	fs.BoolVar(&showVer, "version", false, "print version and exit")
	fs.StringVar(&banner, "banner", "", "banner/evidence file for model_guess")
	fs.StringVar(&model, "model", "", "device model for cve-lookup")
	fs.StringVar(&cveFile, "cve-file", defaultCVEY, "path to cve-models.yaml")
	// El flag package para en el primer arg posicional: el subcomando se
	// extrae ANTES de parsear o sus flags nunca se leerían.
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if rest := fs.Args(); len(rest) > 0 {
		if sub == "" {
			sub = rest[0]
		} else {
			fmt.Fprintf(os.Stderr, "%s: argumento suelto %q\n", name, rest[0])
			return 2
		}
	}

	if showVer && sub == "" {
		fmt.Printf("%s %s\n", name, version)
		return 0
	}

	switch sub {
	case "protocols":
		for _, p := range intrusion.Protocols {
			fmt.Fprintln(stdout, p)
		}
		return 0
	case "legba-parse":
		return legbaParse(stdin, banner, stdout)
	case "cve-lookup":
		return cveLookup(model, cveFile, stdout)
	case "cve-validate":
		if _, err := intrusion.LoadCVEModels(cveFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitSchema
		}
		return 0
	case "":
		// Modo suelo del esqueleto: config + log stub.
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		logx.New("info").Info(name+": intrusión y entrega (EP-02)", "endpoint", cfg.Endpoint)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "%s: subcomando desconocido %q\n", name, sub)
		return 2
	}
}

func legbaParse(stdin io.Reader, bannerPath string, stdout *os.File) int {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	banner := ""
	if bannerPath != "" {
		b, err := os.ReadFile(bannerPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		banner = string(b)
	}
	hit, err := intrusion.ParseLegbaOutput(raw)
	if err != nil {
		if err == intrusion.ErrNoLegbaHit {
			// Target sano: JSON con protocol null y exit != 0 (T-0201 criterio 2).
			_ = intrusion.WriteVerdict(stdout, intrusion.LegbaVerdict{OK: false})
			return exitNoCred
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	v := intrusion.LegbaVerdict{Protocol: &hit.Protocol, CredIndex: &hit.CredIndex, OK: true}
	if guess, ok := intrusion.GuessModel(banner); ok {
		v.ModelGuess = &guess
	}
	if err := intrusion.WriteVerdict(stdout, v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cveLookup(model, cveFile string, stdout *os.File) int {
	if model == "" {
		fmt.Fprintln(os.Stderr, "cve-lookup: falta --model")
		return 2
	}
	entries, err := intrusion.LoadCVEModels(cveFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitSchema
	}
	if e, ok := intrusion.LookupCVE(entries, model); ok {
		fmt.Printf(`{"model":%q,"cve":%q,"exploit_id":%q,"module":%q,"read_cmd":%q}`+"\n",
			e.Model, e.CVE, e.ExploitID, e.Module, e.ReadCmd)
		return 0
	}
	fmt.Printf(`{"model":%q,"cve":null,"exploit_id":null}`+"\n", model)
	return exitNoCVE
}
