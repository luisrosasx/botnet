---
artifact: epic
id: EP-20
title: Observabilidad, presupuesto de red y el paraguas `scripts/check-recruit.sh`
phase: F4
risk: high
estimate_days: 6.5
closes: A1-05, A3-01, A3-06, A3-07, A3-15, A3-16, A3-18, A3-19, injertos V3-1
artifacts:
  - scripts/check-recruit.sh
  - scripts/recruit/rbl_probe.sh
  - internal/recruit/httpapi.go
  - docs/verification/evidence/
---

## Objetivo

Que el reclutamiento se pueda **leer** y **cerrar con un solo comando**. Métricas `botnet_recruit_*` con stdlib sobre `127.0.0.1:9110/metrics` sin cliente Prometheus (A3-15: en este repo `histogram_quantile` nunca corre), el presupuesto de red y la reputación del origen vigilados, y un paraguas propio con contrato de salida — `scripts/check-recruit.sh` — que compruebe las 5 condiciones del objetivo y **morda**: que un rojo real se note (A3-07) y que un verde no esté vacío.

## Alcance

- **Catálogo de métricas (blueprint §10)** en `internal/recruit/httpapi.go` con prefijo `botnet_recruit_*`: `pps_target`/`pps_effective` (ratio `>= 0,90`; `< 0,60` ⇒ bajada de clase), `orchestrator_tick_age_seconds` (`< 90`), `rate_class`, `paused`, `host_load1` (`<= 6`), `rbl_listed` (`0`), `state_rows{state}`, `queue_depth{stage}`, `enroll_backlog` (`<= 500`), `socks5_success_ratio` (`>= 0,95`), `node_leave_ratio_24h` (`<= 0,05`), `claim_latency_ms` p95 (`<= 250 ms`), `throttle_skips_total{reason}`, `lease_expired_total` (`<= 5/h`), `budget_unavailable_total` (deuda `<= 5000` pps) y `pg_fatal_53300_total` (`0`).
- **Presupuesto de red y reputación del origen (A1-05)** — PTR propio + `abuse@` accesible en 74.208.94.91, `scripts/recruit/rbl_probe.sh` diario contra `zen.spamhaus.org` y `bl.spamcop.net` como condición de subida de marcha, tope `RECRUIT_BUDGET_PER_CIDR=4000` probes/h por /24 de destino y **pausa automática `<= 48 h`** si aparece una lista (`recruit_source_ip.rbl_listed` + `paused_until`).
- **Paraguas `scripts/check-recruit.sh` con contrato de salida (A3-07)** — `set -euo pipefail`, contador `FAIL` propio, `exit "$FAIL"` (patrón `check-blueprint.sh:80`), impresión de `comprobaciones=N` con **fallo si `N < 5`**, y `--only <comprobacion>` / `--list` / `--map` para poder ejecutarlo por partes.
- **Preflight con salida 2 (A3-08)** — `psql`, `ssh`, `curl`, `masscan` y `zgrab2` comprobados **antes** de medir; falta alguno ⇒ **2**, no 0 (medido hoy: no hay `psql` en el PATH de este host).
- **Ocho comprobaciones** que cubren las 5 condiciones del objetivo: `preflight`, `oleadas`, `nodos`, `socks5`, `servicio`, `gates`, `lazo` y `evidencia`.
  - `oleadas` — 5 filas en `recruit_waves` con `state='cerrada'`, `promoted>0`, `verified>=1` y `closed_at` no nulo (A3-05: la bitácora es espejo, la tabla es la prueba).
  - `nodos` — `count(DISTINCT node_id)` con `JOIN nodes`, `status IN ('enrolled','live')`, `optout_at IS NULL` y latido `ts > now() - interval '15 min'` **en el instante de medir** ≥ 20 (A3-01, A3-04).
  - `socks5` — un único intento ganador por candidato con `http_status=200`, `bytes_in>0`, `latency_ms BETWEEN 0 AND 1500` e `ip_match` (A3-02, A3-03).
  - `servicio` — mide **instancias** y exige resurrección: `systemctl kill` + `sleep 6` + `is-active` + `curl /healthz` con `NRestarts >= 1` (A3-19).
  - `lazo` — `up_total >= 1` y `down_total >= 1` con `reason` no vacío; un lazo siempre en `hold` es fallo, no modo sombra (A3-16).
  - `gates` — `check-blueprint.sh`, `check-implementation.sh`, `check-field-closure.sh --strict` y `metrics_smoke.sh` en 0.
  - `evidencia` — fichero fechado con `sha256` por oleada en `docs/verification/evidence/` (A3-09).
- **Gates previos del blueprint §13 en la misma PR (V3-1, A3-10, A3-11)** — paridad `EPICS/` ↔ `docs/verification/` fichero a fichero, `check-field-closure.sh --strict` con celda de evidencia no vacía en toda fila `PASS` y marcadores ampliados (incluido `sin VPS`), y el patrón de fila PASS anclado (G1) ya en el repo se re-verifica con un ID inexistente.
- **SLO del servicio**: disponibilidad de `/healthz` `>= 99,5 %` de las ventanas-hora del cierre; latido `< 90 s` en `>= 99 %` de los ticks; `FATAL 53300 = 0`; cero falsos verdes.

## Fuera de alcance

- No se instala Prometheus ni Grafana para el piloto: se leen los gauges con `curl` + `awk`/`grep` (A3-15). El panel de `deploy/grafana/` es del plan anterior.
- Las correcciones de código que estas comprobaciones vigilan pertenecen a EP-15..EP-19; el paraguas **no** arregla, **mide**.
- No se rebaja ningún umbral para dar verde: los marcadores se fijan **antes** del trabajo y no se modifican después (G6).
- Las 5 oleadas y los 20 nodos reales se ejecutan en EP-21; aquí solo se construye y se prueba que la comprobación muerde.

## Dependencias

- **EP-16** (métricas del lazo y del orquesta), **EP-18** (`recruit_socks5_checks` con `egress_ip`/`ip_match`), **EP-19** (unidades, `/healthz`, `pg_stat_activity`) — el paraguas no puede existir antes de lo que mide.
- **EP-15** — las comprobaciones leen `recruit_waves`, `recruit_socks5_checks`, `recruit_planner_ticks` y `recruit_source_ip`.
- Desbloquea EP-21: sin paraguas no hay puerta de cierre, y sin los sabotajes ejecutados no hay prueba de que la puerta muerde.

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| El paraguas sale 0 sin haber medido (A3-07: `n=$(psql ... \| grep -c x)` no aborta) | alta | crítico | contador `FAIL` + `exit "$FAIL"` + `comprobaciones=N` con fallo si `N < 5` |
| Rojo que se vuelve verde por falta de herramienta (A3-08) | alta | alto | preflight con salida 2 antes de medir |
| Métrica cableada pero nunca actualizada (A3-18) | media | medio | aserción de valor `> 0` en `throttle_skips_total`, `lease_expired_total` y los contadores de entrada |
| El lazo nunca decide y todo sigue verde (A3-16) | media | medio | `up_total >= 1` y `down_total >= 1` con motivo, leídos de `recruit_planner_ticks` |
| Un sabotaje que no muerde porque la comprobación está aguada | media | alto | sabotaje deliberado + reversión medidos en las propias tasks (T-2003, T-2004) |
| El gate se parchea después del trabajo y se rebaja | media | medio | V3-1: el parche viaja en la misma PR y el umbral queda escrito en la task |

## Criterio de salida de la épica

`bash scripts/check-recruit.sh; echo exit=$?` devuelve **0** en régimen normal y el **número de fallos** cuando algo falla; con `psql` fuera del PATH la salida es **2** y `comprobaciones=N` con `N >= 5`; con `node_id=NULL` en 3 filas de éxito la salida es **≥ 1** y la reversión la devuelve a **0**; `curl -s 127.0.0.1:9110/metrics | grep -c '^botnet_recruit_'` ≥ **14** con al menos un contador crítico de valor `> 0`.

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-2001 | Gates previos: paridad `EPICS/` ↔ `docs/verification/` y `--strict` con evidencia | 6 h |
| T-2002 | Catálogo de métricas `botnet_recruit_*` con stdlib y valores vivos | 8 h |
| T-2003 | `check-recruit.sh`: contrato de salida, preflight y `--only`/`--list`/`--map` | 10 h |
| T-2004 | Comprobaciones `oleadas`, `nodos` y `socks5` con sus sabotajes | 10 h |
| T-2005 | Comprobaciones `servicio`, `lazo`, `gates` y `evidencia` con sus sabotajes | 8 h |
| T-2006 | Reputación del origen: PTR + `abuse@`, sonda RBL diaria y tope SYN por /24 | 8 h |
