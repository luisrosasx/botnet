# Runbook: métricas, trazas y logs (T-1201, T-1202)

## /metrics — las tres métricas del contrato

Cada componente del plano de control sirve `/metrics` en texto Prometheus
(version 0.0.4, stdlib puro — el binario no paga el SDK oficial):

| Componente | Flag | Métricas del contrato |
|---|---|---|
| `botnet-api` | `-metrics-addr 127.0.0.1:19181` | `botnet_streams_active`, `botnet_rss_kib`, `botnet_alloc_ms` |
| `botnet-gw` | `-metrics-addr 127.0.0.1:19180` | idem |
| `botnet-rdv` | `-metrics-addr 127.0.0.1:19182` | idem |
| nodo (`botd`) | sin scrape (quien nunca escucha) | beacon por túnel: `internal/hbcodec` |

`botnet_rss_kib` se muestrea en el scrape: en Linux es `VmRSS` de procfs
(exacto); en hosts sin procfs (Windows/macOS del CI) es la estimación
`HeapInuse+StackSys+MSpanSys+MCacheSys+GCSys+OtherSys` de
`runtime.MemStats`. La doble fuente está asumida por el panel: el SLO de
p99 de RSS se valida contra la flota real en Linux (F6), no contra el
host del test.

Prueba: `make metrics-smoke` (script `scripts/metrics_smoke.sh`) —
levanta api, gw y rdv, exige las tres métricas en los tres endpoints y
una línea JSON de spool parseable por `jq`.

## Trazas de sesión (gw)

El gateway guarda los cuatro spans de cada sesión — `alloc → open →
close → flush` — bajo el mismo `session_id`
(`cmd/botnet-gw/tracing.go`). Consulta sin ssh al nodo:

```
curl 'http://gw:PORT/trace?session_id=sess-042'   # >= 4 spans en JSON
```

El collector de prueba (`TestTraceSessionFourSpans`) aserta los cuatro
spans por sesión; el export OTLP real queda para la hoja F3 (el SDK de
OpenTelemetry no entra aún en el presupuesto del binario).

## Logs JSON con spool

`pkg/logger` emite una línea = un objeto JSON (nivel, `session_id`,
ts, msg, extra) y mantiene el spool en disco con rotación por tamaño
(una copia `.1`). En Linux, `journalctl -o json` debe poder `jq`-ear el
100 % de las líneas: 0 líneas multilínea crudas (el `\n` del mensaje
escapa por `encoding/json`). En el host Windows del CI la aserción vive
en `TestSpoolJSONLines` (doble medida anotada).

## Telemetría del heartbeat (T-1202)

El latido porta por `hbcodec` (binario, ≤106 B + relleno anti-DPI):
`rss_kib`, `streams`, `loss_estimate` (puntos básicos) y `ver`. El panel
lee Redis en vivo y Postgres en histórico; el dashboard
`deploy/grafana/fleet.json` grafica el p99 de RSS de la flota con alerta
configurada a **6 MB** (Blueprint §12). Asertos:

- vectores del codec: round-trip exacto de los cuatro campos
  (`testdata/codec_vectors/`, `internal/hbcodec/codec_test.go`);
- p99 de RSS de flota simulada ≤ 6 MB (`internal/agent`
  `TestFleetRSSP99UnderAlert`);
- frescura: el `ver` del heartbeat coincide con el del nodo en < 1
  latido (`TestVerFreshnessUnderOneBeat`).

## Panel de flota

`deploy/grafana/fleet.json` — paneles: RSS p99 (alerta 6 MiB), streams
activos, pérdida estimada e inventario de versiones. Fuentes: Redis
(vivo) y Postgres (histórico); el nodo no expone puerto de scraping.
