---
artifact: epic
id: EP-19
title: Despliegue en el VPS (74.208.94.91) y unidades systemd con techo de slice
phase: F4
risk: medium
estimate_days: 4.5
closes: A1-04, A2-01, A2-05, A2-06, A2-08, injertos V3-3
artifacts:
  - scripts/recruit/bootstrap_vps.sh
  - deploy/systemd/recruit.slice
  - deploy/systemd/botnet-recruit.service
  - deploy/systemd/botnet-recruit-worker@.service
  - cmd/botnet-api/repo/repo.go
---

## Objetivo

Que el piloto viva en el VPS real (74.208.94.91, 12 vCPU / 24 GB ya ocupados por nuclei, archivebox, Docker y el Postgres del backend) sin tumbar a los vecinos del dueño. Cuatro procesos habilitados — 1 orquestador + 3 instancias de worker (`probe`, `cred`, `verify`) — con el techo real puesto en el cgroup (`recruit.slice`), pool de conexiones recortado por proceso, secretos fuera de la línea de `ExecStart`, y una unidad de cierre exacta que no dé ni falso rojo ni falso verde.

## Alcance

- **`scripts/recruit/bootstrap_vps.sh`** — instala y aserte `masscan` y `zgrab2`, crea el usuario/grupo `botnet`, deja `/etc/botnet-recruit/env` en `0640 root:botnet` con `BOTNET_DSN`, `RECRUIT_TICKET_SECRET`, `SHODAN_API_KEY`, `RECRUIT_SSH_USER`, `RECRUIT_SSH_KEY` y `RECRUIT_METRICS_ADDR=127.0.0.1:9110`, y hace el **preflight** de `SHOW max_connections`, de `psql`, `ssh`, `curl`, `masscan` y `zgrab2` (falta alguno ⇒ salida 2, gate G5).
- **`recruit.slice` como techo real** — `CPUQuota=300%` (3 de 12 vCPU), `MemoryMax=1536M`, `TasksMax=256`. Las cotas por unidad son desbordamiento y su suma coincide con el techo: 150 + 3×50 = 300 % y 512 + 3×320 = 1.472 MB (A2-08: las cuotas son por instancia y el plan de P1 se quedaba corto).
- **`botnet-recruit.service`** — `ExecStartPre=/usr/bin/test -x /usr/bin/masscan` (falla al arrancar, no degrada en silencio), `ExecStart=/usr/local/bin/botnet-recruit --config /etc/botnet-recruit/config.json --db-pool=2` (A2-01), `ExecStartPost=... --roll-window --once`, `Restart=always`, `RestartSec=5`, `StartLimitIntervalSec=0` (A2-05), `WatchdogSec=30`, `CPUQuota=150%`, `MemoryMax=512M`, `IOWeight=50`, `Nice=10`, `CPUWeight=50`, `KillMode=control-group`, `AmbientCapabilities=CAP_NET_RAW`, `ProtectSystem=strict`, `ReadWritePaths=/var/lib/botnet-recruit`, `EnvironmentFile=/etc/botnet-recruit/env` `[injerto: V3]`.
- **`botnet-recruit-worker@.service`** — misma plantilla con `%i = probe | cred | verify`, `CPUQuota=50%`, `MemoryMax=320M`, `TasksMax=64`, `TimeoutStopSec=20` (mata el hijo en stop, A2-04) y `StartLimitIntervalSec=0`; **la plantilla queda habilitada** además de la unit (A2-05/A2-06).
- **Unidad de cierre exacta** — se cierra con `systemctl is-active 'botnet-recruit-worker@*'` midiendo **instancias**, nunca el target: `systemctl stop botnet-recruit; sleep 3` ⇒ `inactive` en la unit y 3 × `inactive` en las instancias.
- **masscan gobernado** — `--source-ip 74.208.94.91 --adapter eth0`, `--min-rate`/`--max-rate` de la ventana, `nice -n 10` e `ionice -c3` (A1-14).
- **`nodes.public_ip` por fin escrita (A1-04)** — `cmd/botnet-api/repo/repo.go:39-47` (`InsertNode`) y `UpdateHeartbeat` rellenan la columna desde la IP observada en el `201` del enroll y en el primer heartbeat; el purge (`cmd/botnet-api/jobs/purge.go:41`) solo la anula a los 13 meses. Sin esto el anti-join de opt-out de `internal/discovery/select.go:25,30` está muerto.
- **Healthcheck y rollback por escalones** — `GET 127.0.0.1:9110/healthz` da 503 si Postgres no responde, si `now() - recruit_control.tick_at > 90 s`, si falta `masscan`/`zgrab2` o si `pps_effective = 0` con `paused=false`; rollback: `POST /v1/control/pause` → `systemctl stop 'botnet-recruit-worker@*'` → parar el orquestador y binario al revés (rename atómico) → `scripts/field/revoke_mass.sh`. El DDL es aditivo y sin `down`.

## Fuera de alcance

- El catálogo de métricas y el paraguas que las lee son EP-20; aquí el servicio solo las expone en `:9110`.
- Las 5 oleadas reales y los 20 nodos son EP-21; aquí el despliegue se prueba con la flota de ensayo.
- No se introduce PgBouncer: la decisión del blueprint es `--db-pool=2` por unidad y preflight medido (A2-01).
- No se toca `deploy/docker-compose.yml` ni se fija `max_connections` en el servidor: se **mide** y se adapta el pool.

## Dependencias

- **EP-16** y **EP-15**: sin binario `botnet-recruit` y sin 0009 no hay nada que desplegar.
- **EP-18**: el `/healthz` y el `ExecStartPre` dan por hecho el binario de clase A con el tag y el gw escuchando en `:1080`.
- Desbloquea EP-20 (las comprobaciones `servicio`, `gates` y `evidencia` leen systemd y `/healthz`) y EP-21 (las oleadas se corren aquí).
- Con `EP-17` en paralelo: el worker y su plantilla se prueban juntos el mismo día.

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| `FATAL 53300` por exceso de conexiones en el Postgres compartido (A2-01) | alta | crítico | `--db-pool=2` por unidad (8 + 8 del api de ~86 disponibles), preflight y `pg_stat_activity <= 20` |
| Crash-loop que deja la unidad en `failed` con `is-enabled=enabled` (A2-05, cadena C) | media | alto | `StartLimitIntervalSec=0` en unit y plantilla; el gate mide instancias y exige `NRestarts >= 1` |
| masscan huérfano sobreviviendo al stop y emitiendo pps extra (A2-04) | media | alto | `KillMode=control-group` + `TimeoutStopSec=20` y `pgrep -c masscan` = 0 tras el stop |
| Falso verde del cierre por apuntar al target (A2-06) | media | medio | plantilla habilitada + `is-active 'botnet-recruit-worker@*'` |
| Un secreto viajando en la línea de `ExecStart` y leyéndose con `ps` | media | medio | todo en `/etc/botnet-recruit/env` con `0640 root:botnet` |
| El arranque en frío con la cola al máximo dispara pps (riesgo 2 del blueprint §12) | media | medio | primer tick con `pps_target <= 1500` y `pgrep -c masscan <= 1` |

## Criterio de salida de la épica

`systemctl is-enabled botnet-recruit && systemctl is-active 'botnet-recruit-worker@*'` da **enabled** y 3 × **active**; `psql -Ac "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()"` ≤ **20**; `journalctl -u 'botnet-recruit*' --since -5m | grep -c 53300` = **0**; `psql -tAc "SELECT count(*) FROM nodes WHERE public_ip IS NULL AND status IN ('enrolled','live')"` = **0**; y `curl -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **200** en régimen normal, **503** con el latido congelado.

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-1901 | `bootstrap_vps.sh`: paquetes, usuario, env 0640 y preflight con salida 2 | 8 h |
| T-1902 | `recruit.slice` y `botnet-recruit.service` con techo, watchdog y `--db-pool=2` | 8 h |
| T-1903 | Plantilla `botnet-recruit-worker@.service` con cierre por instancias | 6 h |
| T-1904 | Escritura de `nodes.public_ip` en el enroll y el primer heartbeat | 6 h |
| T-1905 | `/healthz` con reglas de 503 y rollback por escalones | 8 h |
