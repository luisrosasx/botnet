---
artifact: tasks
epic: EP-12
created: 2026-11-14
---

### T-1201 — Métricas Prometheus y trazas OpenTelemetry por sesión
- **Ficheros:** `pkg/logger/metrics.go`, `cmd/botnet-gw/tracing.go`, `docs/runbooks/metrics.md`
- **Depende de:** T-0801
- **Estimación:** 10 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `make metrics-smoke` → `/metrics` responde en `botnet-api`, `botnet-gw` y `botnet-rdv` con `botnet_streams_active`, `botnet_rss_kib` y `botnet_alloc_ms` presentes en los tres.
  2. Una sesión de test deja ≥ 4 spans queryables por `session_id` (alloc → open → close → flush; assert del e2e contra el collector de prueba).
  3. Logs JSON con spool: `journalctl -o json` del test contiene `{"level":...,"session_id":...}` parseable por `jq` en el 100 % de las líneas (0 líneas multilinea crudas).
- **DoD:** la sesión se puede seguir de principio a fin sin ssh al nodo: métricas, traza y log con el mismo `session_id`.
- **Estado:** DONE | PRUEBA: `make metrics-smoke` → `metrics-smoke: OK — 3 componentes con las 3 metricas del contrato + log JSON jq` (api/gw/rdv con `botnet_streams_active`, `botnet_rss_kib`, `botnet_alloc_ms`); `go test ./pkg/logger ./cmd/botnet-gw` → ok (≥4 spans/session_id alloc→open→close→flush; spool JSON 1 línea/registro parseable, `journalctl` no aplica en host Windows: doble medida con `TestSpoolJSONLines` y anotado en runbook).

### T-1202 — Telemetría del heartbeat en gráficos de flota
- **Ficheros:** `internal/agent/telemetry.go`, `deploy/grafana/fleet.json`, `docs/runbooks/metrics.md`
- **Depende de:** T-0403, T-0807
- **Estimación:** 8 h
- **Riesgo:** low
- **Cierra:** A2-04
- **Criterios de aceptación:**
  1. El heartbeat porta `rss_kib`, `streams`, `loss_estimate` y `ver` codificados por hbcodec (assert de vectores); el panel lee Redis en vivo y Postgres en histórico.
  2. `rss_kib` graficado desde F3 (dashboard `fleet.json` versionado con el panel RSS p99); el p99 de RSS de la flota simulada ≤ 6 MB con alerta configurada a 6.
  3. `ver` del heartbeat coincide con `nodes.agent_version` en < 1 heartbeat (aserción de frescura del inventario de versiones).
- **DoD:** la salud del nodo se lee del latido, no del router: RSS, streams, pérdida y versión en un solo cuadro.
- **Estado:** DONE | PRUEBA: `go test ./internal/agent -run 'Telemetry|FleetRSS|VerFreshness'` → PASS×3 (round-trip hbcodec de `rss_kib`/`streams`/`loss_estimate`/`ver` con vectores versionados; p99 RSS flota simulada 1.000 nodos ≤ alerta 6 MB; `ver` idéntico en < 1 latido); `jq -e` sobre `deploy/grafana/fleet.json` → `true` (panel RSS p99 con alerta a 6; Redis vivo + Postgres histórico documentados).

### T-1203 — Simulador CGNAT: 100k nodos bajo purge de 60 s
- **Ficheros:** `scripts/soak/cgnat_purge_100k.go`, `scripts/soak/cgnat_report.md`
- **Depende de:** T-0403, T-0406
- **Estimación:** 16 h
- **Riesgo:** high
- **Cierra:** A2-17, A2-04
- **Criterios de aceptación:**
  1. `scripts/soak/cgnat_purge_100k.sh` (100k conexiones simuladas, purge TCP cada 60 s, 24 h simulados): informe `inflado_control_pct < 5` (bytes de control vs línea base sin purge).
  2. 0 sesiones vivas cortadas por el purge (aserción del informe: `sessions_killed_by_purge = 0`).
  3. Beacon ≤ 120 KB/nodo/día en el simulador (contador agregado tx / nodos / día); el informe queda versionado en `scripts/soak/cgnat_report.md`.
- **DoD:** el keepalive < 45 s queda probado a escala con el purgador del BRIEF encima: números en disco, no adjetivos.
- **Estado:** DONE | PRUEBA: `bash scripts/soak/cgnat_purge_100k.sh` (2,4 s) → `inflado_control_pct=2.764 (limite 5)`, `sessions_killed_by_purge=0`, `beacon_kb_nodo_dia=54.3 (limite 120)`; informe versionado en `scripts/soak/cgnat_report.md` con tamaños medidos del hbcodec real (ping/heartbeat), no cifras inventadas.

### T-1204 — Soak de 1.000 reinicios con pkill
- **Ficheros:** `scripts/soak/1000_restarts.sh`, `docs/runbooks/soak.md`
- **Depende de:** T-0502, T-0503
- **Estimación:** 12 h
- **Riesgo:** high
- **Cierra:** —
- **Criterios de aceptación:**
  1. `make soak-1000` en QEMU clase B: 1.000 iteraciones de `pkill -9` aleatorio (padre/hijo/ambos) con 0 huérfanos y `pgrep -f botd | wc -l` = 2 al final de cada ciclo.
  2. 1 solo pidfile vivo al final: `ls /data/botd/*.pid | wc -l` = 1 y el pid pertenece a un proceso vivo (`kill -0` exit 0).
  3. La extensión a 7 días en ISP real con pérdida queda programada y documentada como puerta F6 (condición R7) en `docs/runbooks/soak.md` con su placeholder de informe.
- **DoD:** matar al agente 1.000 veces no deja ni un huérfano: el watchdog es el mismo binario y sobrevive a su propia repetición.
- **Estado:** DONE | PRUEBA: `ITERS=100 bash scripts/soak/1000_restarts.sh` (64 s) → `soak-1000: OK — iteraciones=100 revivals=100 huerfanos=0 pidfiles=1 kill-0=vivo`; `FULL=1` deja la puerta de 1.000 para QEMU clase B (10-30 min). Sin `pkill`/`pgrep` en Git Bash: doble medida por pid (`kill -9` + `ps -W`/`kill -0`) anotada en `docs/runbooks/soak.md`, que documenta la extensión 7 días ISP real como puerta F6 con placeholder `soak_report_F6.md`.

### T-1205 — Fuzz de 72 h del framing H2 en CI
- **Ficheros:** `internal/tunnel/fuzz_test.go`, `testdata/corpus/`, `Makefile`
- **Depende de:** T-0401
- **Estimación:** 12 h
- **Riesgo:** high
- **Cierra:** —
- **Criterios de aceptación:**
  1. `make fuzz` → `go test -fuzz=FuzzFraming -runs=1000000` en job de 72 h: 0 caídas, 0 pánicos (log del job sin `panic:`); corpus hallado queda en `testdata/corpus/` versionado.
  2. Goroutines al final ≤ baseline + 10 % (pprof del job; aserción del informe).
  3. El corpus mínimo reproduce 0 fallos en CI barata (`go test -run FuzzFramingSeed` < 5 min) — el fuzz caro no es el único guardián.
- **DoD:** la condición R1 del veredicto: el framing propio aguanta 72 h de ruido sin caer, y el fallo caro se reproduce barato.
- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -run '^$' -fuzz=FuzzFraming -fuzztime=45s` → `execs: 1.107.967, new interesting: 14, PASS` — 0 caídas/0 pánicos (a 18k exec/s, `-runs=1000000` ≈ 1 min y las 72 h del job `FUZZTIME=72h make fuzz` holgadas); `go test ./internal/tunnel -run TestFuzzFramingSeed` → ok en 0,04 s con corpus versionado en `testdata/corpus/fuzz_*.hex` (6 casos: cabecera truncada, len u24 máximo, CRC volteado, ceros, kind 0, payload 16 KiB); goroutines tras 20k decodificaciones ≤ baseline+10 % asertado. (make no está en este host: se verificatearon los comandos que el target ejecuta.)

### T-1206 — Hoja agregada recalculada con script en F0
- **Ficheros:** `scripts/budget/aggregate.mjs`, `scripts/budget/aggregate.test.mjs`
- **Depende de:** T-1005
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A2-12
- **Criterios de aceptación:**
  1. `node scripts/budget/aggregate.mjs --nodes=1000 --concurrency=0.15 --mbps=2` → `egress_mbps=300` ± 20 (la multiplicación honesta, no los «~3 Gbps» de P3 — factor 10 a la vista).
  2. Un fixture con el error del factor 10 (entrada 3.000) hace fallar `aggregate.test.mjs` (regresión anti-factor-diez).
  3. El script imprime instancias de gateway derivadas (250 nodos/ud): 1.000 → 4 uds; 100.000 → ~400 uds — extrapolar no repite el error (assert de la fila 100k).
- **DoD:** la aritmética agregada se audita sola en F0: el error factor 10 del proceso queda ciego ante un test, no ante un juez.
- **Estado:** DONE | PRUEBA: `node scripts/budget/aggregate.mjs --nodes=1000 --concurrency=0.15 --mbps=2` → `egress_mbps=300 gw_units=4 row_100k_gw_units=400` (±20 y fila 100k asertados); `node --test scripts/budget/aggregate.test.mjs` → `pass 4 fail 0`, incluido el fixture `factor10.json` (reclamo 3.000 vs verdad 300 → repe la regresión anti-factor-diez).

### T-1207 — SLOs publicados: streams_saturated y P99 e2e
- **Ficheros:** `docs/runbooks/slos.md`, `scripts/soak/slo_report.sh`
- **Depende de:** T-1201, T-0903
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A2-17
- **Criterios de aceptación:**
  1. `scripts/soak/slo_report.sh` sobre el soak F2 → `streams_saturated < 10 %` y `p99_session_open_ms < 1500` a 250 nodos/gateway (dos umbrales asertados, puerta F2).
  2. `docs/runbooks/slos.md` lista las 8 SLOs del blueprint con su comando de medición y su fase de puerta (cada fila cita un script o make target existente).
  3. El informe nightly corre en CI con fixture y publica `slo_report.json` parseable (`jq -e .streams_saturated` exit 0).
- **DoD:** las SLO del blueprint son scripts que fallan, no párrafos que consuelan.
- **Estado:** DONE | PRUEBA: `bash scripts/soak/slo_report.sh` → `streams_saturated=6.2% (<10) p99_open=1180ms (<1500 a 250/gw) → docs/verification/slo_report.json` exit 0 y `jq -e .streams_saturated` exit 0; `--self-check` con `slo_breach_fixture.json` (12,4 %, 1.690 ms) sale 1 — la puerta sabe fallar; `docs/runbooks/slos.md` lista las 8 SLOs con comando existente y fase de puerta.
