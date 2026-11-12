// telemetry_classc.go — T-0707 (cierra A1-09): en clase C (mips,
// 32 MB) la telemetria NO nace: no hay proceso bottelemetry que ver
// nacer y morir cada 5 minutos, ni linea cron nueva, ni endpoint de
// reporting propio. Las metricas (rss_kib, streams, loss_estimate)
// viajan plegadas en el heartbeat, que ya las porta por hbcodec
// (telemetry.go, T-1202). Este fichero fija el contrato medible del
// plegado: las constantes son el invariante y el test lo aserta sin
// need de top ni pgrep.
package agent

// TelemetryProcName es el nombre del proceso que NUNCA debe existir
// en clase C: `ps aux | grep -c '[b]ottelemetry'` = 0 (AC1).
const TelemetryProcName = "bottelemetry"

// TelemetrySpawns es la lista de procesos-hijo que el agente lanza
// para telemetria. Vacia por diseno: un solo binario, un solo proceso
// por perfil (pgrep -cx botd = padre+hijo del watchdog, ni uno mas;
// AC1). La lista es el contrato: si alguien anade un spawn, el test
// de AC1/AC3 rompe.
var TelemetrySpawns = []string{}

// TelemetryCronLines: lineas cron que la telemetria anade al
// inventario del watchdog (T-0505 fija 1 linea; AC2 exige que siga en
// 1). Cero por diseno: la cadencia unica del latido (T-0403) es la
// unica cita del agente.
const TelemetryCronLines = 0

// TelemetryExtraEndpoints: peticiones HTTP de reporting propias,
// aparte del heartbeat. Cero (AC3): rss_kib/streams/loss_estimate
// cabalgan la cadencia unica; ningun capture debe ver requests de
// telemetria sueltos.
const TelemetryExtraEndpoints = 0

// ClassCTelemetryPolicy: el pliego de condiciones de la clase C. Es
// dato, no config: si una epica lo cambia, el test lo grita.
type ClassCTelemetryPolicy struct {
	Spawns         []string // processes que nacen para telemetria
	CronLines      int      // lineas cron anadidas al invariante
	ExtraEndpoints int      // requests de reporting aparte del latido
}

// ClassCPolicy devuelve el pliego vigente (los valores de arriba).
func ClassCPolicy() ClassCTelemetryPolicy {
	return ClassCTelemetryPolicy{
		Spawns:         append([]string{}, TelemetrySpawns...),
		CronLines:      TelemetryCronLines,
		ExtraEndpoints: TelemetryExtraEndpoints,
	}
}

// FoldTelemetry: el plegado real — la instantanea se toma en el
// propio proceso (Collect, telemetry.go) y se serializa en el frame
// del latido (BeatFrame). Cero exec, cero fork: leer /proc y
// codificar. Devuelve el frame listo para el tunel, que es el unico
// transporte (los bytes de telemetria no tienen carril propio).
func FoldTelemetry(ver string, streams, lossEstimateBPS int, nodeID [16]byte, seq, uptimeS uint32) ([]byte, error) {
	t := Collect(ver, streams, lossEstimateBPS)
	return t.BeatFrame(nodeID, seq, uptimeS)
}
