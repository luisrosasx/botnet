---
artifact: tasks
epic: EP-19
created: 2026-11-17
---

### T-1901 — `bootstrap_vps.sh`: paquetes, usuario, `env` 0640 y preflight con salida 2
- **Épica:** EP-19
- **Estado:** DONE | PRUEBA: preflight 0/2, env 640 root:botnet, max_connections 100 y barrido real; evidencia en docs/verification/EP-19.md
- **Criterios de aceptación:**
  1. `bash scripts/recruit/bootstrap_vps.sh --check; echo exit=$?` = **0** en el VPS e imprime las 6 líneas de comprobación: `grep -cE '^(masscan|zgrab2|psql|ssh|curl|max_connections)='` = **6** (y `dpkg -l | grep -cE '^ii +(masscan|zgrab2)'` = **2**).
  2. Con una herramienta ausente el preflight no mide: `PATH=/usr/bin bash scripts/recruit/bootstrap_vps.sh --check; echo exit=$?` = **2** (gate G5: hoy `command -v psql || echo NO` da `NO` y no hay preflight alguno).
  3. `/etc/botnet-recruit/env` con las 6 variables y permisos correctos: `grep -cE '^(BOTNET_DSN|RECRUIT_TICKET_SECRET|SHODAN_API_KEY|RECRUIT_SSH_USER|RECRUIT_SSH_KEY|RECRUIT_METRICS_ADDR)=' /etc/botnet-recruit/env` = **6** y `stat -c '%a %U:%G' /etc/botnet-recruit/env` = `640 root:botnet`; `ps -eo args= | grep -c 'RECRUIT_TICKET_SECRET'` = **0** (ningún secreto en la línea de `ExecStart`).
  4. Preflight de conexiones medido, no prometido: `psql -tAc "SHOW max_connections"` ≥ **40** y el plan (4 procesos × 2 + 8 del api = 16) cabe con margen ≥ **50 %**: `psql -tAc "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()"` ≤ **20** con todo el piloto corriendo (A2-01).
  5. Idempotencia y spike: dos ejecuciones seguidas salen **0** en ambas, y el sweep de 254 hosts con `--source-ip 74.208.94.91 --adapter eth0` deja `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE source='masscan'"` ≥ **1** con al menos **1** SYN-ACK observado (`grep -c 'synack=0' dist/raw/sweep.log` = **0** — A1-14).
- **Depende de:** T-1501
- **Estimación:** 8 h
- **DoD:** el VPS está preparado y dice la verdad de sus límites; si falta algo, el despliegue se para en vez de degradar en silencio.
- **Ficheros:** `scripts/recruit/bootstrap_vps.sh`, `deploy/env/recruit.env.example`, `/etc/botnet-recruit/env`
- **Cierra:** A2-01 (preflight), gate G5, A1-15 (host de artefactos real, no `dl.botnet.example`)

### T-1902 — `recruit.slice` y `botnet-recruit.service` con techo, watchdog y `--db-pool=2`
- **Épica:** EP-19
- **Estado:** DONE | PRUEBA: 8 props de unidad, cgroup 300 %, 53300=0 y 2 conexiones; evidencia en docs/verification/EP-19.md
- **Criterios de aceptación:**
  1. `systemctl show -p Slice botnet-recruit` = `Slice=recruit.slice` y `systemctl cat botnet-recruit.service | grep -cE '^(StartLimitIntervalSec=0|WatchdogSec=30|Restart=always|RestartSec=5|EnvironmentFile=/etc/botnet-recruit/env|AmbientCapabilities=CAP_NET_RAW|ProtectSystem=strict|KillMode=control-group)'` = **8**.
  2. Techo real en el cgroup: `systemctl show -p CPUQuota,MemoryMax,TasksMax recruit.slice` da `300%`, `1610612736` (1536 M) y `256`; la suma de cuotas por unidad no lo pasa: `150 + 3×50 = 300 %` y `512 + 3×320 = 1472 MB` ≤ 1536 M (A2-08 — el plan de P1 anunciaba 3,2 vCPU y eran 4,0).
  3. Pool por proceso medido: `systemctl show -p ExecStart botnet-recruit | grep -c -- '--db-pool=2'` = **1** y con las 4 unidades activas `psql -tAc "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()"` ≤ **20** con `journalctl -u 'botnet-recruit*' --since -5m | grep -c 53300` = **0** (A2-01 cerrado con número, no con PgBouncer).
  4. `ExecStartPre` sin masscan no degrada: `mv /usr/bin/masscan /usr/bin/masscan.off && systemctl restart botnet-recruit; systemctl is-active botnet-recruit` = `failed` y `curl -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **503** (no 200) `[injerto: V3]`.
  5. Crash-loop sin `failed`: `systemd-run --unit=t1902 --property=Restart=always --property=RestartSec=1 --property=StartLimitIntervalSec=0 /bin/false; sleep 12; systemctl is-active t1902` = `active` (con el default systemd sería `failed` tras 5 caídas) y `journalctl -u botnet-recruit | grep -c 'start request repeated'` = **0** (A2-05; limpiar con `systemctl reset-failed t1902; systemd-run --cleanup --unit=t1902`).
- **Depende de:** T-1901, T-1601
- **Estimación:** 8 h
- **DoD:** el servicio ocupa lo declarado, no más; un crash-loop y un binario ausente se ven, y el Postgres compartido no se queja.
- **Ficheros:** `deploy/systemd/recruit.slice`, `deploy/systemd/botnet-recruit.service`, `deploy/systemd/botnet-recruit-worker@.service`
- **Cierra:** A2-01, A2-05, A2-08, V3-3, V1-2 (`StartLimitIntervalSec=0`)

### T-1903 — Plantilla `botnet-recruit-worker@.service` con cierre por instancias
- **Épica:** EP-19
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `systemctl is-enabled 'botnet-recruit-worker@*'` da **3** líneas `enabled` y `systemctl is-active 'botnet-recruit-worker@*'` da **3** líneas `active` (unit **y** plantilla habilitadas — A2-06; hoy solo existe `deploy/systemd/botd.service`).
  2. `systemctl show -p CPUQuota,MemoryMax,TasksMax,TimeoutStopSec,KillMode,StartLimitIntervalSec botnet-recruit-worker@probe` devuelve `CPUQuota=50%`, `MemoryMax=335544320`, `TasksMax=64`, `TimeoutStopSec=20s`, `KillMode=control-group`, `StartLimitIntervalSec=0` — **6/6** (el `128` del injerto V3 queda absorbido por la slice: 256 en la slice y 64 por worker, §4 y §9 del blueprint).
  3. masscan no sobrevive al stop: `systemctl stop botnet-recruit-worker@probe; sleep 3; pgrep -c masscan` = **0** (hoy un huérfano emite pps extra — cadena B de A2); 0 procesos huérfanos tras 3 ciclos de `restart`.
  4. Cierre sin falso verde (A2-06): `systemctl stop botnet-recruit; sleep 3` ⇒ `systemctl is-active botnet-recruit` = `inactive` y **3** × `inactive` en `'botnet-recruit-worker@*'`; con la flota parada, `bash scripts/check-recruit.sh --only servicio; echo exit=$?` ≥ **1** (no 0).
  5. Resurrección medida, no declarada (A3-19): `systemctl kill --signal=TERM botnet-recruit-worker@probe; sleep 6; systemctl is-active botnet-recruit-worker@probe` = `active` con `systemctl show -p NRestarts --value botnet-recruit-worker@probe` ≥ **1**.
- **Depende de:** T-1902, T-1604
- **Estimación:** 6 h
- **DoD:** las tres instancias existen, se habilitan, mueren limpias y se miden por instancia: la unidad de cierre es exacta.
- **Ficheros:** `deploy/systemd/botnet-recruit-worker@.service`, `scripts/check-recruit.sh`
- **Cierra:** A2-06, A2-05 (plantilla), V2-2 (`TimeoutStopSec`), V3-3 (`KillMode`/`TasksMax`)

### T-1904 — Escritura de `nodes.public_ip` en el enroll y en el primer heartbeat
- **Épica:** EP-19
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `psql -tAc "SELECT count(*) FROM nodes WHERE public_ip IS NULL AND status IN ('enrolled','live')"` = **0** tras el piloto (hoy iguala el total de nodos vivos, porque nadie escribe la columna — A1-04).
  2. Escrito en el `201`: con un enroll nuevo, `curl -s -o /dev/null -w '%{http_code}'` = **201** y en la misma transacción `psql -tAc "SELECT count(*) FROM nodes WHERE node_id=$1 AND public_ip IS NOT NULL"` = **1**; `grep -c 'public_ip' cmd/botnet-api/repo/repo.go` ≥ **2** (en `InsertNode` y en `UpdateHeartbeat`).
  3. Confirmado en el primer heartbeat: un nodo con `public_ip` nula (insertado a mano) queda rellena tras su primer heartbeat — `psql -tAc "SELECT count(*) FROM nodes WHERE node_id=$1 AND public_ip IS NOT NULL"` = **1** en ≤ 60 s, 1/1.
  4. El purge solo la anula a los 13 meses (2/2 casos): `go test ./cmd/botnet-api/jobs/ -run TestPurgePublicIP -v 2>&1 | grep -c -- '^--- PASS'` ≥ **2** (nodo `retired` con `optout_at` de 12 meses ⇒ no nula; con 14 meses ⇒ nula) — `cmd/botnet-api/jobs/purge.go:41`.
  5. El anti-join de opt-out queda vivo: `bash scripts/discovery/reprobe_guard.sh; echo exit=$?` = **0** con salida `0 violaciones`, y un candidato cuya IP coincide con un `public_ip` con `leaves.reprobe_blocked_until` futuro no entra en la oleada (**0** altas nuevas, **1** excluido en el fixture).
- **Depende de:** T-1502, T-1602
- **Estimación:** 6 h
- **DoD:** la protección de opt-out del barrido deja de estar muerta: un tercero que se baja no vuelve a entrar como alta nueva en la misma oleada.
- **Ficheros:** `cmd/botnet-api/repo/repo.go`, `cmd/botnet-api/jobs/purge.go`, `cmd/botnet-api/jobs/leaves_test.go`
- **Cierra:** A1-04, A2-16 (base para el anti-join por `(ip,port)`)

### T-1905 — `/healthz` con reglas de 503 y rollback por escalones
- **Épica:** EP-19
- **Estado:** DONE | PRUEBA: reglas de 503, pausa, rollback y arranque en frio en el VPS; evidencia en docs/verification/EP-19.md
- **Criterios de aceptación:**
  1. En régimen normal `curl -s 127.0.0.1:9110/healthz` sale **0** con las 5 claves (`grep -cE '(status|db|masscan|tick_age_s|rate_class)='` = **5**) y `curl -o /dev/null -w '%{http_code}'` = **200**; el puerto viene del env: `grep -c 'RECRUIT_METRICS_ADDR=127.0.0.1:9110' /etc/botnet-recruit/env` = **1** y `ss -ltn | grep -cE ':9108|:9109'` = **0** (sin colisión con las propuestas, A2-17).
  2. Latido caducado ⇒ **503**: `psql -c "UPDATE recruit_control SET tick_at = now() - interval '2 min'"` y, a los 30 s, `curl -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **503** (hoy 200); con `tick_at` fresco vuelve a **200** (2/2).
  3. Postgres caído y ritmo cero con `paused=false` ⇒ **503** (2/2); ritmo cero con `paused=true` ⇒ **200** (1/1). Medido con `psql` parado en el contenedor y `psql -c "UPDATE recruit_windows SET pps_used=0"` + `recruit_control.paused`.
  4. Rollback por escalones sin revertir esquema: `curl -s -X POST 127.0.0.1:9110/v1/control/pause` deja `psql -tAc "SELECT paused FROM recruit_control"` = **t** y 0 `claim` en 60 s; tras `systemctl stop 'botnet-recruit-worker@*'` y `systemctl stop botnet-recruit`, `psql -tAc "SELECT count(*) FROM schema_migrations WHERE version LIKE '0009%'"` sigue en **1** (DDL aditivo, sin `down`) y `psql -tAc "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state='active'"` baja de **8**.
  5. Arranque en frío con la cola al máximo (riesgo 2 del blueprint §12): tras `systemctl start botnet-recruit`, en el primer tick `psql -tAc "SELECT pps_target FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 1"` ≤ **1500**, `pgrep -c masscan` ≤ **1** y `curl -s 127.0.0.1:9110/healthz | grep -oE 'tick_age_s=[0-9]+'` con valor **< 90**.
- **Depende de:** T-1902, T-1601
- **Estimación:** 8 h
- **DoD:** el servicio dice la verdad de su salud y se puede parar por escalones sin tocar el esquema ni los datos.
- **Ficheros:** `internal/recruit/httpapi.go`, `internal/recruit/httpapi_test.go`, `deploy/systemd/botnet-recruit.service`
- **Cierra:** V1-2 (latido 90 s), A2-03 (503), A2-17 (puerto reservado)
