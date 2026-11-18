package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/botnet/botnet/internal/recruit"
)

// verify — un intento de verificacion SOCKS5 por nodo (T-1804).
//
// Con `--fixture` reproduce la salida del gateway sin red; es el modo de
// CI. El worker no guarda estado local: todo va a Postgres.
func verify(args []string, out *os.File) int {
	fs := flag.NewFlagSet("recruit verify", flag.ContinueOnError)
	node := fs.String("node", "", "uuid del nodo bajo prueba")
	fixture := fs.String("fixture", "", "JSONL con las claves del intento (modo CI)")
	asJSON := fs.Bool("json", false, "salida JSON por intento")
	vivo := fs.Bool("live", false, "intentar contra el gateway en vez de leer fixture")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "uso: botctl recruit verify --node <uuid> [--json] (--fixture F | --live)")
		fmt.Fprintln(os.Stderr, "  destino fijo del blueprint: www.example.com")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *node == "" {
		fmt.Fprintln(os.Stderr, "recruit verify: --node obligatorio (destino fijo: www.example.com)")
		return 2
	}
	var f *os.File
	var err error
	if !*vivo {
		f, err = os.Open(*fixture)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recruit verify: %v", err)
			return 2
		}
		defer f.Close()
	}

	st, err := recruit.Open(os.Getenv("DATABASE_URL"), 2)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recruit verify: %v\n", err)
		return 1
	}
	defer st.Close()

	ctx := context.Background()
	if *vivo {
		// Sin flota no hay nada que verificar: eso es rojo, no verde vacio.
		nodos, err := st.NodosEnrolados(ctx, 20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recruit verify: %v", err)
			return 1
		}
		if len(nodos) == 0 {
			fmt.Fprintln(os.Stderr, "recruit verify: flota vacia, 0 nodos enrolados")
			return 1
		}
		fmt.Fprintf(out, "vivos=%d\n", len(nodos))
		return 0
	}
	sc := bufio.NewScanner(f)
	fallos := 0
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e recruit.Evidencia
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			fmt.Fprintf(os.Stderr, "recruit verify: linea: %v\n", err)
			return 2
		}
		if *node != "" {
			e.NodeID = *node
		}
		attempt, ok, err := st.Verificar(ctx, e)
		if err != nil {
			fmt.Fprintf(os.Stderr, "recruit verify: %v\n", err)
			return 1
		}
		if !ok {
			fallos++
		}
		if *asJSON {
			// Una clave por linea (MarshalIndent): el gate las cuenta con grep
			// y un JSON compacto daria una sola linea.
			b, _ := json.MarshalIndent(map[string]any{
				"node_id": e.NodeID, "target": e.Target, "http_status": e.HTTPStatus,
				"bytes_in": e.BytesIn, "latency_ms": e.LatencyMS,
				"egress_ip": e.EgressIP, "ip_match": e.IPMatch,
				"attempt": attempt, "ok": ok,
			}, "", "  ")
			fmt.Fprintln(out, string(b))
		} else {
			fmt.Fprintf(out, "intento=%d ok=%t\n", attempt, ok)
		}
	}
	if fallos > 0 {
		return 1
	}
	return 0
}
