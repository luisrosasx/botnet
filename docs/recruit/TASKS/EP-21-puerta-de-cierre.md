---
artifact: tasks
epic: EP-21
created: 2026-11-17
---

### T-2101 — `wave close` y las 5 oleadas medibles en `recruit_waves` + bitácora
- **Épica:** EP-21
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `./bin/botnet-recruit wave close --note="oleada N"` sale **0** y `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada'"` sube exactamente en **+1**; cerrar dos veces la misma oleada deja `count(*)` **sin cambio** y salida **0** (idempotente).
  2. `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND promoted>0 AND verified>=1 AND closed_at IS NOT NULL"` = **5** al final del cierre (hoy la tabla no tiene filas) y `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND closed_at <= started_at"` = **0** (A3-05).
  3. `docs/state/RECRUIT.md` existe con la sección de oleadas y **5** filas con sus tres cifras: `grep -cE '^\| *[1-5] *\|' docs/state/RECRUIT.md` ≥ **5** y 0 filas con celda vacía (`awk -F'|' '/^\| *[1-5] *\|/{for(i=2;i<=NF;i++) if($i ~ /^ *$/) print}' | grep -c .` = **0**) (A3-17).
  4. El gate de estado la exige: `bash scripts/check-state-doc.sh; echo exit=$?` = **0** y **≥ 1** al renombrar la sección de oleadas (sabotaje + reversión medidos dos veces).
  5. Dos hitos fechados (A3-14): `grep -cE 'primer SYN real|primer nodo verificado' docs/state/RECRUIT.md` = **2**, cada uno con fecha ISO `20[0-9]{2}-[0-9]{2}-[0-9]{2}` (`grep -cE '20[0-9]{2}-[0-9]{2}-[0-9]{2}' docs/state/RECRUIT.md` ≥ **2**) — el presupuesto de días deja de ser intocable.
- **Depende de:** T-1504, T-1606
- **Estimación:** 8 h
- **DoD:** «cinco oleadas cerradas» es una consulta con número, no una frase del acta; el calendario tiene dos fechas que se pueden discutir.
- **Ficheros:** `cmd/botnet-recruit/main.go`, `docs/state/RECRUIT.md`, `scripts/check-state-doc.sh`
- **Cierra:** A3-05, A3-17, A3-14

### T-2102 — Los 20 nodos reales simultáneos con las cinco condiciones de `verified`
- **Épica:** EP-21
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `psql -tAc "SELECT count(DISTINCT c.node_id) FROM recruit_candidates c JOIN nodes n USING (node_id) JOIN LATERAL (SELECT ts FROM node_heartbeats h WHERE h.node_id=n.node_id ORDER BY ts DESC LIMIT 1) x ON true WHERE c.state='verified' AND n.status IN ('enrolled','live') AND n.optout_at IS NULL AND x.ts > now()-interval '15 min'"` ≥ **20** (A3-01, A3-04).
  2. Las cinco condiciones a la vez: `psql -tAc "SELECT count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id) WHERE s.ok AND s.http_status=200 AND s.bytes_in>0 AND s.latency_ms BETWEEN 0 AND 1500 AND s.ip_match AND n.status IN ('enrolled','live') AND n.optout_at IS NULL"` = **20** y `psql -tAc "SELECT count(*) FROM (SELECT candidate_id FROM recruit_socks5_checks WHERE ok GROUP BY 1 HAVING count(*)>1) x"` = **0** (un único ganador por candidato, A3-02).
  3. Todos de clase A y publicado: `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='verified' AND device_class='A'"` = total de `verified` (20/20) y `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='deferred' AND fail_reason='socks_fail'"` = nº de B/C enrolados (un B/C extra **no** cuenta como fracaso — riesgo 1 del blueprint §12).
  4. Churn y éxito medidos en el cierre: `curl -s 127.0.0.1:9110/metrics | awk '/^botnet_recruit_node_leave_ratio_24h/{print $2}'` ≤ **0,05** y `.../^botnet_recruit_socks5_success_ratio/{print $2}'` ≥ **0,95** en la oleada que cierra.
  5. Ningún doble de laboratorio: `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE egress_ip='74.208.94.91'"` = **0** y `psql -tAc "SELECT count(DISTINCT node_id) FROM recruit_socks5_checks WHERE ok"` = **20** sobre `count(*)` = **20** filas ganadoras (0 `node_id` repetido, 0 IP del VPS — A3-03).
- **Depende de:** T-1804, T-1805, T-1904, T-1705
- **Estimación:** 16 h
- **DoD:** veinte aparatos distintos, vivos en el instante de medir, con el tráfico saliendo por su propia IP y todos de la clase que escucha SOCKS5.
- **Ficheros:** `docs/state/RECRUIT.md`, `scripts/field/socks5_probe_all.sh`, `internal/recruit/store.go`
- **Cierra:** A3-01, A3-02, A3-03, A3-04 (cierre con los 20 reales)

### T-2103 — Evidencia fechada con `sha256` y ficheros `docs/verification/EP-15..21.md`
- **Épica:** EP-21
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `ls docs/verification/EP-1[5-9].md docs/verification/EP-2[0-1].md 2>/dev/null | wc -l | tr -d ' '` = **7** (un fichero por épica nueva) y cada uno contiene al menos una tabla con la cabecera `| Task | AC | Veredicto | Evidencia |`.
  2. Muestreo de tres tasks: `for t in T-1501 T-1803 T-2102; do grep -hE "^\| $t \| AC[0-9]+ \|" docs/verification/EP-*.md | grep -c PASS; done` devuelve **≥ 3** en las tres (cada task con ≥ 3 filas PASS, una por AC medido).
  3. `bash scripts/check-implementation.sh; echo exit=$?` = **0**, y **1** al borrar una fila `PASS` de `docs/verification/EP-15.md` (sabotaje + reversión — A3-06 ya cerrado pero aquí se ejerce con las épicas nuevas).
  4. `ls docs/verification/evidence/ | grep -c 'recruit-'` ≥ **6** y cada fichero lleva `sha256=` en las 3 primeras líneas (`head -3 f | grep -c 'sha256='` = **1** para los 6); el `sha256sum -c` del fichero de manifiestos de evidencia sale **0** (A3-09).
  5. `bash scripts/check-field-closure.sh --strict; echo exit=$?` = **0** con la celda de evidencia no vacía en toda fila `PASS` (0 filas `PASS` con celda vacía, medido con `awk`).
- **Depende de:** T-2001, T-2101, T-2102
- **Estimación:** 8 h
- **DoD:** el cierre se puede volver a leer dentro de seis meses: cada AC medido tiene su fila, su fecha y su hash.
- **Ficheros:** `docs/verification/EP-15.md` … `docs/verification/EP-21.md`, `docs/verification/evidence/`
- **Cierra:** A3-06 (ejercicio), A3-09, A3-11 (evidencia no vacía)

### T-2104 — Runbook `docs/runbooks/recruit.md` con operación, rollback y límites reales
- **Épica:** EP-21
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `grep -cE '^## ' docs/runbooks/recruit.md` ≥ **6** con las secciones de arranque, pausa, rollback, revoque y desinstala, sabotajes del paraguas y límites del servicio.
  2. Todo comando del runbook es ejecutable: `awk '/^```bash/{f=1;next}/^```/{f=0}f' docs/runbooks/recruit.md > /tmp/rb.sh && bash -n /tmp/rb.sh; echo exit=$?` = **0** y `grep -c '^\$ ' docs/runbooks/recruit.md` ≥ **12**.
  3. El rollback de 4 escalones está probado con su salida real pegada: tras ejecutar `POST /v1/control/pause` → `systemctl stop 'botnet-recruit-worker@*'` → `systemctl stop botnet-recruit` → `bash scripts/field/revoke_mass.sh`, `systemctl is-active 'botnet-recruit-worker@*'` da **3** × `inactive` y `psql -tAc "SELECT count(*) FROM recruit_control WHERE paused"` = **1**.
  4. Los límites escritos coinciden con el systemd vivo: `grep -cE 'CPUQuota=300%|MemoryMax=1536M|TasksMax=256|--db-pool=2|9110' docs/runbooks/recruit.md` ≥ **5** y `systemctl show -p TasksMax --value recruit.slice` = **256** (0 discrepancias doc/servicio).
  5. La decisión de proxy del §8 queda documentada: `grep -cE 'MAX_A|8\.900\.000|clase_bc_sin_socks5' docs/runbooks/recruit.md` ≥ **2** y el runbook dice qué hacer con un B/C enrolado (1 sección, ≥ 3 líneas).
- **Depende de:** T-1902, T-1903, T-1905, T-2005
- **Estimación:** 8 h
- **DoD:** otro operario puede pausar, revertir, revocar y volver a arrancar el piloto sin preguntar; los números del doc son los del systemd, no los del plan.
- **Ficheros:** `docs/runbooks/recruit.md`, `scripts/field/revoke_mass.sh`, `scripts/field/bootstrap.sh`
- **Cierra:** riesgo 2 y 7 del blueprint §12 (arranque en frío y objeción del dueño), A1-09 (`--uninstall` en el runbook)

### T-2105 — Cierre: paraguas en 0, seis sabotajes en rojo y gates existentes en 0
- **Épica:** EP-21
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `bash scripts/check-recruit.sh; echo exit=$?` = **0** en el VPS con `comprobaciones=8` impreso y las **5** condiciones del objetivo listadas como `ok` en `--map` (0 condiciones sin comprobación).
  2. Cero falsos verdes: `bash scripts/recruit/sabotage_recruit.sh` imprime `sabotajes=6 fallan=6` y sale **0** (los 6: nodo fantasma, cuenta por intentos, SOCKS5 sin flota, puerta vaciada, `psql` fuera del PATH, servicio con instancias paradas); al terminar el script, `bash scripts/check-recruit.sh; echo exit=$?` vuelve a **0**.
  3. Gates del plan anterior en 0 en cadena: `bash scripts/check-blueprint.sh && bash scripts/check-implementation.sh && bash scripts/check-field-closure.sh --strict && bash scripts/check-state-doc.sh && bash scripts/metrics_smoke.sh; echo exit=$?` = **0** (5 gates encadenados).
  4. Suite verde con la 0009 ejercitada: `go build ./... && go test ./... 2>&1 | grep -c '^FAIL'` = **0** y `go test ./migrations/ -v 2>&1 | grep -c -- '--- SKIP'` = **0** (gate G4 cerrado en el cierre).
  5. Calendario cumplido: `psql -tAc "SELECT max(closed_at)-min(started_at) <= interval '24 days' FROM recruit_waves WHERE state='cerrada'"` = **t** y los dos hitos fechados de T-2101 caen dentro de esa ventana (0 hitos fuera).
- **Depende de:** T-2003, T-2004, T-2005, T-2101, T-2102, T-2103, T-2104
- **Estimación:** 8 h
- **DoD:** el piloto está cerrado con un comando, con los seis sabotajes demostrando que el comando muerde y con los gates anteriores sin romperse.
- **Ficheros:** `scripts/recruit/sabotage_recruit.sh`, `scripts/check-recruit.sh`, `docs/state/RECRUIT.md`
- **Cierra:** A3-07 (cierre con el paraguas), A3-14 (calendario anclado), objetivo de las 5 condiciones
