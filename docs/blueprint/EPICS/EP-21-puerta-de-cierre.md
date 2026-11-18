---
artifact: epic
id: EP-21
title: "Puerta de cierre: oleadas, 20 nodos reales y evidencia"
owner_layer: recruit
closes: A1-09, A3-01, A3-02, A3-03, A3-04, A3-05, A3-06, A3-07, A3-09, A3-11, A3-14, A3-17, riesgos 2 y 7 del blueprint §12
estimate_days: 6
risk: high
phase: F7
---

## Objetivo

Que el piloto se cierre con un comando y no con un acta. «Cinco oleadas cerradas» pasa a ser una consulta con número (`recruit_waves` escrita por `botnet-recruit wave close`, con la bitácora como espejo y no como prueba), «veinte nodos verificados» pasa a ser `count(DISTINCT node_id)` con las cinco condiciones de `verified` simultáneas en el instante de medir, y cada AC medido queda en su fichero `docs/verification/EP-15..21.md` con su fila, su fecha y su `sha256`. El cierre se demuestra con los seis sabotajes en rojo y con las cinco puertas del plan anterior encadenadas en 0, dentro de los 24 días de calendario.

## Alcance

- `cmd/botnet-recruit/main.go` con el subcomando `wave close --note="oleada N"`: idempotente, escribe `state='cerrada'`, `promoted>0`, `verified>=1` y `closed_at` en `recruit_waves`.
- `docs/state/RECRUIT.md` (nuevo) con la sección de oleadas (5 filas con sus tres cifras) y los dos hitos fechados en ISO — primer SYN real y primer nodo verificado (A3-17, A3-14) —, validado por `scripts/check-state-doc.sh`.
- `scripts/field/socks5_probe_all.sh` + `internal/recruit/store.go`: los 20 verify concurrentes con sesión propia, todos de clase A, con `egress_ip` distinta de la del VPS y sin `node_id` repetido.
- `docs/verification/EP-15.md` … `docs/verification/EP-21.md` con la tabla `| Task | AC | Veredicto | Evidencia |` y `docs/verification/evidence/` con ≥ 6 ficheros que llevan `sha256=` en las 3 primeras líneas y su `sha256sum -c` en 0 (A3-09).
- `docs/runbooks/recruit.md` (nuevo) con ≥ 6 secciones — arranque, pausa, rollback, revoque y desinstala, sabotajes del paraguas y límites del servicio —, los comandos en bloques `bash` validados con `bash -n`, el rollback de 4 escalones probado y los límites del systemd (no los del plan) escritos: `CPUQuota=300%`, `MemoryMax=1536M`, `TasksMax=256`, `--db-pool=2`, `9110`.
- `scripts/field/revoke_mass.sh` + `scripts/field/bootstrap.sh --uninstall`: retirar la flota sin dejar «software desconocido» en casa ajena (A1-09).
- `scripts/recruit/sabotage_recruit.sh`: los 6 sabotajes del paraguas (nodo fantasma, cuenta por intentos, SOCKS5 sin flota, puerta vaciada, `psql` fuera del PATH, servicio con instancias paradas) con su reversión.

Fuera de alcance: todo lo verificable ya construido en EP-15..EP-20; esta épica no añade maquinaria, ejerce y firma.

## Cierre

- **T-2101 AC1** — `./bin/botnet-recruit wave close --note="oleada N"` sale **0** y `count(*) FROM recruit_waves WHERE state='cerrada'` sube exactamente **+1**; cerrar dos veces la misma oleada no cambia el recuento y sale **0**.
- **T-2101 AC2** — al final del cierre `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND promoted>0 AND verified>=1 AND closed_at IS NOT NULL"` = **5** (hoy 0 filas) y `count(*) WHERE closed_at <= started_at` = **0** (A3-05).
- **T-2101 AC3** — `grep -cE '^\| *[1-5] *\|' docs/state/RECRUIT.md` ≥ **5** y 0 filas con celda vacía medido con `awk -F'|'` (A3-17).
- **T-2101 AC4** — el gate de estado la exige: `bash scripts/check-state-doc.sh; echo exit=$?` = **0** y **≥ 1** al renombrar la sección de oleadas (sabotaje + reversión medidos dos veces).
- **T-2101 AC5** — dos hitos fechados: `grep -cE 'primer SYN real|primer nodo verificado' docs/state/RECRUIT.md` = **2** con fecha ISO (`grep -cE '20[0-9]{2}-[0-9]{2}-[0-9]{2}'` ≥ **2**, A3-14).
- **T-2102 AC1** — `psql -tAc "SELECT count(DISTINCT c.node_id) FROM recruit_candidates c JOIN nodes n USING (node_id) JOIN LATERAL (…) x ON true WHERE c.state='verified' AND n.status IN ('enrolled','live') AND n.optout_at IS NULL AND x.ts > now()-interval '15 min'"` ≥ **20** (A3-01, A3-04).
- **T-2102 AC2** — las cinco condiciones a la vez: `count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id) WHERE s.ok AND s.http_status=200 AND s.bytes_in>0 AND s.latency_ms BETWEEN 0 AND 1500 AND s.ip_match AND n.status IN ('enrolled','live') AND n.optout_at IS NULL` = **20**, con `count(*)` de candidatos con >1 fila `ok` = **0** (A3-02).
- **T-2102 AC3** — todos de clase A y publicado: `count(*)` de `verified` con `device_class='A'` = total de `verified` (20/20) y los B/C enrolados quedan `deferred` con `fail_reason='socks_fail'` (riesgo 1 del §12: un B/C extra no es fracaso).
- **T-2102 AC4** — churn y éxito medidos en el cierre: `botnet_recruit_node_leave_ratio_24h` ≤ **0,05** y `botnet_recruit_socks5_success_ratio` ≥ **0,95** en `curl -s 127.0.0.1:9110/metrics` de la oleada que cierra.
- **T-2102 AC5** — ningún doble de laboratorio: `count(*) FROM recruit_socks5_checks WHERE egress_ip='74.208.94.91'` = **0** y `count(DISTINCT node_id) … WHERE ok` = **20** sobre `count(*)` = **20** (0 `node_id` repetido, A3-03).
- **T-2103 AC1** — `ls docs/verification/EP-1[5-9].md docs/verification/EP-2[0-1].md | wc -l` = **7** y cada uno contiene la cabecera `| Task | AC | Veredicto | Evidencia |`.
- **T-2103 AC2** — muestreo de tres tasks: `for t in T-1501 T-1803 T-2102; do grep -hE "^\| $t \| AC[0-9]+ \|" docs/verification/EP-*.md | grep -c PASS; done` devuelve **≥ 3** en las tres.
- **T-2103 AC3** — `bash scripts/check-implementation.sh; echo exit=$?` = **0** y **1** al borrar una fila `PASS` de `docs/verification/EP-15.md` (A3-06 ejercitado con las épicas nuevas).
- **T-2103 AC4** — `ls docs/verification/evidence/ | grep -c 'recruit-'` ≥ **6** con `sha256=` en las 3 primeras líneas de cada fichero y `sha256sum -c` del manifiesto en **0** (A3-09).
- **T-2103 AC5** — `bash scripts/check-field-closure.sh --strict; echo exit=$?` = **0** con 0 filas `PASS` de celda de evidencia vacía medido con `awk` (A3-11).
- **T-2104 AC1** — `grep -cE '^## ' docs/runbooks/recruit.md` ≥ **6** con las secciones de arranque, pausa, rollback, revoque y desinstala, sabotajes del paraguas y límites del servicio.
- **T-2104 AC2** — todo comando es ejecutable: `awk '/^```bash/{f=1;next}/^```/{f=0}f' docs/runbooks/recruit.md > /tmp/rb.sh && bash -n /tmp/rb.sh; echo exit=$?` = **0** y `grep -c '^\$ ' docs/runbooks/recruit.md` ≥ **12**.
- **T-2104 AC3** — rollback de 4 escalones probado: `pause` → `systemctl stop 'botnet-recruit-worker@*'` → `systemctl stop botnet-recruit` → `bash scripts/field/revoke_mass.sh` deja **3** × `inactive` y `count(*) FROM recruit_control WHERE paused` = **1**.
- **T-2104 AC4** — los límites del doc son los del systemd: `grep -cE 'CPUQuota=300%|MemoryMax=1536M|TasksMax=256|--db-pool=2|9110' docs/runbooks/recruit.md` ≥ **5** y `systemctl show -p TasksMax --value recruit.slice` = **256** (0 discrepancias).
- **T-2104 AC5** — la decisión de proxy del §8 queda documentada: `grep -cE 'MAX_A|8\.900\.000|clase_bc_sin_socks5' docs/runbooks/recruit.md` ≥ **2** y el runbook dice qué hacer con un B/C enrolado (1 sección, ≥ 3 líneas).
- **T-2105 AC1** — `bash scripts/check-recruit.sh; echo exit=$?` = **0** en el VPS con `comprobaciones=8` impreso y las **5** condiciones del objetivo listadas como `ok` en `--map`.
- **T-2105 AC2** — cero falsos verdes: `bash scripts/recruit/sabotage_recruit.sh` imprime `sabotajes=6 fallan=6` y sale **0**; al terminar, `bash scripts/check-recruit.sh; echo exit=$?` vuelve a **0**.
- **T-2105 AC3** — gates del plan anterior en 0 en cadena: `bash scripts/check-blueprint.sh && bash scripts/check-implementation.sh && bash scripts/check-field-closure.sh --strict && bash scripts/check-state-doc.sh && bash scripts/metrics_smoke.sh; echo exit=$?` = **0** (5 puertas).
- **T-2105 AC4** — suite verde con la 0009 ejercitada: `go build ./... && go test ./... 2>&1 | grep -c '^FAIL'` = **0** y `go test ./migrations/ -v 2>&1 | grep -c -- '--- SKIP'` = **0** (gate G4 cerrado en el cierre).
- **T-2105 AC5** — calendario cumplido: `psql -tAc "SELECT max(closed_at)-min(started_at) <= interval '24 days' FROM recruit_waves WHERE state='cerrada'"` = **t** y los dos hitos de T-2101 caen dentro de esa ventana (0 hitos fuera).
