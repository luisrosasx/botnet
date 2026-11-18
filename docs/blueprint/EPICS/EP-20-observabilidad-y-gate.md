---
artifact: epic
id: EP-20
title: "Observabilidad, paraguas de comprobaciones y puertas"
owner_layer: recruit
closes: A1-05, A1-14, A2-03, A2-06, A2-10, A3-01, A3-02, A3-03, A3-04, A3-05, A3-06, A3-07, A3-08, A3-09, A3-10, A3-11, A3-15, A3-16, A3-18, A3-19, injerto V3-1, gates G1, G2, G5, G6
estimate_days: 6.25
risk: medium
phase: F7
---

## Objetivo

Que nadie escriba `ok` mirando un número a mano. Dos patas: la señal —el catálogo `botnet_recruit_*` servido con stdlib en `127.0.0.1:9110/metrics`, cableado y vivo, con los umbrales del blueprint §10 legibles con `curl` y `awk`— y la puerta —`scripts/check-recruit.sh`, el paraguas con contrato de salida (`set -euo pipefail`, `exit "$FAIL"`, `comprobaciones=8`, preflight con salida 2) y ocho comprobaciones que muerden, demostradas con sabotaje y su reversión—. Aquí también se remiendan los gates del plan anterior para que aguanten el plan nuevo: paridad `EPICS/` ↔ `docs/verification/`, techo de tasks, `--strict` con evidencia no vacía y el patrón de fila `PASS` anclado.

## Alcance

- `internal/recruit/metrics.go` + `internal/recruit/httpapi.go`: las 17 familias `botnet_recruit_*` del §10 (`pps_target`, `pps_effective`, `orchestrator_tick_age_seconds`, `rate_class`, `paused`, `host_load1`, `rbl_listed`, `state_rows`, `queue_depth`, `enroll_backlog`, `socks5_success_ratio`, `node_leave_ratio_24h`, `claim_latency_ms`, `throttle_skips_total`, `lease_expired_total`, `budget_unavailable_total`, `pg_fatal_53300_total`) y su consumo en `scripts/metrics_smoke.sh`.
- `scripts/check-recruit.sh` (nuevo) con su prueba `scripts/recruit/check_recruit_test.sh`: las 8 comprobaciones `preflight`, `oleadas`, `nodos`, `socks5`, `servicio`, `gates`, `lazo`, `evidencia`, más `--only`, `--list`, `--map` y modo `--fixture`.
- Las tres comprobaciones de datos leen tabla, no bitácora: `oleadas` sobre `recruit_waves`, `nodos` con `count(DISTINCT node_id)` + `JOIN nodes` + `optout_at IS NULL` + latido `node_heartbeats` < 15 min, `socks5` con `bytes_in>0` e `ip_match`.
- Las cuatro de operación: `servicio` (instancias y `NRestarts >= 1` tras `systemctl kill`), `lazo` (exige al menos un `up` y un `down` con `reason`), `gates` (la cadena de puertas existentes) y `evidencia` (ficheros `recruit-wave*-*.log` con `sha256=` en `docs/verification/evidence/`).
- Gates remiendados en la misma PR `[injerto: V3]`: `scripts/check-blueprint.sh` (`-ge 14` épicas, techo 200 tasks y paridad con `docs/verification/`), `scripts/check-field-closure.sh --strict` (marcadores ampliados y celda de evidencia no vacía) y `scripts/check-implementation.sh:85` (fila `PASS` anclada al inicio, A3-06).
- `scripts/recruit/rbl_probe.sh` + `deploy/dns/` + `docs/state/RECRUIT.md`: reputación del origen — PTR identificable con `abuse@`, sonda RBL diaria en `zen.spamhaus.org` y `bl.spamcop.net`, pausa automática ≤ 48 h si aparece una lista y tope `RECRUIT_BUDGET_PER_CIDR=4000` SYN por /24 destino.

Fuera de alcance: el cierre con las 5 oleadas y los 20 nodos (EP-21); la escritura de `public_ip` que consulta la comprobación `nodos` (EP-19); la evidencia que produce el verify (EP-18).

## Cierre

- **T-2001 AC1** — `bash scripts/check-blueprint.sh; echo exit=$?` = **0** con la línea `ok epics=` mostrando **≥ 21**; con `TASK_COUNT=201` forzado la salida es **≠ 0** (techo G2 vigente).
- **T-2001 AC2** — paridad: crear `docs/blueprint/EPICS/EP-99-paridad.md` hace que la puerta salga **≥ 1** citando `EP-99`, y borrarlo la devuelve a **0** (sabotaje + reversión, A3-10).
- **T-2001 AC3** — techo con el plan nuevo: `grep -h '^### T-' docs/blueprint/TASKS/*.md | wc -l` = **102** y el total con el reclutamiento queda **≤ 200** y **≥ 80** (`bash scripts/check-blueprint.sh | grep -oE 'total de tasks=[0-9]+'`).
- **T-2001 AC4** — G6: `bash scripts/check-field-closure.sh --strict; echo exit=$?` = **0** hoy y **1** al borrar una fila `PASS` de `docs/verification/EP-15.md`; `grep -c 'sin VPS' scripts/check-field-closure.sh` = **1** y 0 filas `PASS` con la celda de evidencia vacía (A3-11).
- **T-2001 AC5** — G1 re-mordido: `grep -hE '^\| T-9999 \| AC[0-9]+ \|' docs/verification/EP-13.md | grep -c PASS` = **0** y con `T-1301` = **3** (antes 26 y 26, A3-06).
- **T-2002 AC1** — `curl -s 127.0.0.1:9110/metrics | grep -c '^botnet_recruit_'` ≥ **14** y las 6 familias del §10 presentes en el mismo `grep -cE` ≥ **14**.
- **T-2002 AC2** — cableada, no declarada (A3-18): `curl -s 127.0.0.1:9110/metrics | awk '/^botnet_recruit_(throttle_skips_total|lease_expired_total|budget_unavailable_total)/{s+=$2}END{print (s>0)?1:0}'` = **1** tras un lote con 1 `skipped` y 1 lease vencido.
- **T-2002 AC3** — ratio de ritmo sin PromQL (A3-15): `pps_effective/pps_target` ≥ **0,90** en régimen y, forzando `pps_effective = 0,5 × pps_target`, el siguiente tick da `action='down'`.
- **T-2002 AC4** — umbrales del SLO leídos del mismo sitio: `orchestrator_tick_age_seconds` < **90** en 100 muestras, `enroll_backlog` ≤ **500**, `claim_latency_ms` p95 ≤ **250** y `journalctl -u 'botnet-recruit*' --since -1h | grep -c 53300` = **0**.
- **T-2003 AC1** — `grep -c '^set -euo pipefail' scripts/check-recruit.sh` = **1** y `grep -cE '^exit "\$FAIL"' scripts/check-recruit.sh` = **1**; `bash scripts/check-recruit.sh | grep -oE 'comprobaciones=[0-9]+'` imprime **8** y con `N=4` la salida es **≠ 0** (A3-07).
- **T-2003 AC2** — el `exit` es el número exacto de fallos (medido: **2** con dos comprobaciones rotas); `--list` imprime las **8** comprobaciones y `--map` **5** líneas condición→comprobación.
- **T-2003 AC3** — preflight con **2** (A3-08): con `psql` fuera del PATH, `bash scripts/check-recruit.sh --only preflight; echo exit=$?` = **2** sin ejecutar ninguna otra comprobación (0 líneas `nodos=`).
- **T-2003 AC4** — modo fixture del paraguas (A3-09): `bash scripts/check-recruit.sh --fixture; echo exit=$?` = **0** sin `psql` y sin red; sin el flag la misma situación da **2**.
- **T-2003 AC5** — una comprobación en rojo no impide las demás: con `nodos` saboteada las otras 7 siguen imprimiendo su estado (`grep -c '='` ≥ **7**) y el `exit` es el conteo de fallos.
- **T-2004 AC1** — `oleadas` lee la tabla: `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND promoted>0 AND verified>=1 AND closed_at IS NOT NULL"` = **5** con `--only oleadas` en **0**; una oleada menos da salida **≥ 1** y revertida vuelve a **0** (A3-05).
- **T-2004 AC2** — `nodos` con `count(DISTINCT node_id)`: la consulta incluye `JOIN nodes`, `n.status IN ('enrolled','live')`, `n.optout_at IS NULL` y `node_heartbeats.ts > now() - interval '15 min'`; con 20 nodos da **≥ 20** y `exit=0` (A3-01, A3-04).
- **T-2004 AC3** — sabotaje 1 (nodo fantasma): `node_id=NULL` en 3 filas `verified` ⇒ la comprobación imprime `distinct_node_id=17` y sale **≥ 1**; revertida sale **0**.
- **T-2004 AC4** — sabotaje 2 (cuenta por intentos): 5 filas `ok=true` del mismo `candidate_id` dejan el conteo en **20**, no en **24** (A3-02).
- **T-2004 AC5** — sabotaje 3 (SOCKS5 sin flota): flota vacía y verify contra el gw vivo ⇒ `bash scripts/check-recruit.sh --only socks5; echo exit=$?` = **≥ 1** con `bytes_in=0` en las 20 filas.
- **T-2005 AC1** — `servicio`: `systemctl kill --signal=TERM botnet-recruit; sleep 6` ⇒ `active`, `NRestarts` ≥ **1**, `/healthz` = **200** en < **10 s** y **3** líneas `active` en `'botnet-recruit-worker@*'` con 0 `failed` (A2-06, A3-19).
- **T-2005 AC2** — `lazo`: `count(*)` de `recruit_planner_ticks` con `action='up'` ≥ **1**, con `'down'` ≥ **1** y con `reason` vacío = **0**; un lazo siempre en `hold` hace fallar la comprobación con salida **≥ 1** (A3-16).
- **T-2005 AC3** — `gates`: `bash scripts/check-blueprint.sh && bash scripts/check-implementation.sh && bash scripts/check-field-closure.sh --strict && bash scripts/metrics_smoke.sh; echo exit=$?` = **0**, y `--only gates` sale **≥ 1** si se fuerza uno a fallar.
- **T-2005 AC4** — `evidencia`: `ls docs/verification/evidence/ | grep -cE 'recruit-wave[0-9]+-[0-9]{8}-[0-9]{6}\.log'` = **5** con `sha256=` en `head -3` de cada uno (5/5); borrar uno da salida **≥ 1** (A3-09).
- **T-2005 AC5** — reversión global: tras los cuatro sabotajes, `bash scripts/check-recruit.sh; echo exit=$?` vuelve a **0** sin tocar el guion (`git diff --quiet scripts/check-recruit.sh` = **0**).
- **T-2006 AC1** — `bash scripts/recruit/rbl_probe.sh --ip 74.208.94.91; echo exit=$?` = **0** con `dig +short 94.94.208.74.zen.spamhaus.org` y `…bl.spamcop.net` vacíos 7 días, y `recruit_source_ip` con `NOT rbl_listed` y `rbl_checked_at` < 1 día = **1** (A1-05).
- **T-2006 AC2** — lista encontrada ⇒ pausa automática ≤ **48 h**: en ≤ 60 s `rbl_listed` = **t**, `recruit_control.paused` = **t** y `paused_until > now() + interval '47 hour'` = **t**.
- **T-2006 AC3** — PTR identificable y `abuse@` vivo: `dig -x 74.208.94.91 +short` no vacío y distinto del PTR genérico previo (`grep -c "$PTR_ANTES"` = **0**), con 1 entrega y 0 rebote.
- **T-2006 AC4** — tope de SYN por /24 destino: con `RECRUIT_BUDGET_PER_CIDR=4000`, `count(*)` de jobs `sweep` por /24 y hora ≤ **4000** (0 horas por encima).
- **T-2006 AC5** — caudal real dentro del techo: a `rate_class=4` (5000 pps), `tcpdump -eni eth0 'tcp[tcpflags] & tcp-syn != 0' -c 15000` tarda entre **3** y **20 s** (700–5.000 pps reales; los 2,32 Mbps del blueprint se comprueban, no se citan).
