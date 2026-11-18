---
artifact: defense
wave: 2
author_role: defender
defends: P2-recruit
created: 2026-11-17
---

# D2 — Defensa de P2 (orquestador central, estado por candidato, ventanas de presupuesto)

## 1. Tesis en 5 líneas

- El host manda: 12 vCPU / 24 GB compartidos exigen **una sola autoridad del ritmo**. P2 la tiene en tres filas (`recruit_windows`, `recruit_window_policy`, `recruit_control`); P1 la reparte entre 7 workers y un bucket por minuto; P3 la reparte entre controlador PI, productor ponderado y ventanas.
- `≥20 nodos verificados con una petición SOCKS5 real por nodo` es una prueba **por nodo y con evidencia**: `recruit_socks5_checks (candidate_id, attempt, http_status, latency_ms)` da la fila que se firma en el cierre; un booleano `verify_ok` no la da.
- El cierre pide **criterios medidos**, no elegancia: cada transición deja fila en `recruit_candidate_events`, así que `scripts/check-recruit.sh` imprime funnel y PASS sin levantar el servicio ni tocar la red.
- El conservadurismo está anclado al código real, no a un deseo: la clase 1 son los 1500 pps que ya devuelve `SweepRate(12)` (`internal/discovery/masscan.go:52`) y el techo 5000 deja el enlace por debajo de los ~14200 pps del runbook.
- Workers **stateless con lease**: en un VPS que comparte CPU y reinicia por culpa de los vecinos, perder un worker no pierde un candidato; el estado vive en Postgres y `Restart=always` + `WatchdogSec=30` basta para recuperarlo.

## 2. Autocrítica: 5 debilidades reales y su parche

| # | Debilidad real de P2 | Parche concreto (tabla / fichero / regla) |
|---|---|---|
| A1 | **Punto único de fallo del orquestador.** Sin él no hay `claim`, no hay relevo de ventana (`closed_at`), no hay escalada de `rate_class`: el throughput cae a cero y el presupuesto de la ventana quedan congelados. `Restart=always` reanima el proceso pero no garantiza el relevo si el reinicio entra en `start-limit-hit`. | `deploy/systemd/botnet-recruit.service` añade `StartLimitIntervalSec=0` y `ExecStartPost=/usr/local/bin/botnet-recruit --roll-window --once` (idempotente). Regla de latido: columna nueva `recruit_control.tick_at timestamptz`; `scripts/check-recruit.sh` falla si `now() - tick_at > 90s`. Modo degradado `RECRUIT_WORKER_CLAIM_FALLBACK=1`: el worker reclama directo sobre `recruit_candidates` con el mismo `FOR UPDATE SKIP LOCKED` y a `pps_min` (1500) mientras el orquestador no responda — el orquestador manda el ritmo, no es un permiso para existir. |
| A2 | **Presupuesto cobrado en Postgres puede bloquear la operación.** La compra de pps es un `UPDATE` sobre la fila de la ventana (serialización en una sola fila) y el `CHECK (pps_used <= pps_budget)` convierte el sobrepaso en error 23514; con `statement_timeout=5s` un worker puede quedar parado 5 s y quemar un intento en una BD lenta. | `migrations/0009_recruit.up.sql`: compra por **granel** por lease — columnas nuevas `pps_granted int NOT NULL DEFAULT 0` y `pps_debt int NOT NULL DEFAULT 0`; el `CHECK` duro se sustituye por `CHECK (pps_used - pps_budget <= 5000)` (una ventana de deuda, no un muro). Regla fail-open con techo en `internal/recruit/budget.go`: dos fallos o un `>200 ms` en la compra => el worker sigue a `pps_min` y suma `botnet_recruit_budget_unavailable_total`; Postgres lento **ralentiza, nunca detiene**. |
| A3 | **Orden estricto R1→R2→R3→R4 deja el primer SYN real hacia el día 17** y la cierre pide 20 nodos verificados: riesgo de enterarse en la cuarta semana de que `masscan` no tenía `CAP_NET_RAW` en el VPS. | Épica nueva **R0 (1 día)**: `scripts/recruit/install_deps.sh` + un sweep de 254 hosts en 74.208.94.91 con salida NDJSON leída por `discovery.ParseMasscanNDJSON`. Reordenación: R2 se corta en dos (claim/complete los 2 primeros días) y `R4a botnet-probe` entra el día 7. Primer SYN real el día 7 en vez del 17. |
| A4 | **Cadena larga y embudo desconocido:** 14 estados y 6 saltos hasta `verified`; si el yield real de masscan en bruto es 1:20, hacer 20 nodos cuesta 400 candidatos por oleada y P2 no tenía dónde medirlo. El doble banner con `banner_hash` coincidente añade hasta dos ventanas (2 h) por candidato. | Vista `recruit_funnel_v` sobre `recruit_candidate_events` + criterio en `scripts/check-recruit.sh --funnel` (tabla `state|rows|pasa|motivo_muerte`). Regla nueva: segunda captura **solo** si `device_class_guess IS NULL OR model_guess IS NULL` (lo demás baja el pps y las horas). Regla de subida: solo se sube `rate_class` si el yield del embudo mejora; si el embudo está roto, más SYN es más ruido gratis. |
| A5 | **La escalada puede quedarse congelada en clase 1 para siempre.** Pide 5 métricas a la vez y dos de ellas (`botnet_recruit_host_cpu_pct`, `botnet_recruit_tcp_retrans_ratio`) miden el host **compartido**, que no controlamos: un pico del vecino bloquea la subida cinco oleadas y el cierre se alarga. | `scripts/recruit/host_stats.sh` (una llamada por tick, lee `/proc/net/snmp` y `/proc/loadavg`, stdlib sin node_exporter). Fallback documentado: si `host_cpu_pct` no es fiable, se acepta `load1 <= 6` (criterio de P1 §5). Salida de emergencia en la tabla: columna nueva `recruit_control.manual_rate_class smallint` con `updated_at` — el operario puede subir una marcha dentro de una oleada sin romper la auditoría (`actor` en `recruit_candidate_events`). |

## 3. Concesiones que robo a P1 y P3

| # | Robo | De | Dónde entra en P2 | Por qué no rompe la coherencia |
|---|---|---|---|---|
| C1 | `bytes_in > 0` + latencia como condición de verify (P1 §10 riesgo 11) | P1 | `recruit_socks5_checks`: `CHECK (ok = false OR (bytes_in > 0 AND latency_ms BETWEEN 0 AND 1500))`, columna nueva `bytes_in int NOT NULL DEFAULT 0` | Refuerza el estado `verified` sin añadir estados: sigue siendo la única salida de éxito. |
| C2 | `KillMode=control-group` + `TasksMax=128` (P3 §7) | P3 | `deploy/systemd/botnet-recruit.service` y `botnet-recruit-worker@.service` | Un masscan huérfano a 5000 pps es el peor fallo del modelo de ruido; es un ajuste de unit, no de arquitectura. |
| C3 | Ticks de control persistidos con retención 7 días (P3 `recruit_control_tick`) | P3 | Tabla nueva `recruit_planner_ticks (tick_at timestamptz PRIMARY KEY, rate_class smallint, action text CHECK (action IN ('up','down','hold','paused')), reason text)`; poda como `migrations/0005_retention_leaves.up.sql` | Da la evidencia escrita de que "subir solo con métricas" ocurrió (condición 2 pide fila PASS), y el que decide sigue siendo el planificador. |
| C4 | Exclusión por CIDR con índice GiST y doble comprobación (P1 §4 `btree_gist`, §5 "el banner puede delatar un AS de banco tarde") | P1 | `recruit_exclusions` acepta `kind='prefix'` con `prefix cidr` + `CREATE INDEX recruit_excl_gix ON recruit_exclusions USING gist (prefix inet_ops)`; se comprueba al nacer y antes de `assessed` | La exclusión sigue siendo insumo del estado `eligible`; la autoridad del estado no se mueve. |
| C5 | Tope de edad global `RECRUIT_MAX_AGE_DAYS=14` (P1 §12 `timeout_budget`) | P1 | Regla en `internal/recruit/planner.go`: candidato > 14 días sin `verified` pasa a `discarded` con `fail_reason='age'` | Es una muerte más dentro del mismo árbol de muertes; sin estados nuevos. |
| C6 | `Nice=10` / `CPUWeight=50` por unidad (P1 §7) | P1 | `botnet-recruit-worker@{probe,cred,verify}.service` | El scheduler del host recorta a botnet antes que a los vecinos; refuerza el mismo presupuesto de CPUQuota. |
| C7 | `pps_effective` (paquetes reales frente a objetivo) como señal (P3 §5) | P3 | Métrica nueva `botnet_recruit_pps_effective` y una condición más de subida: `>= 0.90 * pps de la clase` | Impide que una red saturada parezca sana y dispare la escalada; es un insumo del planificador. |
| C8 | Taxonomia cerrada de causas de muerte (P3 `reason_code`) | P3 | `CHECK (fail_reason IS NULL OR fail_reason IN ('excluded','no_answer','no_banner','burned','flash_cap','age','socks_fail','leave'))` | `check-recruit.sh` imprime muertes por motivo sin cadenas libres. |
| C9 | Doble modo sin binario: fixture NDJSON para CI (P1 §6 `SWEEP_JSON`) | P1 | Flag `--sweep-json testdata/recruit/masscan.ndjson` en `botnet-probe`; carpeta nueva `testdata/recruit/` | Los gates (`go test ./...`) siguen en 0 sin `masscan`/`zgrab2` instalados en el runner. |

Rechazo explícito: no robo `recruit_score` (pesos Beta por CIDR/puerto de P3). Introduce memoria de aprendizaje donde P2 ya resuelve el mismo problema con `recruit_exclusions` + `rate_class`; lo reconsidero en la oleada 5 solo si el funnel da < 1 %.

## 4. Presupuesto revisado

| Épica | Días W1 | Días revisados | Paralelizable | Motivo del cambio |
|---|---:|---:|---|---|
| R0 Spike VPS | 0 | 1 | No (necesita el VPS) | A3: `masscan` y `zgrab2` contra la red real antes de escribir una línea de planificador |
| R1 Estado y DDL | 4 | 4 | Parcial con R0 | C3 y C4: `recruit_planner_ticks` y GiST entran aquí |
| R2 Orquestador y API | 5 | 4 | Sí con R4a | A2 y A3: `claim`/`complete` salen en 2 días; el presupuesto por granel va en R3 |
| R3 Planificador y presupuestos | 4 | 5 | Sí con R2 | A2: `pps_granted` + deuda + fail-open; A5: `host_stats.sh` |
| R4 Workers envueltos | 4 | 4 | Sí desde R1 | invariable; R4a (`botnet-probe`) se adelanta al día 7 |
| R5 Exclusiones y ritmo | 3 | 3 | Sí | C4 y A5 |
| R6 VPS y observabilidad | 3 | 3 | Sí | C2, C6, C7 |
| R7 Gate y cierre con 20 nodos | 4 | 5 | No (nodos reales) | A4: `--funnel` y las cinco oleadas ejecutadas |
| **Total** | **27** | **28** | | +1 día efectivo (3,7 %) a cambio de 5 días de calendario |

Calendario: **24 días → 19 días**. El día extra efectivo paga el spike R0 y el presupuesto por granel; el ahorro viene de solapar R0/R4a/R6 y de que el primer SYN real pase del día 17 al día 7, de modo que las cinco oleadas caben dentro del plan en vez de al final.

## 5. Riesgos residuales y condición de aceptación

| # | Riesgo residual | Condición de aceptación |
|---|---|---|
| R-1 | `masscan` no consigue raw socket útil en el VPS y hay que pasar a `--connect` (handshake TCP) | Se acepta si el ritmo efectivo con `--connect` es >= 800 conex/s sostenidas y `botnet_recruit_tcp_retrans_ratio <= 0,02`; si falla, la oleada queda en pausa con `recruit_control.paused = true` y no se declara fracaso |
| R-2 | Postgres lento y presupuesto en fail-open: se gastan SYN de más durante la caída | Se acepta con deuda <= 5000 pps de una ventana y <= 15 min de sobrepaso, medido en `botnet_recruit_budget_unavailable_total`; fuera de ese margen se pausa el servicio |
| R-3 | Yield del embudo por debajo de lo previsto: puede que los 20 nodos lleguen al final de la oleada 5 y no antes | Se acepta `>= 20 verified` al cierre de la oleada 5; con < 10 `enrolled` al final de la oleada 3 se ensancha la ventana nocturna y no se sube `rate_class` |
| R-4 | Ruido ajeno: un pico de los servicios vecinos sube `host_cpu_pct` por encima de 55 y el planificador se desescala solo | Se acepta una desescalada reversible en 3 ventanas consecutivas; no cuenta como incumplimiento de oleada si `botnet_recruit_node_leave_ratio_24h <= 0,05` |
| R-5 | IP de origen única (74.208.94.91) quemada por lockout en cámaras y routers tras `legba` | Se acepta el sacrificio del rango quemado: 3 intentos por ventana y por IP, estado `burned`, y alta manual en `recruit_exclusions` con `kind='asn'` |

## 6. Cómo prueba cada una de las 5 condiciones de cierre

| Condición | Comando concreto | Salida esperada |
|---|---|---|
| 1. Cinco oleadas cerradas y blueprint consolidado | `bash scripts/check-recruit.sh --oleadas && bash scripts/check-blueprint.sh` | `oleadas=5 cerradas=5 deferred_vivos=0` y `PASS blueprint`; ambos `exit 0` |
| 2. Tasks DONE con >= 3 criterios, comando y fila PASS | `bash scripts/check-state-doc.sh` (lee `docs/state/RECRUIT.md`) | una fila `PASS` por task de R0–R7 con su comando de medición impreso; `exit 0` |
| 3. Servicio persistente en el VPS con healthcheck | `ssh -i ~/.ssh/rr_vps root@74.208.94.91 'systemctl is-enabled botnet-recruit && systemctl is-active botnet-recruit && curl -fsS localhost:9109/healthz'` | `enabled`, `active` y cuerpo con `status=ok`, `db=up`, `masscan=true`; `exit 0` (503 si falta Postgres o masscan) |
| 4. >= 20 nodos reales verificados con SOCKS5 real por nodo | `bash scripts/field/socks5_probe_all.sh --min 20` (por nodo: `POST /api/v1/sessions` -> `proxy_url socks5h://gw:port` -> `curl -x ... -o /dev/null -w '%{http_code} %{time_total}'`) | `verified=21 ok=21/21` y `SELECT count(*) FROM recruit_socks5_checks WHERE ok` >= 20; `http_code=200` y `bytes_in>0` por nodo |
| 5. Gates existentes en 0 | `make vet && make test && bash scripts/check-implementation.sh && bash scripts/check-field-closure.sh && bash scripts/metrics_smoke.sh` | todo `exit 0`; `go test ./migrations/` en verde con el DDL de `0009_recruit.up.sql` y las métricas `botnet_recruit_*` presentes en `/metrics` |

## 7. Veredicto de una línea

Bajo estas restricciones a P1 se le rompe el `install` por SSH saliente (`RECRUIT_SSH_USER` en su §7) sobre una población de cámaras y routers sin SSH accesible más un `verify_ok` booleano sin evidencia por nodo, y a P3 se le rompe la operación del host compartido: tres cabezas de ritmo (controlador PI, productor ponderado y ventanas) sin una fila de pausa ni un techo auditable, con los 20 nodos dependientes de un lazo que no se estabiliza hasta la última época (EP-18, día 16 de 22).
