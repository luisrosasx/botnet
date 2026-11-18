---
artifact: epic
id: EP-16
title: Orquestador central — estado, cola durable, lease con fencing y planificador de ritmo
phase: F2
risk: high
estimate_days: 6.5
closes: A2-03, A2-04, A3-16, injertos V1-2, V1-4, V2-1, V2-2, V3-4
artifacts:
  - cmd/botnet-recruit/main.go
  - internal/recruit/state.go
  - internal/recruit/planner.go
  - internal/recruit/budget.go
  - internal/recruit/store.go
  - internal/recruit/httpapi.go
---

## Objetivo

Materializar la ganadora P2: un único proceso (`botnet-recruit`) que es la autoridad del estado y del ritmo — transiciones de la máquina de estados del blueprint §5, cola durable, lease con fencing por fase, ventanas-hora UTC, presupuestos en tres unidades y `/healthz` con latido. El estado vive en Postgres, así que perder un worker o reiniciar el host no pierde un candidato; el ritmo tiene una sola autoridad con techo auditable.

## Alcance

- `cmd/botnet-recruit/main.go` con subcomandos y banderas del blueprint §4: `migrate`, `run`, `--roll-window --once`, `wave close`, `--config`, `--db-pool=2`, `--sweep-json`.
- API mínima del orquestador (`internal/recruit/httpapi.go`): `POST /v1/work/claim`, `POST /v1/work/complete`, `POST /v1/control/pause`, `POST /v1/control/resume`, `GET /healthz`, `GET /metrics` en `127.0.0.1:9110` (puerto propio: 9108 y 9109 ya prometidos — A2-17).
- Máquina de estados en `internal/recruit/state.go`: los 14 estados (`new`, `excluded`, `eligible`, `sweeping`, `bannered`, `assessed`, `staging`, `enrolling`, `enrolled`, `verified`, `deferred`, `discarded`, `burned`, `rolled_back`) con los 8 caminos de la tabla §5 y su `fail_reason` tipada; cada transición escribe `recruit_candidate_events` con `actor` y `seq`.
- Cola durable (`internal/recruit/store.go`): claim atómico `FOR UPDATE SKIP LOCKED` con `LIMIT` sobre el índice parcial por fase y `UNIQUE (candidate_id, stage, attempt)` como segunda clave de idempotencia `[injerto: V2]`.
- Lease con fencing por fase (grab 30 s, cred 300 s, deliver 900 s), `RenewLease()` cada 60 s y commit `WHERE lease_owner=$1 AND lease_token=$3` `[injerto: V2]`; reclaim de leases vencidos por `recruit_q_lease`.
- Fallback del worker: `RECRUIT_WORKER_CLAIM_FALLBACK=1` — sin orquestador, el worker reclama directo sobre `recruit_candidates` con el mismo `SKIP LOCKED` y emite a `pps_min` (1500) (el orquestador manda el ritmo, no da permiso para existir).
- Planificador (`internal/recruit/planner.go`): ventana = una hora UTC alineada que nace al cambiar de hora y se cierra con `closed_at`; semilla de `recruit_window_policy` (22-02 h = 0, 02-06 h = 3, 06-22 h = 1); **la compra rueda la ventana sola** y el orquestador también la rueda en `ExecStartPost` con `--roll-window --once` (A2-03); la ventana de credenciales se evalúa en hora **local del destino** (desplazamiento por `country`), no en el reloj del operario (A1-11).
- Lazo con histéresis escrito en `recruit_planner_ticks`: subir **una** clase tras 3 ventanas consecutivas con `socks5_success_ratio >= 0,95`, `pps_effective >= 0,90 × pps_budget`, `node_leave_ratio_24h <= 0,05` y `probes_15m >= 200` (si no, `hold` con `reason='denominador_corto'`); bajar **una** sola clase al incumplir una métrica y abrir cooldown de 3 ventanas; nunca dos subidas en menos de 6 ventanas; fallback declarado del host compartido (`load1 <= 6` con `host_fallback=true`); freno de embudo con `enroll_backlog > 500` ⇒ `hold` con `reason='enroll_backlog'`; salida de emergencia con `recruit_control.manual_rate_class` `[injerto: V3]` `RECRUIT_PPS_FIXED=<n>`.

## Fuera de alcance

- Los workers (`botnet-probe`, `botnet-strike`, `botctl recruit verify`) son EP-17 y EP-18; el orquestador no ejecuta etapa propia, solo reparte y recibe.
- El DDL es de EP-15; aquí no se añade ninguna columna nueva.
- Las unidades systemd, la slice y los secretos: EP-19. Las métricas y el paraguas: EP-20.
- El lazo no usa PromQL: umbral medido con stdlib y `curl`+`grep` sobre `/metrics` (A3-15 — el repo no tiene Prometheus).

## Dependencias

- **EP-15** (obligatoria): todo el estado está en las tablas de `0009_recruit.up.sql`.
- Desbloquea EP-17 (los workers consumen `claim`/`complete` y el lease) y EP-20 (el lazo y las métricas salen de `recruit_planner_ticks` y `recruit_control.tick_at`).
- Paralelizable con EP-18 (el frente SOCKS5 del gw y la matriz no tocan la cola).

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| Bucket congelado: ventana sin rodar y compra que se come intentos (cadena A de A2) | alta | alto | compra auto-ruedante + `no_budget` no consume intento + `/healthz` 503 a los 90 s (T-1601, T-1606) |
| Fila caliente de `recruit_windows` serializada: 4-6 workers quemando el `statement_timeout` (A2-13) | media | medio | compra por granel con deuda `<= 5000` pps y fail-open con techo (T-1606) |
| El orquestador como punto único de fallo | media | alto | estado en Postgres + lease con reclaim + fallback de claim + latido con 503 (blueprint §4) |
| El lazo nunca decide (siempre `hold`) y sale verde igual (A3-16) | media | medio | `recruit_planner_ticks` con `up_total >= 1` y `down_total >= 1` exigidos por el paraguas (T-1607) |
| Dos workers escribiendo el mismo candidato tras un lease vencido | media | alto | fencing `WHERE lease_owner=$1 AND lease_token=$3` (T-1604) |

## Criterio de salida de la épica

Con un fixture de 200 candidatos: `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE lease_expires_at < now()"` = **0** a los 240 s de un `systemctl restart botnet-recruit`; `curl -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz` = **200** con latido fresco y **503** con `tick_at` congelado 2 min; `psql -tAc "SELECT count(*) FROM recruit_planner_ticks WHERE action IN ('up','down')"` ≥ **2**; y dos `POST /v1/work/claim` concurrentes no devuelven el mismo `candidate_id`.

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-1601 | Proceso y API del orquestador con `/healthz` por latido | 8 h |
| T-1602 | Máquina de estados de 14 estados con auditoría por transición | 8 h |
| T-1603 | Cola durable con claim atómico `FOR UPDATE SKIP LOCKED` | 6 h |
| T-1604 | Lease por fase con `RenewLease()` y commit con fencing | 6 h |
| T-1605 | `/v1/work/{claim,complete}` y `/v1/control/{pause,resume}` con fallback del worker | 6 h |
| T-1606 | Ventanas-hora UTC auto-ruedantes con política por hora y granel con deuda | 8 h |
| T-1607 | Lazo de histéresis con cooldown, `host_fallback`, freno de embudo y `RECRUIT_PPS_FIXED` | 8 h |
