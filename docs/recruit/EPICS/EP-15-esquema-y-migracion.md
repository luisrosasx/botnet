---
artifact: epic
id: EP-15
title: Esquema Postgres del reclutamiento y migración 0009 sin carrera
phase: F1
risk: high
estimate_days: 2.5
closes: A2-02, A3-02 (esqueleto), A1-05 (tabla), injerto V3-2
artifacts:
  - migrations/0009_recruit.up.sql
  - migrations/migrations.go
  - migrations/ep08_ddl_test.go
  - testdata/recruit/
---

## Objetivo

Que el reclutamiento tenga un único suelo de datos autoritativo: un solo fichero `migrations/0009_recruit.up.sql` con las 11 tablas del blueprint §6, aplicado bajo `pg_advisory_lock(6009)`, todo con `IF NOT EXISTS`, sin `ALTER` sobre `nodes` (la tabla caliente del heartbeat) y con `SET LOCAL lock_timeout='2s'` + `statement_timeout='60s'`. Y que ese esquema se pruebe de verdad: hoy `migrations/ep08_ddl_test.go:24` hace `t.Skip` sin `DATABASE_URL` y `go test ./...` sale 0 con la 0009 nunca aplicada (gate G4 abierto).

## Alcance

- Un solo `0009_recruit.up.sql` (no dos 0009 como ya ocurre con `0007_node_heartbeats` y `0007_rgpd`) bajo `pg_advisory_lock(6009)` en `migrations.Apply` (`migrations/migrations.go:57`).
- Autoridad de estado y cola: `recruit_candidates` con `UNIQUE (ip,port)` (`recruit_candidate_key`), los 14 estados de `state` con `CHECK`, `lease_owner`/`lease_expires_at`/`lease_token` (fencing), `cred_protocol`/`cred_ref`, `node_id`, `verified_at`, `age_clock_at`, `next_attempt_at`, `stage_attempts`, `fail_reason` con dominio cerrado de 11 valores, y `recruit_verified_requires_node`.
- Los tres índices de cola/lease/cierre: `recruit_q_claim` (parcial sobre `next_attempt_at` donde `state IN ('new','eligible','deferred')`), `recruit_q_lease`, `recruit_q_state`, más `recruit_q_node` y `recruit_q_verified`.
- Auditoría por transición: `recruit_candidate_events` con `actor` y `recruit_event_key UNIQUE (candidate_id, seq)`.
- Ritmo y gobierno: `recruit_windows` con la unidad tipada `unit CHECK (unit IN ('pps','conn','attempt'))` y `recruit_budget_debt`, `recruit_window_policy` por hora UTC, `recruit_control` (singleton `id=1` con `tick_at`), `recruit_planner_ticks` con `action IN ('up','down','hold','paused','blocked')` y retención de 7 días al patrón 0005.
- Trabajo, evidencia y gobierno de bajo ruido: `recruit_jobs` con doble clave de idempotencia (`recruit_job_key`, `recruit_job_attempt_key UNIQUE (candidate_id, stage, attempt)`), `recruit_socks5_checks` con `recruit_socks5_key UNIQUE (candidate_id, attempt)`, `recruit_socks5_ok_evidence` y el índice parcial único `recruit_socks5_one_win`, `recruit_exclusions`, `recruit_waves`, `recruit_source_ip`.
- Cero dependencia de `btree_gist` (el rol `botnet` de `deploy/docker-compose.yml` no es superusuario: A2-07).
- Arreglo G4: `TestApplyOnCleanDatabase` sin `SKIP` silencioso — con `DATABASE_URL` debe aplicar y asertar; sin él, la prueba del paraguas sale **2**, no 0.

## Fuera de alcance

- El spike del VPS (paquetes, `/etc/botnet-recruit/env`, sweep de 254 hosts con `--source-ip`/`--adapter`) vive en EP-19; aquí solo el DDL y su prueba.
- No se toca `nodes`: `public_ip`, `status`, `consent_at`, `optout_at` y `monthly_gb_cap` ya están en `0003_core.up.sql`, así que 0009 no toma `ACCESS EXCLUSIVE` sobre la tabla del heartbeat.
- No hay migración `down`: el DDL es aditivo y un rollback de código nunca revierte esquema (blueprint §9).
- La lógica de presupuesto, lease y ventana (quién escribe esas columnas) es EP-16; aquí solo existen y se validan.

## Dependencias

- Ninguna: es la raíz del plan de reclutamiento. EP-16, EP-17, EP-18, EP-19, EP-20 y EP-21 leen este esquema.
- Hacia atrás: `migrations.Apply` ya la usan `botnet-api` y `botnet-discover`; el cambio en `Apply` (advisory lock) afecta a los dos binarios y lo comprueba T-1501.

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| Dos procesos aplican 0009 a la vez (`botnet-api` llama `Apply` en `cmd/botnet-api/main.go:101` y `:121`) | alta | crash-loop con `relation already exists` | `pg_advisory_lock(6009)` + `IF NOT EXISTS` (T-1501, A2-02) |
| Un `REFERENCES nodes` toma `ACCESS EXCLUSIVE` y bloquea el heartbeat | media | alto | sin `ALTER TABLE nodes` en 0009; `lock_timeout='2s'` hace que el DDL falle antes de bloquear |
| El DDL de 0009 no se prueba nunca (G4) y una `CHECK` queda mal escrita | alta | medio | T-1505 con `DATABASE_URL` obligatorio y salida 2 sin DSN |
| Tabla de un dominio mal cerrado (`fail_reason` con 11 valores) | media | medio | prueba de inserción con valor fuera de dominio ⇒ `23514` |

## Criterio de salida de la épica

`bash scripts/check-recruit.sh --only ddl` (o `go test ./migrations/ -v`) sobre una base limpia deja `psql -tAc "SELECT count(*) FROM schema_migrations WHERE version LIKE '0009%'"` = **1**, `grep -c 'already exists'` = **0** en dos aplicaciones concurrentes, `psql -tAc "SELECT count(*) FROM pg_tables WHERE tablename LIKE 'recruit_%'"` = **11** y cero `--- SKIP` en `./migrations/`.

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-1501 | Migración única 0009 bajo `pg_advisory_lock(6009)` con timeouts | 4 h |
| T-1502 | `recruit_candidates`, `recruit_candidate_events` y `recruit_jobs` con cola, lease y fencing | 6 h |
| T-1503 | `recruit_windows`, `recruit_window_policy`, `recruit_control` y `recruit_planner_ticks` con unidad tipada | 4 h |
| T-1504 | `recruit_socks5_checks` con único ganador y evidencia dura; `recruit_exclusions`, `recruit_waves`, `recruit_source_ip` | 4 h |
| T-1505 | Arreglo del SKIP del DDL (gate G4): `DATABASE_URL` obligatorio, salida 2 sin DSN | 2 h |
