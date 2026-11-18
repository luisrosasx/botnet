---
artifact: epic
id: EP-16
title: "Orquestador, máquina de estados y planificador del ritmo"
owner_layer: recruit
closes: A1-13, A2-03, A2-04, A2-09, A2-10, A2-11, A2-12, A2-13, A2-17, A3-04, A3-16, injertos V1-2, V1-4, V2-1, V2-2, V2-3, V2-4, V3-4
estimate_days: 6.25
risk: high
phase: F7
---

## Objetivo

Un solo proceso —`botnet-recruit`— que sea la autoridad del estado y del ritmo: la máquina de 14 estados con auditoría por transición, la cola durable en Postgres con claim atómico `FOR UPDATE SKIP LOCKED`, el lease por fase con `RenewLease()` y commit con fencing, las ventanas-hora UTC auto-ruedantes con granel y deuda acotada, y el lazo de histéresis que sube una clase y baja una clase con la decisión escrita. El orquestador no es un permiso para existir: si cae, el worker reclama directo y baja al suelo de 1500 pps; si vive pero calla, `/healthz` se pone en 503.

## Alcance

- `cmd/botnet-recruit/main.go`: proceso con las 6 banderas `--config`, `--db-pool`, `--roll-window`, `--once`, `--explain-tick`, `--sweep-json`, y el subcomando `wave close` (su escritura vive en EP-21).
- `internal/recruit/state.go` + `state_test.go`: los 14 estados del blueprint §5 y los 8 caminos de transición, con `age_clock_at` que no avanza con `outcome='skipped'` `[injerto: V2]` y reintento `deferred` con respaldo `2^stage_attempts` h.
- `internal/recruit/store.go` + `store_test.go`: claim atómico sobre el índice parcial `recruit_q_claim`, lease por fase (`sweep=30s`, `cred=300s`, `deliver=900s`), `RenewLease()` cada 60 s y commit con `WHERE lease_owner=$1 AND lease_token=$3`; `idempotency_key` por sha256 de `ip:port:stage:attempt`.
- `internal/recruit/budget.go` + `budget_test.go`: ventanas alineadas a la hora UTC, compra por granel con deuda ≤ 5000 pps y tope de 3 intentos por IP de destino y hora compartido entre `cred` y `deliver` (A1-13).
- `internal/recruit/planner.go` + `planner_test.go` con el fixture `testdata/recruit/planner_windows.json`: histéresis de una clase por tick con cooldown de 3 ventanas, freno de embudo `enroll_backlog > 500` `[injerto: V1]`, `host_fallback` con `load1<=6` y `RECRUIT_PPS_FIXED` que congela el lazo `[injerto: V3]`.
- `internal/recruit/httpapi.go` + `httpapi_test.go` y `internal/recruit/db.go`: `/healthz`, `/metrics`, `POST /v1/work/{claim,complete}` y `POST /v1/control/{pause,resume}` en el puerto propio **9110** (A2-17).
- `deploy/systemd/botnet-recruit-worker@.service`: la unidad que demuestra el fallback del worker (`RECRUIT_WORKER_CLAIM_FALLBACK=1`) y el reclaim del lease.

Fuera de alcance: el SOCKS5 que produce la evidencia de `verified` es EP-18; los workers que ejecutan las etapas son EP-17; la comprobación `lazo` del paraguas es EP-20.

## Cierre

- **T-1601 AC1** — `go build -o bin/botnet-recruit ./cmd/botnet-recruit && ./bin/botnet-recruit --help` sale **0** y lista las **6** banderas (`grep -cE '^ +(--)?(config|db-pool|roll-window|once|explain-tick|sweep-json)\b'` = **6**).
- **T-1601 AC2** — `curl -s -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **200** con latido fresco y **503** tras `UPDATE recruit_control SET tick_at = now() - interval '2 min'`; el cuerpo imprime las 5 claves `status`, `db`, `masscan`, `tick_age_s`, `rate_class`.
- **T-1601 AC3** — `psql -tAc "SELECT (now() - tick_at) < interval '90 s' FROM recruit_control"` = **t** en 5/5 muestras separadas 30 s y `botnet_recruit_orchestrator_tick_age_seconds` < **90** en `curl -s 127.0.0.1:9110/metrics`.
- **T-1601 AC4** — `POST /v1/control/pause` deja `paused` = **t** con 0 `claim` nuevos en 60 s y `resume` la devuelve a **f** sin reiniciar el proceso (mismo `pgrep -f botnet-recruit`).
- **T-1602 AC1** — `go test ./internal/recruit/ -run TestStateTable` ok con los **14** estados y `go test ./internal/recruit/ -run TestTransition -v | grep -c -- '^--- PASS'` ≥ **8** (un test por camino del §5).
- **T-1602 AC2** — cada transición escribe **una** fila en `recruit_candidate_events` con `from_state`/`to_state` y `actor` no vacío (`count(*) WHERE actor=''` = **0**) y `reason` en los 4 estados terminales.
- **T-1602 AC3** — `enrolling` fallido ⇒ `state='deferred'` con `next_attempt_at` en el futuro y `(next_attempt_at - now())/3600` entre **7** y **9** h con `stage_attempts=3`.
- **T-1602 AC4** — 10 ciclos con `outcome='skipped'` dejan `count(DISTINCT age_clock_at)` = **1** y `state='eligible'`; con 1 ciclo `ok` pasa a **2**; `fail_reason='age'` solo con > 14 días y **nunca** con `no_budget` (`count(*)` de esa combinación = **0**) `[injerto: V2]`.
- **T-1603 AC1** — `go test ./internal/recruit/ -run TestClaimAtomic -count=100 2>&1 | grep -c '^ok'` = **1** con 0 filas comunes entre reclamadores y `grep -c 'FOR UPDATE SKIP LOCKED' internal/recruit/store.go` ≥ **1** `[injerto: V2]`.
- **T-1603 AC2** — `psql -tAc "EXPLAIN (FORMAT TEXT) SELECT candidate_id FROM recruit_candidates WHERE state IN ('new','eligible','deferred') ORDER BY next_attempt_at LIMIT 5"` → `grep -c 'recruit_q_claim'` = **1** y `grep -c 'Seq Scan on recruit_candidates'` = **0**.
- **T-1603 AC3** — con 5.000 candidatos y 4 reclamadores, el p95 de `botnet_recruit_claim_latency_ms` ≤ **250 ms** en 1.000 claims con 0 reclamos perdidos.
- **T-1603 AC4** — `systemctl restart botnet-recruit` no pierde cola: `count(*)` de `state IN ('eligible','deferred')` coincide con el previo (diferencia **0** filas).
- **T-1604 AC1** — `go test ./internal/recruit/ -run TestLeaseDurationByPhase -v` ok con `sweep=30s`, `cred=300s`, `deliver=900s` (A2-04, sin lease único de 300 s).
- **T-1604 AC2** — `journalctl -u botnet-recruit --since '-10m' | grep -c 'lease_renew'` ≥ **9** y `count(*) WHERE lease_owner IS NOT NULL AND lease_expires_at < now()` = **0** en un barrido de 216 s.
- **T-1604 AC3** — el perdedor del fencing no escribe: su `lease_token` no coincide, `grep -c 'lease_stale'` en el log ≥ **1** y el commit lleva `WHERE lease_owner=$1 AND lease_token=$3`.
- **T-1604 AC4** — `pkill -f 'masscan --'; systemctl restart botnet-recruit; sleep 240` ⇒ `count(*) WHERE lease_expires_at < now()` = **0** y `pgrep -c masscan` ≤ **1** (prueba A2-04 del §2).
- **T-1605 AC1** — `POST /v1/work/claim -d '{"stage":"sweep","worker":"probe-1","limit":5}'` devuelve **5** `candidate_id` con **200** y el `complete` deja `finished_at` no nulo con `outcome IN ('ok','fail','timeout','skipped')` en 5/5.
- **T-1605 AC2** — repetir el `complete` con el mismo `idempotency_key` deja `count(*)` por clave = **1**, 0 eventos nuevos y salida **200**.
- **T-1605 AC3** — con el orquestador parado, `RECRUIT_WORKER_CLAIM_FALLBACK=1 ./bin/botnet-probe --claim --once` sale **0**, imprime `pps=1500` y deja `lease_owner` no nulo.
- **T-1605 AC4** — con `paused=true`, `POST /v1/work/claim` responde **409** o **204** (nunca 200 con carga) y `count(*)` de `recruit_jobs` reclamados en 60 s = **0**.
- **T-1606 AC1** — `count(*) FROM recruit_windows WHERE date_trunc('hour', window_start) <> window_start` = **0** y ningún `closed_at <= window_start`.
- **T-1606 AC2** — con la ventana gastada y el orquestador parado, en ≤ **60 s** aparece la ventana nueva (`closed_at IS NULL` = **1**) y `botnet-recruit --roll-window --once` es idempotente (0 filas nuevas la segunda vez).
- **T-1606 AC3** — con demanda de 3.000 SYN y `pps_budget=1500`: `pps_debt` ≤ **5000** y `pps_effective/pps_budget` ≥ **0,90** en la ventana siguiente; `botnet_recruit_budget_unavailable_total` crece ≤ 5000 pps por ventana.
- **T-1606 AC4** — la 4ª tentativa de `cred`/`deliver` sobre la misma IP destino en la misma hora se rechaza: `count(*)` de jobs por destino y hora = **3** (compartido entre etapas, A1-13).
- **T-1607 AC1** — fixture de 3 ventanas sanas ⇒ el último `recruit_planner_ticks` da `action='up'` con `rate_class` **+1** exacto; con `probes_15m=199` da `hold` con `reason='denominador_corto'`.
- **T-1607 AC2** — incumplir una métrica ⇒ `down` con **-1** exacto, 0 `up` en las 3 ventanas siguientes y 0 violaciones del mínimo de 6 ventanas entre subidas.
- **T-1607 AC3** — con **501** filas en `staging|enrolling` el tick da `hold` con `reason='enroll_backlog'` y `pps_effective` = **0**; con **499** vuelve a promover `[injerto: V1]`.
- **T-1607 AC4** — sin `host_cpu_pct` fiable el tick queda con `host_fallback=t` y acepta `load1<=6` (`count(*)` con `host_fallback` en 1 h ≥ **1**); `RECRUIT_PPS_FIXED=1200` deja `count(DISTINCT pps_target)` = **1** `[injerto: V3]`.
- **T-1607 AC5** — `UPDATE recruit_control SET manual_rate_class=0` hace que el siguiente tick dé `action='paused'` o `rate_class=0` con `reason` no vacío (salida de emergencia auditada).
