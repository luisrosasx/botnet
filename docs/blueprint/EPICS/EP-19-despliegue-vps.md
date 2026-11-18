---
artifact: epic
id: EP-19
title: "Despliegue en el VPS: slice, unidades, salud y `public_ip`"
owner_layer: recruit
closes: A1-04, A1-15, A2-01, A2-03, A2-05, A2-06, A2-08, A2-16, A2-17, injertos V1-2, V2-2, V3-3, gate G5
estimate_days: 4.5
risk: medium
phase: F7
---

## Objetivo

Que el piloto quepa en el VPS real (74.208.94.91, Ubuntu 26.04) y que el despliegue diga la verdad de sus límites: un preflight que se para con salida 2 si falta una herramienta en vez de degradar en silencio, un techo que lo pone el cgroup (`recruit.slice`) y no la buena intención, pool de conexiones por proceso medido para no ahogar el Postgres compartido, tres instancias de worker que se habilitan, mueren limpias y se miden por instancia, `/healthz` con reglas de 503 honestas y un rollback por escalones que no toca el esquema. Y que `nodes.public_ip` por fin se escriba, porque sin ella el anti-join de opt-out del barrido está muerto.

## Alcance

- `scripts/recruit/bootstrap_vps.sh`: paquetes (`masscan`, `zgrab2`), usuario `botnet`, `/etc/botnet-recruit/env` con las 6 variables y `640 root:botnet`, preflight de `max_connections` con salida 2 y plantilla `deploy/env/recruit.env.example`.
- `deploy/systemd/recruit.slice`: `CPUQuota=300%`, `MemoryMax=1610612736` (1536 M) y `TasksMax=256` — el techo real, del que las unidades son desbordamiento (150+3×50 % y 512+3×320 MB).
- `deploy/systemd/botnet-recruit.service`: `StartLimitIntervalSec=0`, `WatchdogSec=30`, `Restart=always`, `RestartSec=5`, `EnvironmentFile=`, `AmbientCapabilities=CAP_NET_RAW`, `ProtectSystem=strict`, `KillMode=control-group` y `--db-pool=2`; `ExecStartPre=test -x /usr/bin/masscan` `[injerto: V3]`.
- `deploy/systemd/botnet-recruit-worker@.service` (plantilla): `CPUQuota=50%`, `MemoryMax=335544320`, `TasksMax=64`, `TimeoutStopSec=20s` (V2-2), `KillMode=control-group` y `StartLimitIntervalSec=0` (A2-05/A2-06).
- `internal/recruit/httpapi.go` + `httpapi_test.go`: reglas de 503 de `/healthz` en el puerto reservado `127.0.0.1:9110` (`RECRUIT_METRICS_ADDR`, A2-17) y rollback por escalones.
- `cmd/botnet-api/repo/repo.go` (`InsertNode` y `UpdateHeartbeat`), `cmd/botnet-api/jobs/purge.go` y `jobs/leaves_test.go`: escritura de `nodes.public_ip` en el `201` y en el primer heartbeat, purge a los 13 meses y guardia del re-clutaje con `reprobe_guard.sh`.
- `scripts/check-recruit.sh` como unidad de cierre exacta: se mide `'botnet-recruit-worker@*'`, nunca el target (lo consume EP-20).

Fuera de alcance: el catálogo de métricas `botnet_recruit_*` y el paraguas completo son EP-20; la evidencia SOCKS5 que alimenta `check-recruit.sh --only socks5` es EP-18.

## Cierre

- **T-1901 AC1** — `bash scripts/recruit/bootstrap_vps.sh --check; echo exit=$?` = **0** e imprime las 6 líneas de comprobación (`grep -cE '^(masscan|zgrab2|psql|ssh|curl|max_connections)='` = **6**).
- **T-1901 AC2** — con una herramienta ausente (`PATH=/usr/bin`) el preflight no mide: sale **2** (gate G5; hoy `command -v psql || echo NO` da `NO` y no hay preflight).
- **T-1901 AC3** — `grep -cE '^(BOTNET_DSN|RECRUIT_TICKET_SECRET|SHODAN_API_KEY|RECRUIT_SSH_USER|RECRUIT_SSH_KEY|RECRUIT_METRICS_ADDR)=' /etc/botnet-recruit/env` = **6** con `stat -c '%a %U:%G'` = `640 root:botnet` y `ps -eo args= | grep -c 'RECRUIT_TICKET_SECRET'` = **0**.
- **T-1901 AC4** — preflight de conexiones medido: `SHOW max_connections` ≥ **40** y `count(*)` de `pg_stat_activity` ≤ **20** con todo el piloto corriendo (plan 4 procesos × 2 + 8 del api = 16 con margen ≥ 50 %, A2-01).
- **T-1901 AC5** — idempotencia y spike: dos ejecuciones seguidas salen **0** y el barrido de 254 hosts con `--source-ip 74.208.94.91 --adapter eth0` deja `source='masscan'` ≥ **1** con `grep -c 'synack=0' dist/raw/sweep.log` = **0** (A1-14, A1-15).
- **T-1902 AC1** — `systemctl show -p Slice botnet-recruit` = `Slice=recruit.slice` y `systemctl cat botnet-recruit.service | grep -cE '^(StartLimitIntervalSec=0|WatchdogSec=30|Restart=always|RestartSec=5|EnvironmentFile=/etc/botnet-recruit/env|AmbientCapabilities=CAP_NET_RAW|ProtectSystem=strict|KillMode=control-group)'` = **8**.
- **T-1902 AC2** — techo real en el cgroup: `systemctl show -p CPUQuota,MemoryMax,TasksMax recruit.slice` da `300%`, `1610612736` y `256`, y la suma de cuotas (150+3×50 % y 512+3×320 MB) no lo pasa (A2-08).
- **T-1902 AC3** — pool medido: `systemctl show -p ExecStart botnet-recruit | grep -c -- '--db-pool=2'` = **1**, `pg_stat_activity` ≤ **20** con las 4 unidades activas y `journalctl -u 'botnet-recruit*' --since -5m | grep -c 53300` = **0** (A2-01 sin PgBouncer).
- **T-1902 AC4** — sin `masscan` no degrada: `mv /usr/bin/masscan …` + `systemctl restart` deja la unidad `failed` y `/healthz` en **503**, no 200 `[injerto: V3]`.
- **T-1902 AC5** — crash-loop sin `failed`: `systemd-run --unit=t1902 --property=Restart=always --property=RestartSec=1 --property=StartLimitIntervalSec=0 /bin/false` sigue `active` a los 12 s y `journalctl | grep -c 'start request repeated'` = **0** (A2-05).
- **T-1903 AC1** — `systemctl is-enabled 'botnet-recruit-worker@*'` da **3** líneas `enabled` y `is-active` **3** `active` (unit **y** plantilla habilitadas, A2-06).
- **T-1903 AC2** — `systemctl show -p CPUQuota,MemoryMax,TasksMax,TimeoutStopSec,KillMode,StartLimitIntervalSec botnet-recruit-worker@probe` da `50%`, `335544320`, `64`, `20s`, `control-group`, `0` — **6/6**.
- **T-1903 AC3** — masscan no sobrevive al stop: `systemctl stop botnet-recruit-worker@probe; sleep 3; pgrep -c masscan` = **0**, con 0 huérfanos tras 3 ciclos de `restart`.
- **T-1903 AC4** — cierre sin falso verde: tras `systemctl stop botnet-recruit` la unidad queda `inactive` con 3 × `inactive` en las instancias y `bash scripts/check-recruit.sh --only servicio; echo exit=$?` ≥ **1** (A2-06).
- **T-1903 AC5** — resurrección medida: `systemctl kill --signal=TERM botnet-recruit-worker@probe; sleep 6` ⇒ `active` con `NRestarts` ≥ **1** (A3-19).
- **T-1904 AC1** — `psql -tAc "SELECT count(*) FROM nodes WHERE public_ip IS NULL AND status IN ('enrolled','live')"` = **0** tras el piloto (hoy iguala el total de vivos, A1-04).
- **T-1904 AC2** — escrito en el `201`: el enroll nuevo sale **201** con `count(*)` de ese `node_id` con `public_ip IS NOT NULL` = **1** en la misma transacción, y `grep -c 'public_ip' cmd/botnet-api/repo/repo.go` ≥ **2**.
- **T-1904 AC3** — confirmado en el primer heartbeat: un nodo con `public_ip` nula queda rellena en ≤ **60 s** (1/1).
- **T-1904 AC4** — el purge solo la anula a los 13 meses: `go test ./cmd/botnet-api/jobs/ -run TestPurgePublicIP -v 2>&1 | grep -c -- '^--- PASS'` ≥ **2** (12 meses ⇒ no nula; 14 meses ⇒ nula).
- **T-1904 AC5** — el anti-join de opt-out queda vivo: `bash scripts/discovery/reprobe_guard.sh; echo exit=$?` = **0** con `0 violaciones`, y un candidato con IP en `reprobe_blocked_until` futuro no entra en la oleada (**0** altas, **1** excluido).
- **T-1905 AC1** — en régimen `/healthz` sale **0** con las 5 claves y `curl -o /dev/null -w '%{http_code}'` = **200**; `grep -c 'RECRUIT_METRICS_ADDR=127.0.0.1:9110' /etc/botnet-recruit/env` = **1** y `ss -ltn | grep -cE ':9108|:9109'` = **0** (A2-17).
- **T-1905 AC2** — latido caducado ⇒ **503**: `UPDATE recruit_control SET tick_at = now() - interval '2 min'` y a los 30 s el `curl` da **503** (hoy 200); con `tick_at` fresco vuelve a **200** (2/2).
- **T-1905 AC3** — Postgres caído y ritmo cero con `paused=false` ⇒ **503** (2/2); ritmo cero con `paused=true` ⇒ **200** (1/1).
- **T-1905 AC4** — rollback por escalones sin revertir esquema: `POST /v1/control/pause` deja `paused` = **t** y 0 `claim` en 60 s; tras parar workers y orquestador `schema_migrations LIKE '0009%'` sigue en **1** y `pg_stat_activity` en `state='active'` baja de **8**.
- **T-1905 AC5** — arranque en frío con la cola al máximo (riesgo 2 del §12): en el primer tick `pps_target` ≤ **1500**, `pgrep -c masscan` ≤ **1** y `tick_age_s` < **90**.
