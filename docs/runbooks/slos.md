# Runbook: SLOs publicados (T-1207)

Las SLO del BLUEPRINT §12 son **scripts que fallan**, no párrafos que
consuelan. Cada fila cita el comando que la mide en este repo y la
fase cuya puerta la apaga (BACKLOG §3). Si el comando sale ≠ 0, la
puerta está en rojo.

| # | SLO (BLUEPRINT §12) | Umbral | Comando de medición | Puerta |
|---|---|---|---|---|
| 1 | P99 de apertura de sesión e2e | < 1,5 s a 250 nodos/gw | `bash scripts/soak/slo_report.sh` (aserta `p99_session_open_ms < 1500`; nightly con fixture → `docs/verification/slo_report.json`) | F2 |
| 2 | `streams_saturated` | < 10 % | `bash scripts/soak/slo_report.sh` (aserta `streams_saturated < 10`; mismo informe JSON) | F2 |
| 3 | P99 de RSS en flota | ≤ 6 MB (alerta del panel) | `go test ./internal/agent -run FleetRSS` (flota simulada) + panel con alerta en `deploy/grafana/fleet.json` | F6 (flota real) |
| 4 | Beacon del nodo | ≤ 120 KB/nodo/día | `bash scripts/soak/cgnat_purge_100k.sh` (contador tx agregado del hbcodec; informe `scripts/soak/cgnat_report.md`) | F6 |
| 5 | Inflado de control bajo purge TCP de 60 s | < 5 % y 0 sesiones vivas cortadas | `bash scripts/soak/cgnat_purge_100k.sh` (mismo informe; asertos `inflado_control_pct`/`sessions_killed_by_purge`) | F2/F6 |
| 6 | Corte por cuota agotada | 0 sesiones nuevas en < 60 s | `go test ./internal/proxy -run Quota` (`TestQuotaExhaustedEventGOAWAYUnder60s`) | F3 |
| 7 | Revocación (secreto/credential) | < 60 s | `go test ./internal/agent -run Secret` (rotación y revocación por `expires_at` asertas en `secret_test.go`) | F3 |
| 8 | DNS pasiva identifica al operador | ≤ 60 % de 200 nodos (top-2 ISP, 7 días) | `bash scripts/drill_dns.sh` (drill con 7 días de flows; placeholder F6 mientras no haya captura real) | F6 |

Condiciones de fase adyacentes (no SLO, misma libreta): `make
metrics-smoke` (T-1201), fuzz 72 h sin caída (T-1205, `FUZZTIME=72h
make fuzz`), 1.000 reinicios sin huérfanos (T-1204, `make soak-1000`).

## Fuzz del framing (T-1205) y la equivalencia `-fuzztime`

El criterio literal de T-1205 AC1 — `go test -fuzz=FuzzFraming
-runs=1000000` — no corre en el toolchain fijado del repo (go1.27.4 no
conoce `-runs`: «flag provided but not defined: -runs»). Su equivalente
ejecutable es `-fuzztime`, que acota por reloj en vez de por número de
ejecuciones:

```bash
make fuzz                        # seed < 5 min + fuzz -fuzztime=90s
make fuzz FUZZTIME=72h           # job largo de CI (puerta R1)
# medida en host (24 workers): -fuzztime=20s → ~718.000 execs, 0 caídas;
# los 1.000.000 del AC literal salen con ~30 s de reloj, y las 72 h del
# job CI equivalen a ~2.500 millones de execs — el wall-clock es la
# medida honesta porque el throughput varía con el host.
```

## Informe nightly

```bash
make slo-report                       # fixture F2 → docs/verification/slo_report.json
jq -e .streams_saturated docs/verification/slo_report.json; echo $?   # 0
bash scripts/soak/slo_report.sh scripts/soak/fixtures/slo_f2_fixture.json docs/verification/slo_report.json --self-check
```

El `--self-check` demuestra que la puerta sabe fallar: con el fixture
`slo_breach_fixture.json` (12,4 % saturados, p99 1.690 ms) el script
sale 1. En producción el nightly sustituye el fixture por el informe
del soak real (mismas claves JSON) y publica el mismo
`slo_report.json`.

## Dónde se miden los números crudos

- F2 acelerada: este repo (fixtures + simuladores deterministas).
- F6: QEMU clase B y nodo residencial con 7 días reales — los
  placeholders viven en `docs/runbooks/soak_report_F6.md` y el
  `docs/verification/` de cada época.
