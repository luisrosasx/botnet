---
artifact: epic
id: EP-15
title: "Esquema y migración única del reclutamiento (0009)"
owner_layer: recruit
closes: A1-02, A1-05, A1-12, A2-02, A2-07, A2-13, A3-02, A3-03, A3-04, A3-05, A3-12, injertos V1-1, V1-2, V2-1, V2-2, V3-2, gate G4
estimate_days: 2.5
risk: medium
phase: F7
---

## Objetivo

Que el reclutamiento tenga dónde escribir antes de mover un solo SYN: una única migración `0009_recruit.up.sql` con las once tablas `recruit_*` (estado y cola del candidato, auditoría por transición, ventanas y política de ritmo, singleton de control, evidencia SOCKS5 y gobierno) aplicable dos veces y por dos procesos a la vez sin carrera, sin `ALTER` de la tabla caliente `nodes`, sin `btree_gist` y sin pedir superusuario. Incluye el arreglo del gate G4: la migración deja de ser un `SKIP` verde-mudo y exige `DATABASE_URL`.

## Alcance

- `migrations/0009_recruit.up.sql`: `SET LOCAL lock_timeout='2s'` y `statement_timeout='60s'`, todo el DDL con `IF NOT EXISTS`; `recruit_candidates` (autoridad de estado, cola con `next_attempt_at`, lease con `lease_owner`/`lease_expires_at`/`lease_token`), `recruit_candidate_events` (auditoría con `actor` y `seq`), `recruit_jobs` (clave `UNIQUE (candidate_id, stage, attempt)` e `idempotency_key`).
- Índices parciales de cola: `recruit_q_claim`, `recruit_q_lease`, `recruit_q_state`, `recruit_q_node`, `recruit_q_verified`.
- `recruit_windows` con unidad tipada `unit IN ('pps','conn','attempt')` y techo de deuda; `recruit_window_policy` con las 24 horas sembradas (22-02 h = 0, 02-06 h = 3, 06-22 h = 1); `recruit_control` singleton `id=1` con `tick_at` nullable; `recruit_planner_ticks` con `action IN ('up','down','hold','paused','blocked')`.
- `recruit_socks5_checks` con `node_id NOT NULL`, `UNIQUE (candidate_id, attempt)` (A1-03), el único ganador `recruit_socks5_one_win` y la evidencia dura `recruit_socks5_ok_evidence` `[injerto: V1]`; `recruit_exclusions`, `recruit_waves`, `recruit_source_ip`.
- `migrations/migrations.go`: `pg_advisory_lock(6009)` tomado **antes** del bucle de `Apply`.
- Pruebas: `migrations/migrations_test.go`, `migrations/recruit_ddl_test.go`, `migrations/ep08_ddl_test.go` y el fixture `testdata/recruit/candidates_fixture.sql`.

Fuera de alcance: la máquina de estados en código y el lazo de ritmo son EP-16; los workers que rellenan las tablas son EP-17; el paraguas que las comprueba es EP-20.

## Cierre

- **T-1501 AC1** — `grep -c 'pg_advisory_lock(6009)' migrations/migrations.go` ≥ **1** tomado antes del `Apply`; `(./bin/botnet-api migrate & ./bin/botnet-recruit migrate & wait)` sobre base limpia sale **0** en ambos con `grep -c 'already exists'` = **0** (A2-02, A2-07).
- **T-1501 AC2** — `grep -cE "^SET LOCAL (lock_timeout|statement_timeout)" migrations/0009_recruit.up.sql` = **2** y `grep -cE 'ALTER TABLE (ONLY )?nodes' migrations/0009_recruit.up.sql` = **0**.
- **T-1501 AC3** — todo `CREATE` con `IF NOT EXISTS`: `psql -tAc "SELECT count(*) FROM pg_tables WHERE tablename LIKE 'recruit_%'"` repite **11** tras dos aplicaciones y `schema_migrations WHERE version LIKE '0009%'` = **1**.
- **T-1501 AC4** — `grep -c 'btree_gist' migrations/0009_recruit.up.sql` = **0** y `psql -d "$DSN_ROL_BOTNET" -c '\d recruit_candidates'` sale **0** con el rol no superusuario de `deploy/docker-compose.yml`.
- **T-1502 AC1** — `recruit_candidate_key` contiene `UNIQUE (ip, port)` y la misma fila con `ON CONFLICT (ip,port) DO NOTHING` deja `count(*)` de `recruit_candidates WHERE ip='203.0.113.7' AND port=7547` = **1** con salida **0**.
- **T-1502 AC2** — `count(*)` de `pg_indexes` entre los cinco índices de cola = **5** y el `EXPLAIN` del claim sobre `(state, next_attempt_at)` menciona `recruit_q_claim` (`grep -c` = **1**).
- **T-1502 AC3** — dominios cerrados: `state='leased'` falla con `23514` (salida **≠ 0**) y los **14** estados y los **11** `fail_reason` del blueprint §5 se insertan con salida **0**.
- **T-1502 AC4** — `recruit_job_attempt_key` contiene `UNIQUE (candidate_id, stage, attempt)` y `recruit_verified_requires_node` = **1**: poner `state='verified'` con `node_id` nulo falla con `23514`.
- **T-1503 AC1** — `recruit_windows_unit_check` = **1** y `unit='gbit'` falla con `23514`: la unidad solo admite `pps|conn|attempt` `[injerto: V3]`.
- **T-1503 AC2** — `recruit_control` nace con **1** fila (`id=1`), `INSERT (id) VALUES (2)` falla con `23514` y `tick_at` está `is_nullable = YES`.
- **T-1503 AC3** — `recruit_window_policy` tiene **24** filas y `rate_class` en las horas UTC 0/3/12 devuelve `0`, `3`, `1` (semilla del §7).
- **T-1503 AC4** — `pps_used=6001` sobre `pps_budget=1000` falla por `recruit_budget_debt` y `recruit_planner_ticks` rechaza un `action` fuera de `up|down|hold|paused|blocked` (techo de deuda, A2-13).
- **T-1504 AC1** — `recruit_socks5_one_win` contiene `UNIQUE (candidate_id) WHERE ok`: **5** inserciones `ok=true` del mismo candidato dejan `count(*) WHERE ok` = **1** y `count(*) WHERE candidate_id=$1` = **5**, con `23505` en la segunda (A3-02).
- **T-1504 AC2** — `recruit_socks5_ok_evidence` = **1** y rechaza `http_status=200`+`bytes_in=0` y `latency_ms=1501`; pasa `bytes_in=1`, `latency_ms=1500`, `ip_match=true`, `http_status=200` (salida **0**) `[injerto: V1]`.
- **T-1504 AC3** — `node_id` de `recruit_socks5_checks` es `is_nullable = NO` y `recruit_socks5_key` = **1** (`UNIQUE (candidate_id, attempt)` → `23505` al repetir el intento).
- **T-1504 AC4** — `count(*)` de `pg_constraint` entre `recruit_exclusion_key`, `recruit_waves_state_check` y `recruit_exclusions_category_check` = **3** y el índice parcial `recruit_windows_open` = **1**.
- **T-1505 AC1** — con `DATABASE_URL`: `go test ./migrations/ -v 2>&1 | grep -c -- '--- SKIP'` = **0** (hoy **1**) y `grep -c '^ok'` = **1** (gate G4).
- **T-1505 AC2** — sin `DATABASE_URL`: `DATABASE_URL= go test ./migrations/ 2>&1 | grep -c -- '--- FAIL'` = **1** y el mensaje menciona `DATABASE_URL`.
- **T-1505 AC3** — las restricciones se prueban por inserción negativa: **≥ 5** rechazos asertados y `count(*)` de `pg_constraint LIKE 'recruit_%'` ≥ **12**.
- **T-1505 AC4** — la suite general no se rompe: `go build ./... && go test ./... 2>&1 | grep -c '^FAIL'` = **0** y `go test ./migrations/ -run TestApplyOnCleanDatabase` menciona la prueba concurrente (`grep -c 'concurrente\|advisory'` ≥ **1**).
