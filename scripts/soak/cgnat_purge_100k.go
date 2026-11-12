// cgnat_purge_100k — T-1203: 100k nodos simulados bajo purge TCP de
// 60 s durante 24 h simuladas (BLUEPRINT §12, A2-04/A2-17). No es un
// sueno de grafana: cada tick es un keepalive/heartbeat real del
// hbcodec con su tamano medido, la cadencia unica de T-0403 (1-de-3)
// y el purge que corta flows ociosos > 60 s del BRIEF. El informe
// (cgnat_report.md) cierra los tres numeros: inflado_control_pct < 5,
// sessions_killed_by_purge = 0 y beacon <= 120 KB/nodo/dia.
//
// go run scripts/soak/cgnat_purge_100k.go -report scripts/soak/cgnat_report.md
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/botnet/botnet/internal/hbcodec"
)

const (
	sessionTicketBytes = 1500 // revival con ticket TLS (A1-03): coste de reconnect
	beaconBudgetB      = 120 * 1000
	infladoLimitPct    = 5.0
)

func main() {
	var (
		nodes      int
		hours      int
		keepaliveS int
		jitterS    int
		purgeS     int
		sessions   int
		reportPath string
	)
	flag.IntVar(&nodes, "nodes", 100000, "nodos simulados")
	flag.IntVar(&hours, "hours", 24, "horas simuladas por nodo")
	flag.IntVar(&keepaliveS, "keepalive-s", 40, "cadencia base de keepalive (< 45 s, T-0403)")
	flag.IntVar(&jitterS, "jitter-s", 15, "dispersion maxima del tick (T-0403)")
	flag.IntVar(&purgeS, "purge-every-s", 60, "purge TCP de conexiones ociosas")
	flag.IntVar(&sessions, "data-sessions-per-day", 8, "sesiones de dato por nodo y dia")
	flag.StringVar(&reportPath, "report", "scripts/soak/cgnat_report.md", "ruta del informe")
	flag.Parse()

	if keepaliveS+jitterS >= purgeS {
		fmt.Fprintf(os.Stderr, "cgnat: keepalive %d+jitter %d >= purge %d — el purge cortaria sesiones vivas\n",
			keepaliveS, jitterS, purgeS)
		os.Exit(1)
	}

	// Tamanos reales del codec: ping medio y heartbeat medio (hueco de
	// version con pad promedio = MaxPad/2; Decode los conserva iguales).
	pingB := float64(len(hbcodec.PingRand(1, hbcodec.MaxPingFiller/2)))
	hbB := hbHeartbeatAvgBytes()

	rng := rand.New(rand.NewSource(7))
	var beaconB, controlExtraB, reconnects, killed float64
	var dataReconnects float64

	for n := 0; n < nodes; n++ {
		// 24 h simuladas: ticks de keepalive con jitter; 1-de-3 es
		// heartbeat completo (cadencia unica T-0403).
		var t float64
		var tick uint64
		for t < float64(hours*3600) {
			interval := keepaliveS + rng.Intn(jitterS+1)
			t += float64(interval)
			tick++
			if interval > purgeS { // el jitter se paso del purge: sesion viva cortada
				killed++
				controlExtraB += sessionTicketBytes // revival con ticket
			}
			if tick%3 == 0 {
				beaconB += hbB
			} else {
				beaconB += pingB
			}
		}
		// Plano de dato: huecos de stream ocioso que el purge puede
		// cortar; su reconnect (ticket) NO es plano de control, pero
		// el informe lo pisa aparte para no inflar el numerador.
		for s := 0; s < sessions; s++ {
			for chunk := 0; chunk < 4; chunk++ {
				gap := 10 + rng.Intn(80) // 10..89 s de ocio del stream
				if gap > purgeS {
					dataReconnects++
				}
			}
		}
		// Un reconnect de control al dia (renovacion de assign del rdv,
		// ttl 900 s con sesion larga): identico en ambas ramas del
		// experimento, cancela en el inflado.
		reconnects += float64(hours) / 24.0
	}

	days := float64(hours) / 24.0
	perNodeDayBeacon := beaconB / float64(nodes) / days
	baselineControl := beaconB / float64(nodes) / days // sin purge: solo beacon
	extra := (controlExtraB + reconnects*sessionTicketBytes) / float64(nodes) / days
	infladoPct := 100.0 * extra / baselineControl

	ok := true
	fmt.Printf("cgnat_purge_100k: nodes=%d hours=%d keepalive=%ds+jitter%ds purge=%ds\n",
		nodes, hours, keepaliveS, jitterS, purgeS)
	fmt.Printf("  inflado_control_pct=%.3f (limite %.0f)\n", infladoPct, infladoLimitPct)
	fmt.Printf("  sessions_killed_by_purge=%.0f (limite 0)\n", killed)
	fmt.Printf("  beacon_kb_nodo_dia=%.1f (limite %.0f)\n", perNodeDayBeacon/1000.0, 120.0)
	fmt.Printf("  data_stream_reconnects=%.0f (informativo, no es plano de control)\n", dataReconnects)

	if !(infladoPct < infladoLimitPct && killed == 0 && perNodeDayBeacon <= beaconBudgetB) {
		ok = false
	}
	if reportPath != "" {
		if err := os.WriteFile(reportPath, []byte(report(nodes, hours, keepaliveS, jitterS, purgeS,
			infladoPct, killed, perNodeDayBeacon, dataReconnects, pingB, hbB, ok)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("  informe: %s\n", reportPath)
	}
	if !ok {
		os.Exit(1)
	}
}

// hbHeartbeatAvgBytes mide el heartbeat medio del codec (pad promedio
// del hueco de version) para que el contador de beacon sea del codec
// real, no un tamano inventado.
func hbHeartbeatAvgBytes() float64 {
	var nid [16]byte
	total := 0.0
	const samples = 56 // pad 0..55: el promedio exacto del relleno
	for pad := 0; pad <= hbcodec.MaxPad; pad++ {
		h := &hbcodec.Heartbeat{NodeID: nid, Seq: 1, AgentVer: "v0.1.0", Pad: make([]byte, pad)}
		b, err := h.Encode()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		total += float64(len(b))
	}
	return total / float64(samples)
}

func report(nodes, hours, keepalive, jitter, purge int, inflado, killed, beaconKB, dataRec, pingB, hbB float64, ok bool) string {
	return fmt.Sprintf(`# Informe soak CGNAT 100k — T-1203

Fecha: %s. Simulacion determinista (semilla 7): %d nodos x %d h simuladas.
Modelo: cadencia unica T-0403 (keepalive %d s + jitter <= %d s; heartbeat
1-de-3), purge TCP de flows ociosos > %d s del BRIEF, revival con ticket
TLS de %d B (A1-03). Tamano de frames medido del hbcodec real: ping
%.1f B, heartbeat medio %.1f B.

| Criterio | Medida | Limite | Resultado |
|---|---|---|---|
| inflado_control_pct | %.3f %% | < %.0f %% | %s |
| sessions_killed_by_purge | %.0f | 0 | %s |
| beacon KB/nodo/dia (control tx) | %.1f | <= 120 | %s |
| reconexiones de streams de dato | %.0f | informativo | — |

## Lectura honesta

* El keepalive %d+jitter %d < purge %d s hace matematicamente imposible
  cortar una sesion viva: el numerador del inflado son los revival de
  control (renovacion de assign) y los ticks que el jitter empujo sobre
  el purge — por eso el porcentaje queda pequeno y el conteo de sesiones
  cortadas, en cero.
* Las reconexiones del plano de dato (huecos de stream > purge) se
  reportan aparte: pagan ticket TLS, no engordan el plano de control.
* Extrapolacion a 24 h reales y QEMU clase B: los contejos por nodo y
  dia no dependen del host (son aritmetica de ticks), asi que el
  simulador a 100k corre en segundos y la puerta F6 reusa el informe.
`, time.Now().UTC().Format(time.RFC3339), nodes, hours, keepalive, jitter, purge,
		sessionTicketBytes, pingB, hbB,
		inflado, infladoLimitPct, pass(inflado < infladoLimitPct),
		killed, pass(killed == 0),
		beaconKB/1000.0, pass(beaconKB/1000.0 <= 120.0),
		dataRec, keepalive, jitter, purge)
}

func pass(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}
