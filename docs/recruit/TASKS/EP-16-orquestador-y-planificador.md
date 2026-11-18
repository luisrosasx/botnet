---
artifact: tasks
epic: EP-16
created: 2026-11-17
---

### T-1601 — Proceso y API del orquestador con `/healthz` por latido
- **Épica:** EP-16
- **Estado:** DONE | PRUEBA: `go test ./internal/recruit/` y evidencia en `docs/verification/EP-16.md`
- **Criterios de aceptación:**
  1. `go build -o bin/botnet-recruit ./cmd/botnet-recruit && ./bin/botnet-recruit --help` sale **0** y lista las 6 banderas: `--config`, `--db-pool`, `--roll-window`, `--once`, `--explain-tick`, `--sweep-json` (`grep -cE '^ +(--)?(config|db-pool|roll-window|once|explain-tick|sweep-json)\b'` = **6**).
  2. `curl -s -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **200** con el latido fresco y **503** tras `psql -c "UPDATE recruit_control SET tick_at = now() - interval '2 min'"` (hoy el servicio no existe; el criterio A2-03 exige el 503); `curl -s 127.0.0.1:9110/healthz` imprime las 5 claves `status`, `db`, `masscan`, `tick_age_s`, `rate_class` (`grep -cE '(status|db|masscan|tick_age_s|rate_class)='` = **5**).
  3. El latido se escribe: `psql -tAc "SELECT (now() - tick_at) < interval '90 s' FROM recruit_control"` = **t** en régimen estable, medida 5 veces separadas 30 s (5/5 en `t`), y `botnet_recruit_orchestrator_tick_age_seconds` < **90** en `curl -s 127.0.0.1:9110/metrics`.
  4. `POST /v1/control/pause` deja `psql -tAc "SELECT paused FROM recruit_control"` = **t** y 0 `claim` nuevos en 60 s; `POST /v1/control/resume` la devuelve a **f** (2/2) sin reiniciar el proceso (mismo PID antes y después: `pgrep -f botnet-recruit` idéntico).
- **Depende de:** T-1501, T-1503
- **Estimación:** 8 h
- **DoD:** hay un solo proceso que manda el ritmo y dice la verdad de su estado; «vivo pero mudo» se ve rojo, no verde.
- **Ficheros:** `cmd/botnet-recruit/main.go`, `internal/recruit/httpapi.go`, `internal/recruit/db.go`
- **Cierra:** V1-2, A2-03 (parte de 503), A2-17 (puerto 9110 propio)

### T-1602 — Máquina de estados de 14 estados con auditoría por transición
- **Épica:** EP-16
- **Estado:** DONE | PRUEBA: `go test ./internal/recruit/` y evidencia en `docs/verification/EP-16.md`
- **Criterios de aceptación:**
  1. `go test ./internal/recruit/ -run TestStateTable` ok y la tabla declara los **14** estados del blueprint §5 (`grep -cE "'(new|excluded|eligible|sweeping|bannered|assessed|staging|enrolling|enrolled|verified|deferred|discarded|burned|rolled_back)'" migrations/0009_recruit.up.sql` ≥ **14**); cada uno de los **8** caminos de la tabla §5 tiene su test de paso: `go test ./internal/recruit/ -run TestTransition -v | grep -c -- '^--- PASS'` ≥ **8**.
  2. Cada transición escribe exactamente una fila en `recruit_candidate_events`: `psql -tAc "SELECT count(*) FROM recruit_candidate_events WHERE candidate_id=$1 AND seq=(SELECT max(seq) FROM recruit_candidate_events WHERE candidate_id=$1) AND from_state='new' AND to_state='eligible'"` = **1**, con `actor` no vacío (`psql -tAc "SELECT count(*) FROM recruit_candidate_events WHERE actor='' "` = **0**) y `reason` presente en los **4** estados terminales del embudo.
  3. El reintento no es muerte: un `enrolling` fallido deja `state='deferred'` y `psql -tAc "SELECT (next_attempt_at - now()) > interval '0 s'"` = **t** con `2^stage_attempts` h de respaldo (`psql -tAc "SELECT extract(epoch FROM (next_attempt_at - now()))/3600 FROM recruit_candidates WHERE candidate_id=$1"` entre **7** y **9** con `stage_attempts=3`).
  4. El reloj de edad no corre con `skip` `[injerto: V2]`: tras **10** ciclos con `outcome='skipped'`, `psql -tAc "SELECT count(DISTINCT age_clock_at) FROM recruit_candidates WHERE candidate_id=$1"` = **1** (misma marca) y `state` sigue en `eligible`; con **1** ciclo `outcome='ok'` la marca cambia (`count(DISTINCT age_clock_at)` = **2**). Muere con `fail_reason='age'` solo cuando `now() - age_clock_at > interval '14 days'` (fixture: 1/1) y `no_budget` **nunca** produce `fail_reason='age'` (`count(*)` con esa combinación = **0**).
- **Depende de:** T-1502, T-1601
- **Estimación:** 8 h
- **DoD:** el candidato tiene estado, memoria y fecha de edad honestas; ningún descarte vuelve a ser un número redondo escrito a mano.
- **Ficheros:** `internal/recruit/state.go`, `internal/recruit/state_test.go`, `internal/recruit/store.go`
- **Cierra:** V2-3, A2-09, A3-04 (regla de `verified` en código)

### T-1603 — Cola durable con claim atómico `FOR UPDATE SKIP LOCKED`
- **Épica:** EP-16
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. Dos `claim` concurrentes por la misma fase devuelven conjuntos disjuntos: `go test ./internal/recruit/ -run TestClaimAtomic -count=100 2>&1 | grep -c '^ok'` = **1** y la aserción de solape da **0** filas comunes en 100 iteraciones; el SQL contiene `FOR UPDATE SKIP LOCKED` (`grep -c 'FOR UPDATE SKIP LOCKED' internal/recruit/store.go` ≥ **1**) `[injerto: V2]`.
  2. El claim usa el índice parcial, no un barrido: `psql -tAc "EXPLAIN (FORMAT TEXT) SELECT candidate_id FROM recruit_candidates WHERE state IN ('new','eligible','deferred') ORDER BY next_attempt_at LIMIT 5"` → `grep -c 'recruit_q_claim'` = **1** y `grep -c 'Seq Scan on recruit_candidates'` = **0**.
  3. Latencia del claim: con 5.000 candidatos en cola y 4 reclamadores, `curl -s 127.0.0.1:9110/metrics | awk '/^botnet_recruit_claim_latency_ms/{print $2}'` da p95 ≤ **250 ms** (medido en 1.000 claims, 0 reclamos perdidos).
  4. Nada se pierde al reiniciar: `systemctl restart botnet-recruit; sleep 5; psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state IN ('eligible','deferred')"` coincide con el conteo previo (diferencia **0** filas) — la cola está en Postgres, no en memoria (contraste con A2-12 de P3).
- **Depende de:** T-1502, T-1602
- **Estimación:** 6 h
- **DoD:** el repartido es atómico, usa el índice que toca y sobrevive al reinicio del proceso; dos workers no pueden hacer el mismo trabajo.
- **Ficheros:** `internal/recruit/store.go`, `internal/recruit/store_test.go`
- **Cierra:** V2-1, A2-12

### T-1604 — Lease por fase con `RenewLease()` y commit con fencing
- **Épica:** EP-16
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. Duraciones exactas por fase: `go test ./internal/recruit/ -run TestLeaseDurationByPhase -v` ok con `sweep=30s`, `cred=300s`, `deliver=900s` (`grep -cE 'sweep:30s|cred:300s|deliver:900s'` sobre la salida con `-v` = **3**); ninguna fase usa un lease único de 300 s (A2-04).
  2. `RenewLease()` cada 60 s: `journalctl -u botnet-recruit --since '-10m' | grep -c 'lease_renew'` ≥ **9** en diez minutos, y `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE lease_owner IS NOT NULL AND lease_expires_at < now()"` = **0** en un barrido de 216 s (la cohorte sobrevive a su lease).
  3. Fencing: un worker con `lease_token` viejo intenta el commit y no escribe — `psql -tAc "SELECT lease_token FROM recruit_candidates WHERE candidate_id=$1"` no coincide con el del perdedor, `grep -c 'lease_stale'` en el log ≥ **1** y el SQL del commit contiene `WHERE lease_owner=$1 AND lease_token=$3` (`grep -c 'lease_owner' internal/recruit/store.go` ≥ **1**).
  4. Reclaim medido: `pkill -f 'masscan --'; systemctl restart botnet-recruit; sleep 240; psql -tAc "SELECT count(*) FROM recruit_candidates WHERE lease_expires_at < now()"` = **0** y `pgrep -c masscan` ≤ **1** (prueba A2-04 del blueprint §2, con cero escrituras del dueño difunto).
- **Depende de:** T-1602, T-1603
- **Estimación:** 6 h
- **DoD:** un worker muerto libera la fila y un worker zombi no puede escribir encima del estado nuevo; el proceso más largo que el lease deja de ser una excepción.
- **Ficheros:** `internal/recruit/store.go`, `internal/recruit/store_test.go`, `deploy/systemd/botnet-recruit-worker@.service`
- **Cierra:** A2-04, V2-2

### T-1605 — `/v1/work/{claim,complete}` y `/v1/control/{pause,resume}` con fallback del worker
- **Épica:** EP-16
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `curl -s -X POST 127.0.0.1:9110/v1/work/claim -d '{"stage":"sweep","worker":"probe-1","limit":5}'` devuelve **5** `candidate_id` con salida **200**, y el `complete` correspondiente deja `psql -tAc "SELECT finished_at IS NOT NULL, outcome FROM recruit_jobs WHERE worker='probe-1'"` = **t** con `outcome IN ('ok','fail','timeout','skipped')` (5/5).
  2. `complete` duplicado no duplica el efecto: repetir el mismo `idempotency_key` deja `psql -tAc "SELECT count(*) FROM recruit_jobs WHERE idempotency_key=$1"` = **1** y `count(*)` de eventos nuevos = **0**, con salida **200** (idempotencia por sha256 de `ip:port:stage:attempt`).
  3. Fallback declarado y medido: con el orquestador parado (`systemctl stop botnet-recruit`), `RECRUIT_WORKER_CLAIM_FALLBACK=1 ./bin/botnet-probe --claim --once` sale **0**, reclama por `SKIP LOCKED` directo en `recruit_candidates` y emite a `pps_min` — el log imprime `pps=1500` (`grep -c 'pps=1500'` = **1**) y `lease_owner` queda no nulo en la fila reclamada.
  4. Con `paused=true`, `POST /v1/work/claim` responde **409** o **204** (nunca **200** con carga) y `psql -tAc "SELECT count(*) FROM recruit_jobs WHERE claimed_at > now() - interval '60 s'"` = **0** en el minuto siguiente.
- **Depende de:** T-1601, T-1603, T-1604
- **Estimación:** 6 h
- **DoD:** el orquestador reparte y recibe resultados por HTTP; si no está, el trabajo no se para, solo baja al suelo de 1500 pps.
- **Ficheros:** `internal/recruit/httpapi.go`, `internal/recruit/httpapi_test.go`, `internal/recruit/store.go`
- **Cierra:** A2-12 (mitad de fallback), A1-13 (tope de intentos por destino en el `complete`)

### T-1606 — Ventanas-hora UTC auto-ruedantes con política por hora y granel con deuda
- **Épica:** EP-16
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. Toda ventana nace alineada a la hora UTC: `psql -tAc "SELECT count(*) FROM recruit_windows WHERE date_trunc('hour', window_start) <> window_start"` = **0**, y `psql -tAc "SELECT count(*) FROM recruit_windows WHERE closed_at IS NOT NULL AND closed_at <= window_start"` = **0**.
  2. La compra rueda la ventana sola (A2-03): con `psql -c "UPDATE recruit_windows SET pps_used=pps_budget, window_start = window_start - interval '70 min'"` y el orquestador parado, en ≤ **60 s** aparece la ventana nueva (`psql -tAc "SELECT count(*) FROM recruit_windows WHERE closed_at IS NULL"` = **1**) y `psql -tAc "SELECT pps_used < pps_budget FROM recruit_windows WHERE closed_at IS NULL"` = **t**; `botnet-recruit --roll-window --once` es idempotente (segunda ejecución: 0 filas nuevas, salida **0**).
  3. Granel con deuda y techo: con `pps_budget=1500` y demanda de 3.000 SYN, `psql -tAc "SELECT pps_debt FROM recruit_windows WHERE closed_at IS NULL"` ≤ **5000** y `pps_effective/pps_budget` ≥ **0,90** en la ventana siguiente; `botnet_recruit_budget_unavailable_total` no crece sin límite (delta ≤ **5000** pps por ventana).
  4. Tope por destino `[injerto: V2]`: la 4ª tentativa de `cred` o `deliver` sobre la misma IP de destino dentro de la misma hora se rechaza — `psql -tAc "SELECT count(*) FROM recruit_jobs j JOIN recruit_candidates c USING (candidate_id) WHERE c.ip=$1 AND j.stage IN ('cred','deliver') AND date_trunc('hour', j.claimed_at)=date_trunc('hour', now())"` = **3** (compartido entre las dos etapas, A1-13).
- **Depende de:** T-1601, T-1603, T-1503
- **Estimación:** 8 h
- **DoD:** el presupuesto rueda solo, se gasta por granel con deuda acotada y nadie muere de presupuesto sin haber trabajado.
- **Ficheros:** `internal/recruit/budget.go`, `internal/recruit/budget_test.go`, `internal/recruit/planner.go`
- **Cierra:** A2-03, V2-4, A2-10, A2-13, A1-13

### T-1607 — Lazo de histéresis con cooldown, `host_fallback`, freno de embudo y `RECRUIT_PPS_FIXED`
- **Épica:** EP-16
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. Subida de una sola clase con las tres ventanas propias: fixture de 3 ventanas con `socks5_success_ratio>=0,95`, `pps_effective>=0,90*pps_budget`, `node_leave_ratio_24h<=0,05` y `probes_15m>=200` ⇒ `psql -tAc "SELECT action, rate_class FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 1"` = `up` con `rate_class` **+1** exacto; con `probes_15m=199` ⇒ `hold` con `reason='denominador_corto'` (1/1).
  2. Reversión de una clase y cooldown de 3: incumplir una métrica ⇒ el tick imprime `down` con **-1** exacto (`-2` imposible) y en las 3 ventanas siguientes `psql -tAc "SELECT count(*) FROM recruit_planner_ticks WHERE action='up' AND tick_at > $ventana_revertida"` = **0** (solo caben `hold` o `down`); nunca dos subidas en menos de 6 ventanas (fixture: **0** violaciones).
  3. Freno de embudo `[injerto: V1]`: con **501** filas en `staging|enrolling`, el tick da `action='hold'` y `reason='enroll_backlog'` (1/1) y `pps_effective` del tick = **0**; con **499** vuelve a promover (`action <> 'hold'` o `reason <> 'enroll_backlog'`).
  4. Fallback del host declarado: sin `host_cpu_pct` fiable, el tick queda con `host_fallback=t` y se acepta `load1<=6` — `psql -tAc "SELECT count(*) FROM recruit_planner_ticks WHERE host_fallback AND tick_at > now()-interval '1 h'"` ≥ **1** en el fixture de vecino ocupado; y `RECRUIT_PPS_FIXED=1200` congela el lazo con `psql -tAc "SELECT count(DISTINCT pps_target) FROM recruit_planner_ticks WHERE tick_at > now()-interval '1 h'"` = **1** `[injerto: V3]`.
  5. Salida de emergencia auditada: `psql -c "UPDATE recruit_control SET manual_rate_class=0"` hace que el siguiente tick dé `action='paused'` o `rate_class=0` (1/1) y la decisión queda escrita con `reason` no vacío.
- **Depende de:** T-1601, T-1606
- **Estimación:** 8 h
- **DoD:** la marcha sube despacio y baja de golpe, siempre con la decisión escrita y con motivo; ningún humano decide el ritmo a ojo.
- **Ficheros:** `internal/recruit/planner.go`, `internal/recruit/planner_test.go`, `testdata/recruit/planner_windows.json`
- **Cierra:** A2-11, A3-16, V1-4, V3-4 (mitad de `RECRUIT_PPS_FIXED`)
