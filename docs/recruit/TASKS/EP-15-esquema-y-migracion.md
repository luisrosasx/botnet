---
artifact: tasks
epic: EP-15
created: 2026-11-17
---

### T-1501 — Migración única 0009 bajo `pg_advisory_lock(6009)` con timeouts
- **Épica:** EP-15
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `grep -c 'pg_advisory_lock(6009)' migrations/migrations.go` = **1** o más, y el bloqueo se toma **antes** del bucle de `Apply` (`migrations/migrations.go:57`): la prueba concurrente `(./bin/botnet-api migrate & ./bin/botnet-recruit migrate & wait)` sobre base limpia sale **0** en los dos procesos y `grep -c 'already exists'` sobre la salida conjunta = **0**.
  2. `grep -cE "^SET LOCAL (lock_timeout|statement_timeout)" migrations/0009_recruit.up.sql` = **2** (`lock_timeout='2s'` y `statement_timeout='60s'`) y `grep -cE 'ALTER TABLE (ONLY )?nodes' migrations/0009_recruit.up.sql` = **0** (la tabla caliente no se toca).
  3. `grep -cE '^CREATE (TABLE|INDEX|UNIQUE INDEX)' migrations/0009_recruit.up.sql` = el 100 % con `IF NOT EXISTS`: `psql -tAc "SELECT count(*) FROM pg_tables WHERE tablename LIKE 'recruit_%'"` repite **11** tras aplicar dos veces, y `psql -tAc "SELECT count(*) FROM schema_migrations WHERE version LIKE '0009%'"` = **1** (una sola 0009, no dos como con los dos 0007).
  4. Sin `btree_gist`: `grep -c 'btree_gist' migrations/0009_recruit.up.sql` = **0** y `psql -d "$DSN_ROL_BOTNET" -c '\d recruit_candidates'` sale **0** con el rol `botnet` de `deploy/docker-compose.yml` (no superusuario).
- **Depende de:** — (raíz del plan de reclutamiento)
- **Estimación:** 4 h
- **DoD:** un solo `0009_recruit.up.sql`, aplicable dos veces seguidas y por dos procesos a la vez sin carrera, sin `ACCESS EXCLUSIVE` sobre `nodes` y sin pedir superusuario.
- **Ficheros:** `migrations/0009_recruit.up.sql`, `migrations/migrations.go`, `migrations/migrations_test.go`
- **Cierra:** A2-02, A2-07

### T-1502 — `recruit_candidates`, `recruit_candidate_events` y `recruit_jobs` con cola, lease y fencing
- **Épica:** EP-15
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `psql -tAc "SELECT indexdef FROM pg_indexes WHERE indexname='recruit_candidate_key'"` contiene `UNIQUE (ip, port)` (`grep -c 'UNIQUE (ip, port)'` = **1**) y la misma fila insertada dos veces con `ON CONFLICT (ip,port) DO NOTHING` deja `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE ip='203.0.113.7' AND port=7547"` = **1** con salida **0** (idempotencia de alta).
  2. `psql -tAc "SELECT count(*) FROM pg_indexes WHERE indexname IN ('recruit_q_claim','recruit_q_lease','recruit_q_state','recruit_q_node','recruit_q_verified')"` = **5**, y `EXPLAIN SELECT candidate_id FROM recruit_candidates WHERE state IN ('new','eligible','deferred') ORDER BY next_attempt_at LIMIT 5` menciona `recruit_q_claim` (`grep -c 'recruit_q_claim'` = **1**).
  3. Dominios cerrados: `psql -c "INSERT INTO recruit_candidates (ip,port,state) VALUES ('203.0.113.8',22,'leased')"` falla con `23514` (salida **≠ 0**) y los **14** estados del blueprint §5 se insertan y borran con salida **0** (14/14); lo mismo con los **11** valores de `fail_reason`.
  4. `psql -tAc "SELECT indexdef FROM pg_indexes WHERE indexname='recruit_job_attempt_key'"` contiene `UNIQUE (candidate_id, stage, attempt)` (`grep -c` = **1**) y `psql -tAc "SELECT count(*) FROM pg_constraint WHERE conname='recruit_verified_requires_node'"` = **1**: un `UPDATE ... SET state='verified'` con `node_id` nulo falla con `23514` (salida **≠ 0**).
- **Depende de:** T-1501
- **Estimación:** 6 h
- **DoD:** la autoridad de estado, la cola y el lease existen con sus claves de idempotencia y sus índices parciales; la auditoría por transición y el fencing tienen dónde escribir.
- **Ficheros:** `migrations/0009_recruit.up.sql`, `migrations/migrations_test.go`, `testdata/recruit/candidates_fixture.sql`
- **Cierra:** V2-1 (esquema), A3-04 (columna `verified_at`), A1-02 (columnas `cred_protocol`/`cred_ref`), V2-2 (columnas de lease)

### T-1503 — `recruit_windows`, `recruit_window_policy`, `recruit_control` y `recruit_planner_ticks` con unidad tipada
- **Épica:** EP-15
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `psql -tAc "SELECT count(*) FROM pg_constraint WHERE conname='recruit_windows_unit_check'"` = **1** y `psql -c "INSERT INTO recruit_windows (window_start, unit, pps_budget, conn_budget, attempt_budget) VALUES (date_trunc('hour',now()),'gbit',1500,30,60)"` falla con `23514` (salida **≠ 0**): la unidad solo admite `pps|conn|attempt` `[injerto: V3]`.
  2. `psql -tAc "SELECT count(*) FROM recruit_control"` = **1** tras la migración (singleton `id=1`) y `psql -c "INSERT INTO recruit_control (id) VALUES (2)"` falla con `23514` (salida **≠ 0**); `psql -tAc "SELECT is_nullable FROM information_schema.columns WHERE table_name='recruit_control' AND column_name='tick_at'"` = **YES** (el latido admite nulo hasta el primer tick).
  3. `psql -tAc "SELECT count(*) FROM recruit_window_policy"` = **24** y `psql -tAc "SELECT rate_class FROM recruit_window_policy WHERE hour_utc IN (0,3,12) ORDER BY hour_utc"` = `0`, `3`, `1` (semilla del §7: 22-02 h = 0, 02-06 h = 3, 06-22 h = 1).
  4. `psql -c "INSERT INTO recruit_windows (window_start, pps_budget, pps_used, pps_granted) VALUES (now(),1000,6001,1000)"` falla con `23514` por `recruit_budget_debt` (salida **≠ 0**) y `psql -c "INSERT INTO recruit_planner_ticks (tick_at, rate_class, action, reason) VALUES (now(),1,'wobble','x')"` falla igual: `action` solo admite `up|down|hold|paused|blocked`.
- **Depende de:** T-1501
- **Estimación:** 4 h
- **DoD:** el ritmo tiene dónde vivir con la unidad tipada, la deuda con techo, el singleton con latido y la decisión escrita por tick.
- **Ficheros:** `migrations/0009_recruit.up.sql`, `migrations/migrations_test.go`
- **Cierra:** V3-2, V1-2 (columna `tick_at`), A2-13 (techo de deuda)

### T-1504 — `recruit_socks5_checks` con único ganador y evidencia dura; `recruit_exclusions`, `recruit_waves`, `recruit_source_ip`
- **Épica:** EP-15
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `psql -tAc "SELECT indexdef FROM pg_indexes WHERE indexname='recruit_socks5_one_win'"` contiene `UNIQUE (candidate_id) WHERE ok` (`grep -c 'WHERE ok'` = **1**): tras insertar **5** filas `ok=true` del mismo `candidate_id`, solo la primera se acepta — `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE ok"` = **1**, `... WHERE candidate_id=$1` = **5** y el segundo `INSERT` devuelve `23505` (salida **≠ 0**) (A3-02).
  2. `psql -tAc "SELECT count(*) FROM pg_constraint WHERE conname='recruit_socks5_ok_evidence'"` = **1** con las cuatro condiciones: `http_status=200`, `bytes_in=0`, `latency_ms=1500`, `ip_match=true` falla (`23514`, salida **≠ 0**); `http_status=200`, `bytes_in=1`, `latency_ms=1501`, `ip_match=true` falla; y `bytes_in=1`, `latency_ms=1500`, `ip_match=true`, `http_status=200` pasa (salida **0**) `[injerto: V1]`.
  3. `psql -tAc "SELECT is_nullable FROM information_schema.columns WHERE table_name='recruit_socks5_checks' AND column_name='node_id'"` = **NO** y `psql -tAc "SELECT count(*) FROM pg_constraint WHERE conname='recruit_socks5_key'"` = **1** (`UNIQUE (candidate_id, attempt)`): dos filas con el mismo `(candidate_id, attempt)` dan `23505` (A1-03).
  4. Las tres tablas de gobierno nacen con su dominio: `psql -tAc "SELECT count(*) FROM pg_constraint WHERE conname IN ('recruit_exclusion_key','recruit_waves_state_check','recruit_exclusions_category_check')"` = **3**, y `psql -tAc "SELECT count(*) FROM pg_indexes WHERE tablename='recruit_windows' AND indexname='recruit_windows_open'"` = **1** (la ventana abierta se encuentra sin barrido).
- **Depende de:** T-1501
- **Estimación:** 4 h
- **DoD:** la evidencia del SOCKS5 no se puede rellenar a mano: sin `bytes_in>0`, sin latitud plausible y sin IP de salida coincidente no hay `ok=true`, y un candidato solo puede tener un intento ganador.
- **Ficheros:** `migrations/0009_recruit.up.sql`, `migrations/migrations_test.go`
- **Cierra:** A3-02, V1-1 (esquema), A3-03 (esquema), A1-05 (`recruit_source_ip`), A3-05 (`recruit_waves`), A1-12 (`recruit_exclusions`)

### T-1505 — Arreglo del SKIP del DDL (gate G4): `DATABASE_URL` obligatorio, salida 2 sin DSN
- **Épica:** EP-15
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. Con `DATABASE_URL` definido, `go test ./migrations/ -v 2>&1 | grep -c -- '--- SKIP'` = **0** (hoy **1**) y `go test ./migrations/ 2>&1 | grep -c '^ok'` = **1**: las 11 tablas de la 0009 se aplican de verdad en el test (A3-12).
  2. Sin `DATABASE_URL`, la prueba deja de ser verde muda: `DATABASE_URL= go test ./migrations/ 2>&1 | grep -c -- '--- FAIL'` = **1** (hoy sale `ok` con 1 `SKIP`) y el mensaje del fallo menciona `DATABASE_URL` (`grep -c 'DATABASE_URL'` ≥ **1**).
  3. Las restricciones se prueban por inserción negativa, no por lectura: el test aserta **≥ 5** rechazos (`state` inválido, `ok=true` con `bytes_in=0`, segundo ganador `ok`, `(ip,port)` duplicado, `unit='gbit'`) y `psql -tAc "SELECT count(*) FROM pg_constraint WHERE conname LIKE 'recruit_%'"` sobre la base de prueba ≥ **12**.
  4. La suite general no se rompe: `go build ./... && go test ./... 2>&1 | grep -c '^FAIL'` = **0** y `go test ./migrations/ -run TestApplyOnCleanDatabase 2>&1 | grep -c 'concurrente\|advisory'` ≥ **1** (la prueba de las dos aplicaciones concurrentes vive en el test).
- **Depende de:** T-1501, T-1502, T-1503, T-1504
- **Estimación:** 2 h
- **DoD:** la 0009 se ejecuta en un Postgres real en CI o el test lo impide; ningún verde vuelve a esconder una migración nunca aplicada.
- **Ficheros:** `migrations/ep08_ddl_test.go`, `migrations/recruit_ddl_test.go`
- **Cierra:** A3-12, gate G4
