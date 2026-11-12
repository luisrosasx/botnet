// botnet-build — sliver-builder: matriz de 6 archs, manifiesto ed25519
// firmado con raiz fijada en build y conciliacion horaria de release (EP-14).
// Subcomandos: matrix | genroot | sign | verify | print-root |
// smoke-double | reconcile-hourly. Sin flags, conserva el stub con
// --version/--config del suelo.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/cmd/botnet-api/jobs"
	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/logx"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const name = "botnet-build"

func usage() {
	fmt.Fprintln(os.Stderr, "usage: botnet-build [matrix|genroot|sign|verify|print-root|smoke-double|reconcile-hourly] [flags]")
}

func main() {
	verb := ""
	args := os.Args[1:]
	if len(args) > 0 && args[0][0] != '-' {
		verb, args = args[0], args[1:]
	}

	switch verb {
	case "":
		stub(args)
	case "matrix":
		fs := newSet("matrix")
		path := fs.String("profiles", "deploy/sliver-builder/profiles.yaml", "profiles del builder")
		parse(fs, args)
		ps, err := LoadProfiles(*path)
		if err == nil {
			err = ValidateProfiles(ps)
		}
		if err != nil {
			fail(err)
		}
		for _, p := range ps {
			fmt.Printf("profile %s class=%s goos=%s goarch=%s goarm=%s upx=%v max_bytes=%d\n",
				p.Name, p.Class, p.GOOS, p.GOARCH, p.GOARM, p.UPX, p.MaxBytes)
		}
		fmt.Printf("matrix: %d perfiles validos (CGO_ENABLED=0 fijado por el recipe)\n", len(ps))
	case "genroot":
		fs := newSet("genroot")
		keydir := fs.String("keydir", "deploy/signing/keys", "directorio de la raiz")
		parse(fs, args)
		fail(cmdGenroot(*keydir))
	case "sign":
		fs := newSet("sign")
		dist := fs.String("dist", "dist", "directorio con botd-<arch>")
		keydir := fs.String("keydir", "deploy/signing/keys", "directorio de la raiz")
		out := fs.String("out", "dist/manifest.json", "ruta del manifest")
		urlTmpl := fs.String("url", "https://dl.botnet.example/{version}/{file}", "plantilla url")
		ver := fs.String("version", version, "version de la release")
		parse(fs, args)
		fail(cmdSign(*dist, *keydir, *out, *urlTmpl, *ver))
	case "verify":
		fs := newSet("verify")
		manifest := fs.String("manifest", "dist/manifest.json", "manifest a verificar")
		root := fs.String("root", "deploy/signing/keys/root_ed25519.pub", "pubkey raiz fijada")
		dist := fs.String("dist", "", "dist/ para cotejar sha256 (opcional)")
		parse(fs, args)
		fail(cmdVerify(*manifest, *root, *dist))
	case "print-root":
		parse(newSet("print-root"), args)
		fmt.Println(agent.PrintRoot())
	case "smoke-double":
		fs := newSet("smoke-double")
		stub := fs.String("stub", "http://127.0.0.1:18081", "URL del stub_api")
		parse(fs, args)
		fail(cmdSmokeDouble(*stub))
	case "rgpd-check":
		parse(newSet("rgpd-check"), args)
		dsn := os.Getenv("DATABASE_URL")
		if dsn == "" {
			fail(fmt.Errorf("rgpd-check: DATABASE_URL requerido (base de desarrollo con migraciones)"))
		}
		sqlDB, oerr := sql.Open("pgx", dsn)
		if oerr != nil {
			fail(oerr)
		}
		defer sqlDB.Close()
		fail(jobs.RGPDCheck(context.Background(), sqlDB))
	case "reconcile-hourly":
		fs := newSet("reconcile-hourly")
		drift := fs.Float64("drift", 0.4, "desvio objetivo del fixture (%), < tolerancia para puerta verde")
		nodes := fs.Int("nodes", 10, "nodos del fixture de 24 h")
		parse(fs, args)
		red, err := jobs.RunFixture24h(os.Stdout, *drift, *nodes)
		if err == nil && red > 0 {
			err = fmt.Errorf("%d horas en rojo (tol %.2f%%)", red, jobs.TolerancePct)
		}
		fail(err)
	default:
		usage()
		os.Exit(2)
	}
}

func newSet(verb string) *flag.FlagSet {
	return flag.NewFlagSet(verb, flag.ExitOnError)
}

func parse(fs *flag.FlagSet, args []string) {
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, name+":", err)
		os.Exit(1)
	}
}

// stub conserva el comportamiento del suelo: --version y --config.
func stub(args []string) {
	var (
		showVer bool
		cfgPath string
	)
	fs := newSet(name)
	fs.StringVar(&cfgPath, "config", "", "path to JSON config file")
	fs.BoolVar(&showVer, "version", false, "print version and exit")
	parse(fs, args)

	if showVer {
		fmt.Printf("%s %s\n", name, version)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logx.New("info").Info(name+" stub: usa un subcomando (ver usage)", "endpoint", cfg.Endpoint)
}
