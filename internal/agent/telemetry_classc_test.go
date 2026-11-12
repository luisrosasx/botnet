// telemetry_classc_test.go — T-0707 con dobles: el pliego de la clase
// C (cero spawns, cero lineas cron, cero endpoints propios) y el
// plegado real: las metricas van dentro del frame del heartbeat y
// alli se leen al decodificar. No hace falta ni ps ni pgrep: el
// contrato del proceso unico es dato y el test lo aserta; pgrep -cx
// botd = 2 (padre+hijo del watchdog) sigue siendo el invariante
// porque TelemetrySpawns esta vacia.
package agent

import (
	"testing"

	"github.com/botnet/botnet/internal/hbcodec"
)

func TestClassCPlegadoCeroHuella(t *testing.T) {
	p := ClassCPolicy()
	// AC1: ningun proceso nace y muere cada 5 min (ps '[b]ottelemetry' = 0;
	// pgrep -cx botd = 2 sigue siendo solo supervisor+agente).
	if len(p.Spawns) != 0 {
		t.Errorf("spawns de telemetria = %v, want ninguna lista (0)", p.Spawns)
	}
	// AC2: la linea cron del watchdog (T-0505) sigue sola (crontab -l = 1).
	if p.CronLines != 0 {
		t.Errorf("lineas cron anadidas = %d, want 0 (invariante de 1 intacto)", p.CronLines)
	}
	// AC3: 0 endpoints extra de reporting.
	if p.ExtraEndpoints != 0 {
		t.Errorf("endpoints de telemetria propios = %d, want 0", p.ExtraEndpoints)
	}
	if TelemetryProcName != "bottelemetry" {
		t.Errorf("el proceso fantasma debe llamarse bottelemetry, hay %q", TelemetryProcName)
	}
	// ClassCPolicy devuelve copia: nadie muta el contrato por fuera.
	p.Spawns = append(p.Spawns, "invento")
	if len(ClassCPolicy().Spawns) != 0 {
		t.Error("ClassCPolicy debe devolver copia del contrato")
	}
}

func TestTelemetryViajaEnElHeartbeat(t *testing.T) {
	// AC3 medido: las metricas van en la cadencia unica y solo en
	// ella. El doble: un latido con rss/streams/loss -> frame ->
	// decodificado, y ni una peticion mas (ExtraEndpoints = 0).
	nodeID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	frame, err := FoldTelemetry("0.7.0", 3, 120, nodeID, 42, 86400)
	if err != nil {
		t.Fatalf("FoldTelemetry: %v", err)
	}
	var hb hbcodec.Heartbeat
	if err := hb.Decode(frame); err != nil {
		t.Fatalf("Decode del latido plegado: %v", err)
	}
	if hb.NodeID != nodeID || hb.Seq != 42 || hb.UptimeS != 86400 {
		t.Errorf("cabecera del latido: %+v", hb)
	}
	if hb.Streams != 3 || hb.LossEstimate != 120 {
		t.Errorf("streams/loss plegados: %d/%d, want 3/120", hb.Streams, hb.LossEstimate)
	}
	if hb.RssKiB == 0 {
		t.Error("rss_kib debe viajar en el latido (muestra del proceso propio)")
	}
	if TelemetryExtraEndpoints != 0 {
		t.Error("la telemetria abrio endpoints propios: capture veria requests sueltos")
	}
}
