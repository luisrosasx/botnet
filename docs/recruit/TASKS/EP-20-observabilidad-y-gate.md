---
artifact: tasks
epic: EP-20
created: 2026-11-17
---

### T-2001 — Gates previos: paridad `EPICS/` ↔ `docs/verification/` y `--strict` con evidencia
- **Épica:** EP-20
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `bash scripts/check-blueprint.sh; echo exit=$?` = **0** y la línea `ok epics=` muestra **≥ 21** épicas (`grep -c 'ok epics='` = **1**); el techo sigue mordiendo: con `TASK_COUNT` forzado a 201 la salida es **≠ 0** (hoy `-ge 14` y techo 200, G2 ya parcheado).
  2. Paridad nueva: `printf -- '---\nid: EP-99\n---\n' > docs/blueprint/EPICS/EP-99-paridad.md && bash scripts/check-blueprint.sh; echo exit=$?` = **≥ 1** con `EP-99` citado en la salida (`grep -c 'EP-99'` ≥ **1**), y `rm docs/blueprint/EPICS/EP-99-paridad.md && bash scripts/check-blueprint.sh; echo exit=$?` = **0** (sabotaje + reversión) — A3-10.
  3. Techo de tasks con el plan nuevo: `grep -h '^### T-' docs/blueprint/TASKS/*.md | wc -l | tr -d ' '` = **102** y, sumadas las del reclutamiento, el total queda **≤ 200** y **≥ 80** (`bash scripts/check-blueprint.sh | grep -oE 'total de tasks=[0-9]+'` dentro de rango).
  4. G6: `bash scripts/check-field-closure.sh --strict; echo exit=$?` = **0** hoy, y = **1** al borrar una fila `PASS` de `docs/verification/EP-15.md` (sabotaje + reversión); el marcador `sin VPS` está ampliado: `grep -c 'sin VPS' scripts/check-field-closure.sh` = **1** y ninguna fila `PASS` queda con la celda de evidencia vacía (`awk` de celdas vacías en filas `PASS` = **0**) — A3-11.
  5. G1 re-mordido con ID inexistente: `grep -hE '^\| T-9999 \| AC[0-9]+ \|' docs/verification/EP-13.md | grep -c PASS` = **0** y con `T-1301` = **3** (antes **26** y **26**) — A3-06.
- **Depende de:** — (puede abrir el día 1; no necesita código)
- **Estimación:** 6 h
- **DoD:** los gates existentes no se rompen al crecer el plan y ya no dan por cerrado lo que no conocen; el parche viaja en la misma PR que las épicas nuevas `[injerto: V3]`.
- **Ficheros:** `scripts/check-blueprint.sh`, `scripts/check-field-closure.sh`, `docs/verification/EP-15.md`
- **Cierra:** V3-1, A3-06, A3-10, A3-11, gates G1, G2, G6

### T-2002 — Catálogo de métricas `botnet_recruit_*` con stdlib y valores vivos
- **Épica:** EP-20
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `curl -s 127.0.0.1:9110/metrics | grep -c '^botnet_recruit_'` ≥ **14** y las 6 familias del §10 están: `curl -s 127.0.0.1:9110/metrics | grep -cE '^botnet_recruit_(pps_target|pps_effective|orchestrator_tick_age_seconds|rate_class|paused|host_load1|rbl_listed|state_rows|queue_depth|enroll_backlog|socks5_success_ratio|node_leave_ratio_24h|claim_latency_ms|throttle_skips_total|lease_expired_total|budget_unavailable_total|pg_fatal_53300_total)'` ≥ **14**.
  2. Cableada, no declarada (A3-18): `curl -s 127.0.0.1:9110/metrics | awk '/^botnet_recruit_(throttle_skips_total|lease_expired_total|budget_unavailable_total)/{s+=$2}END{print (s>0)?1:0}'` = **1** tras correr un lote con 1 `skipped` y 1 lease vencido (hoy: 13 métricas pueden ser 3 escritas).
  3. Ratio de ritmo sin PromQL (A3-15): `curl -s 127.0.0.1:9110/metrics | awk '/^botnet_recruit_pps_target/{t=$2}/^botnet_recruit_pps_effective/{e=$2}END{printf "%.2f", e/t}'` ≥ **0,90** en régimen estable, y al forzar `pps_effective = 0,5 × pps_target` el siguiente tick da `action='down'` (`psql -tAc "SELECT action FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 1"` = `down`).
  4. Umbrales del SLO leídos del mismo sitio: `orchestrator_tick_age_seconds` < **90** en 100 muestras seguidas (0 lecturas ≥ 90), `enroll_backlog` ≤ **500**, `claim_latency_ms` p95 ≤ **250** y `journalctl -u 'botnet-recruit*' --since -1h | grep -c 53300` = **0**.
- **Depende de:** T-1601, T-1607
- **Estimación:** 8 h
- **DoD:** los umbrales del blueprint §10 se leen con `curl` y `awk` sobre el propio servicio; nadie escribe `ok` mirando un número a mano.
- **Ficheros:** `internal/recruit/httpapi.go`, `internal/recruit/metrics.go`, `scripts/metrics_smoke.sh`
- **Cierra:** A3-15, A3-18, A2-03 (señal de ritmo)

### T-2003 — `scripts/check-recruit.sh`: contrato de salida, preflight y `--only`/`--list`/`--map`
- **Épica:** EP-20
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `grep -c '^set -euo pipefail' scripts/check-recruit.sh` = **1** y `grep -cE '^exit "\$FAIL"' scripts/check-recruit.sh` = **1**; la última línea estándar es `comprobaciones=N` con `N >= 5`: `bash scripts/check-recruit.sh | grep -oE 'comprobaciones=[0-9]+'` imprime **8** y una versión con `N=4` devuelve salida **≠ 0** (A3-07).
  2. `bash scripts/check-recruit.sh; echo exit=$?` devuelve **0** sin fallos y el **número exacto de fallos** con dos comprobaciones rotas a la vez (medido: `2`), no solo «rojo»; `--list` imprime las **8** comprobaciones (`preflight oleadas nodos socks5 servicio gates lazo evidencia`) y `--map` imprime **5** líneas condicion→comprobación (las 5 condiciones del objetivo).
  3. Preflight con **2** (A3-08): con `psql` fuera del PATH, `bash scripts/check-recruit.sh --only preflight; echo exit=$?` = **2** y no se ejecuta ninguna otra comprobación (0 líneas `nodos=` en la salida); hoy el paraguas no existe y `command -v psql || echo NO` da `NO`.
  4. Modo fixture del paraguas (A3-09): `bash scripts/check-recruit.sh --fixture; echo exit=$?` = **0** sin `psql` en el PATH y sin red (0 conexiones salientes medida con `ss`), mientras que sin el flag la misma situación da **2**.
  5. Una comprobación en rojo noimpa las demás: con `nodos` saboteada, las otras **7** siguen imprimiendo su estado (`grep -c '='` en la salida ≥ **7**) y el `exit` es el conteo de fallos, no un `1` prestado.
- **Depende de:** T-2002, T-1905
- **Estimación:** 10 h
- **DoD:** existe el paraguas, sale con el número de fallos, distingue «falta herramienta» (2) de «hay fallo» (≥1) y se puede correr por partes.
- **Ficheros:** `scripts/check-recruit.sh`, `scripts/recruit/check_recruit_test.sh`
- **Cierra:** A3-07, A3-08, gate G5 (preflight integrado)

### T-2004 — Comprobaciones `oleadas`, `nodos` y `socks5` con sus sabotajes
- **Épica:** EP-20
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `oleadas`: la comprobación lee la tabla, no la bitácora — `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND promoted>0 AND verified>=1 AND closed_at IS NOT NULL"` = **5** y `bash scripts/check-recruit.sh --only oleadas; echo exit=$?` = **0**; con una oleada menos (UPDATE de prueba) la salida es **≥ 1** y al revertir vuelve a **0** (sabotaje + reversión; hoy `check-blueprint.sh` sale 0 antes, durante y después de reclutar nada — A3-05).
  2. `nodos` con `count(DISTINCT node_id)`: la consulta de la comprobación incluye `JOIN nodes`, `n.status IN ('enrolled','live')`, `n.optout_at IS NULL` y el latido de `node_heartbeats` con `ts > now() - interval '15 min'`; con los 20 nodos puestos da **≥ 20** y `exit=0` (A3-01, A3-04).
  3. Sabotaje 1 (nodo fantasma): `psql -c "UPDATE recruit_candidates SET node_id=NULL WHERE candidate_id IN (SELECT candidate_id FROM recruit_candidates WHERE state='verified' LIMIT 3)"` ⇒ la comprobación imprime `distinct_node_id=17` y sale **≥ 1**; revertido, sale **0** (hoy sale 0 porque `count(*)` no mira el `node_id`).
  4. Sabotaje 2 (cuenta por intentos): insertar **5** filas `ok=true` del mismo `candidate_id` (violando `recruit_socks5_one_win` solo tras deshabilitarlo a propósito en la BD de pruebas) deja el conteo de nodos en **20**, no en **24** — si sube, la puerta cuenta intentos y no aparatos (A3-02).
  5. Sabotaje 3 (SOCKS5 sin flota): flota vacía y verify contra el gw vivo ⇒ `bash scripts/check-recruit.sh --only socks5; echo exit=$?` = **≥ 1** con `bytes_in=0` en las 20 filas registrado (hoy sale 0).
- **Depende de:** T-2003, T-2101, T-2102
- **Estimación:** 10 h
- **DoD:** las tres comprobaciones de datos muerden: se demuestra con un sabotaje deliberado y su reversión, medido dos veces con el mismo comando.
- **Ficheros:** `scripts/check-recruit.sh`, `docs/verification/EP-15.md`
- **Cierra:** A3-01, A3-02, A3-03 (conteo), A3-04, A3-05 (comprobación)

### T-2005 — Comprobaciones `servicio`, `lazo`, `gates` y `evidencia` con sus sabotajes
- **Épica:** EP-20
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `servicio`: mide instancias y exige resurrección — `systemctl kill --signal=TERM botnet-recruit; sleep 6; systemctl is-active botnet-recruit` = `active`, `systemctl show -p NRestarts --value botnet-recruit` ≥ **1**, `curl -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **200** en < **10 s**, y `'botnet-recruit-worker@*'` devuelve **3** líneas `active` con 0 en `failed` (A2-06, A3-19).
  2. `lazo`: `psql -tAc "SELECT count(*) FROM recruit_planner_ticks WHERE action='up'"` ≥ **1**, `... action='down'` ≥ **1**, y `count(*)` con `reason IS NULL OR reason=''` = **0**; un lazo siempre en `hold` (fixture) hace fallar la comprobación con salida **≥ 1** (A3-16 — hoy un lazo que nunca decide sale verde).
  3. `gates`: `bash scripts/check-blueprint.sh && bash scripts/check-implementation.sh && bash scripts/check-field-closure.sh --strict && bash scripts/metrics_smoke.sh; echo exit=$?` = **0**, y `bash scripts/check-recruit.sh --only gates; echo exit=$?` = **≥ 1** si se fuerza uno a fallar (sabotaje + reversión).
  4. `evidencia`: `ls docs/verification/evidence/ | grep -cE 'recruit-wave[0-9]+-[0-9]{8}-[0-9]{6}\.log'` = **5** y `head -3` de cada uno contiene `sha256=` (5/5); borrar uno ⇒ salida **≥ 1**, restaurarlo ⇒ **0** (A3-09).
  5. Reversión global: tras los cuatro sabotajes anteriores, `bash scripts/check-recruit.sh; echo exit=$?` vuelve a **0** sin tocar el guion (`git diff --quiet scripts/check-recruit.sh` sale **0**) — el sabotaje no deja rastro.
- **Depende de:** T-2003, T-1903, T-1607, T-2103
- **Estimación:** 8 h
- **DoD:** las cuatro comprobaciones de operación también muerden, y todo el paraguas se prueba con sabotaje + reversión, no con una ejecución optimista.
- **Ficheros:** `scripts/check-recruit.sh`, `docs/verification/evidence/`
- **Cierra:** A2-06 (comprobación), A3-09, A3-16, A3-19

### T-2006 — Reputación del origen: PTR + `abuse@`, sonda RBL diaria y tope SYN por /24
- **Épica:** EP-20
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `bash scripts/recruit/rbl_probe.sh --ip 74.208.94.91; echo exit=$?` = **0** con salida vacía en las dos listas: `for d in zen.spamhaus.org bl.spamcop.net; do dig +short 94.94.208.74.$d; done` vacío **7 días consecutivos**, y `psql -tAc "SELECT count(*) FROM recruit_source_ip WHERE ip='74.208.94.91' AND NOT rbl_listed AND rbl_checked_at > now()-interval '1 day'"` = **1** (A1-05).
  2. Lista encontrada ⇒ pausa automática ≤ **48 h**: con el fixture que hace responder a `zen.spamhaus.org`, en ≤ **60 s** `psql -tAc "SELECT rbl_listed FROM recruit_source_ip WHERE ip='74.208.94.91'"` = **t** y `psql -tAc "SELECT paused FROM recruit_control"` = **t** con `psql -tAc "SELECT paused_until > now() + interval '47 hour' FROM recruit_source_ip"` = **t** (1/1).
  3. PTR identificable y abuse vivo: `dig -x 74.208.94.91 +short` no vacío y **no** coincide con el PTR genérico del proveedor medido antes de la épica (`grep -c "$PTR_ANTES" <(dig -x 74.208.94.91 +short)` = **0**); el dominio resultante tiene `abuse@` que acepta un mensaje de prueba (1 entrega, 0 rebote).
  4. Tope de SYN por /24 destino: con `RECRUIT_BUDGET_PER_CIDR=4000`, en cada hora de la oleada `psql -tAc "SELECT count(*) FROM recruit_jobs j JOIN recruit_candidates c USING (candidate_id) WHERE j.stage='sweep' AND split_network(c.ip,'/24')='203.0.113.0/24' AND date_trunc('hour', j.claimed_at)=date_trunc('hour', now())"` ≤ **4000** (0 horas por encima).
  5. Caudal real dentro del techo: a `rate_class=4` (5000 pps), `tcpdump -eni eth0 'tcp[tcpflags] & tcp-syn != 0' -c 15000` tarda entre **3** y **20 s** (700–5.000 pps reales) — la cifra del blueprint de 2,32 Mbps en trama de 58 B se comprueba, no se cita.
- **Depende de:** T-1504, T-1607
- **Estimación:** 8 h
- **DoD:** el IPv4 único deja de ser anónimo y anónimamente baneable; la subida de marcha tiene una condición de reputación y el /24 destino tiene techo.
- **Ficheros:** `scripts/recruit/rbl_probe.sh`, `deploy/dns/`, `docs/state/RECRUIT.md`
- **Cierra:** A1-05, A1-14 (`--source-ip`/`--adapter` en la práctica), A2-10 (cota del embudo)
