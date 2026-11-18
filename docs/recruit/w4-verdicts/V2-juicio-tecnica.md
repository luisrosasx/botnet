---
artifact: V2-juicio-tecnica
wave: 4
author_role: judge
judge_id: V2
lens: corrección técnica y cierre de riesgo (concurrencia, idempotencia, presupuesto de red, arranque en frío, fugas)
scores: [P1-pipeline, P2-orquestador, P3-flujo]
based_on: [P1,P2,P3,D1,D2,D3,A1,A2,A3]
created: 2026-11-17
---

# V2 — Veredicto del Juez 2: corrección técnica y cierre de riesgo

Se puntúa el diseño **como queda tras su defensa** (P+D), no el borrador. Regla de la sección 2:
`nota final = mín(nota bruta, 10 − 2·n)`, suelo 0, donde `n` son los S1 de A1/A2/A3 que tocan los seis
criterios y que P+D dejan **sin mecanismo alguno**.

## 1. Matriz de puntuación (0-10 por propuesta, ponderada)

| Criterio | Peso | P1 | P2 | P3 | Base de la nota |
|---|---:|---:|---:|---:|---|
| C1 Idempotencia y recuperación | 0,25 | 8 | 7 | 6 | P1 tiene la mejor cola del concurso: `FOR UPDATE SKIP LOCKED` + `UNIQUE (ip,port)` + `UNIQUE (candidate_id,stage,round,attempt)` convierten el reintento en `ON CONFLICT DO NOTHING`; le falta fencing (el commit no mira `lease_owner`). P2 añade `idempotency_key` y eventos con `UNIQUE (candidate_id,seq)`, pero el reparto pasa por un proceso. P3 hace CAS de una sentencia sin lock (barato y a prueba de lost update), sin auditoría por intento hasta D3. |
| C2 Control de ruido y su medición | 0,20 | 7 | 7 | 8 | P1: un único punto de verdad (`recruit_rate_budget`), pero habla en pps para logins y ssh (De3 lo admite) y su subida pide 24 h limpias (A2-11: caben 2 en todo el cierre). P2: `rate_class` con techo auditable y `CHECK`, pero dos de las cinco señales miden al vecino. P3: `pps_target` frente a `pps_effective`, índice de ruido y ticks persistidos 7 días. |
| C3 Contención con el host y Postgres compartido | 0,20 | 4 | 6 | 8 | Conexiones concurrentes sobre ~86 disponibles (`internal/db/db.go:24` fija 8 por proceso): P1 11 procesos = **88**, P2 4 = **32**, P3 1 = **8**. P1 y P2 multiplican el pool por unidad sin PgBouncer. P3 es la única con techo de cgroup real (`Slice` + `MemoryHigh` < `MemoryMax` + `TasksMax`) y el único presupuesto de host que cuadra (3 vCPU = 25 %, 1,5 GB = 6,25 %). |
| C4 Arranque en frío sin estado | 0,10 | 8 | 7 | 7 | P1: la cola es la tabla, no hay estado que reconstruir. P2: estado en Postgres, pero la ventana y su `closed_at` solo ruedan si vive el orquestador. P3: recarga filas no terminales, y pierde `integral`, `cooldown` y los hasta 3.584 elementos de canal; arranca en 2000 pps con la cola de Postgres más cargada (A2-12). |
| C5 Fugas de recursos (procesos, conexiones, leases) | 0,15 | 4 | 5 | 6 | Ninguna partía con `StartLimitIntervalSec=0`. P1 sin `KillMode` hasta D1 (masscan huérfano a 1500 pps) y con 10 instancias por 7 declaradas. P2 con lease de 120 s, menor que un barrido de 216 s: reclaim prematuro y segundo subprocess garantizado. P3 con `KillMode=control-group`, `TasksMax=128` y canales acotados, pero sin lease hasta el robo de D3. |
| C6 Verificabilidad del criterio SOCKS5 | 0,10 | 4 | 7 | 6 | Nadie puede cumplirlo hoy: `scripts/cross/build_matrix.sh:80` compila sin `-tags socks5` y solo las pruebas usan el tag, así que `dist/botd-*` llevan el stub (`internal/proxy/socks5_stub.go:11` → `ErrDisabled`). P1 cuenta candidatos y guarda un booleano. P2 es el único con evidencia por intento (`recruit_socks5_checks`, `target`, `http_status`, `latency_ms`, `bytes_in`), aunque cuenta intentos y no nodos. P3 contrasta la IP de salida contra `nodes.public_ip`. |
| **Nota bruta ponderada** | 1,00 | **6,00** | **6,50** | **6,90** | |

## 2. Techo por S1 sin cerrar (−2 por S1)

| Propuesta | Nota bruta | S1 del lente que P+D dejan sin mecanismo | n | Techo (10−2n) | **Nota final** |
|---|---:|---|---:|---:|---:|
| P1 | 6,00 | A1-01 SOCKS5 ausente del artefacto; A2-01 88 conexiones (D1 C2 mantiene 8 por unidad, no toca la multiplicación); A2-03 bucket congelado con el scheduler muerto; A2-06 el cierre pregunta por `recruit-worker@cred`, unidad que P1 no habilita | 4 | 2,0 | **2,0** |
| P2 | 6,50 | A1-01; A2-01 (32 conexiones viven, pero la multiplicación por proceso sigue sin resolver); A2-04 lease de 120 s sin fencing por debajo de los 216 s del barrido | 3 | 4,0 | **4,0** |
| P3 | 6,90 | A1-01 (el único S1 de lente sin cerrar; A2-12 es S2 y A1-03/A3-0x son de medición) | 1 | 8,0 | **6,9** |

No entran en el recuento, por compartidos y ajenos a estos seis criterios: A1-02 (`install` por SSH con clave
sobre población de credencial por defecto), A1-04 (`nodes.public_ip` sin escribir → opt-out de 12 meses muerto),
A1-05 (reputación de la IP de origen) y A3-04 (falta `verified_at`). Los cuatro siguen abiertos para la ganadora.

P1 queda en 2,0 por **acumulación de S1, no por calidad**: su mecanismo de cola es el mejor del concurso y se
marcha a la ganadora por la vía de los injertos.

## 3. Modos de fallo en cadena de A2: inevitables y quién los corta

| Cadena | Es inevitable en | Por qué es estructural | Quién la corta antes |
|---|---|---|---|
| A — bucket congelado (A2-03) | **P1** | el scheduler es el único que rueda `window_start`; muerto él, la compra no devuelve fila, el `no_budget` consume `attempt` y el reclaim lo reencola hasta `timeout_budget` con proceso en verde | **P3**, por colación: productor y lazo viven en el mismo proceso, así que "cero SYN" implica siempre "cero `/metrics`"; el estado "vivo pero mudo" no existe. P2 la recorta (D2 A1: `--roll-window --once`, `tick_at` con latido y claim en fallback a 1500 pps) pero no la elimina |
| B — reclaim sin fencing + huérfanos de masscan (A2-04, A2-06) | **P1 y P2** | lease más corto que la tarea (300 s frente a `bootstrap.sh`; 120 s frente a 216 s de barrido) y commit que no comprueba `lease_owner`: entran dos `bootstrap.sh` y dos masscan desde la misma IP de origen, y el router recibe dos `botd` | **P3**: `KillMode=control-group` + `TasksMax=128` se llevan al hijo con el padre desde el primer día, y el CAS impide que el sucesor escriba encima de un estado que ya cambió. Le falta el `WHERE lease_owner=$1` (injerto 2) |
| C — restart en ventana + start-limit (A2-05, A2-09) | **las tres** | default systemd (burst 5 en 10 s) deja la unit en `failed` tras un `apt upgrade` a las 07:05, `is-enabled` sigue diciendo `enabled` y el gate sale VERDE con cero trabajo | **P3**, porque su unidad es única y su propio comando de cierre ya pide `pgrep -c masscan` tras el restart; P1 es el único que además pregunta por una instancia que no habilita. **Ninguna la cierra**: falta `StartLimitIntervalSec=0` |

## 4. Presupuesto de red: recálculo de los números de A2

Con SYN de 44 B (nivel IP) y trama Ethernet de 58 B; 1 Mbps = 1.000.000 bit/s.

| Afirmación | Recálculo | Veredicto |
|---|---:|---|
| 1500 pps ≈ 0,53 Mbps (P1 §5) | 1500 × 44 B = 66.000 B/s × 8 = 528.000 bit/s = **0,528 Mbps** | **P1 acierta.** A2 dice que está 8× por debajo ("0,528 MB/s = 4,22 Mbps") al confundir 66.000 B/s con 0,528 MB/s |
| Con trama de 58 B | 1500 × 58 × 8 = **0,696 Mbps** | A2 publica 5,49 Mbps (otra vez MB/s por Mbps) |
| "5 Mbps = 625.000 bit/s" (A2 §4) | 5 Mbps = 5.000.000 bit/s; **625.000 son bytes/s** | error de unidad de A2 |
| "→ 1.347 pps" (A2 §4) | 5.000.000 / (58 B × 8 = 464 bit) = **10.776 pps**; con preámbulo+IFG (72 B) = **8.680 pps** | A2 divide bytes/s entre bits/trama ⇒ **8× de error**. Con 44 B: 5.000.000/352 = **14.204 pps**, que es exactamente el 14.200 de `internal/discovery/masscan.go:47-49` |
| Consecuencia de la reductio | con 1.347 pps el suelo del código (`SweepRate(12)` = 1500) ya pasaría del techo del runbook y **ninguna** propuesta podría emitir un solo paquete | el "1,1× por encima del runbook" de A2 no existe |
| Techo P1 10.000 pps | 3,52 Mbps (44 B) / 4,64 Mbps (58 B) | sigue por debajo de 5 Mbps; la crítica válida de A2 es que **ninguna** cita el ancho medido del enlace del VPS |
| Techo P2/P3 5.000 pps | 1,76 Mbps (44 B) / 2,32 Mbps (58 B) | margen de 2,2× sobre el límite con trama Ethernet; no necesitan el 14.200 para justificarse |
| Presupuesto de host P1 "≤3,2 vCPU y ≤3 GB" con "7 instancias" | lo que enumera son **10** workers: discover 50 % + 9 × 25 % = **2,75 vCPU (23 %)** y 1.024 + 9 × 384 MB = **4,48 GB (18,7 %)** | CPU conservadora y conteo de unidades contradictorio; **la memoria está 49 % corta**. A2 tampoco acierta: anuncia 4,0 vCPU con la misma suma que da 275 % |
| `conn_budget = 20 × pps / 1500` y `attempt_budget = 40 × clase` (P2) | clase 4 = 66,7 conexiones (no entero) y `conn_used` no lo incrementa nadie; 200 candidatos × 3 intentos = 600 intentos a 40/h = **15 h de ventana por oleada** | adorno el primero, inviable el segundo (A2-10) |

**Quién sigue publicando números incorrectos:** **P1** (4,48 GB reales publicados como ≤3 GB; "7 instancias"
frente a 10 workers; escalera a 10.000 pps sin ancho de enlace medido) y **P2** (`conn_budget` sin uso y no
entero; `attempt_budget` que no cabe en el calendario). **Quién los corrige: P3**, la única cuyas cifras
sobreviven al recálculo — un solo proceso (3 vCPU / 1,5 GB), 1500..5000 pps = 0,53..1,76 Mbps,
`RECRUIT_BUDGET_PER_CIDR=4000` probes/h como techo por rango y el único que mide `pps_effective` frente al
objetivo. Lo que P3 **no** corrige es lo que A2 tampoco mide: el ancho real del enlace del VPS sigue sin número.

## 5. Ganadora y condenada

**Ganadora: P3 (motor de flujo continuo).** Menos agujeros reales donde manda la lente: 8 conexiones frente a
88, un solo techo de cgroup, `KillMode=control-group` que no deja masscan huérfano, CAS sin lock como
primitiva de concurrencia, y el único diseño que mide el ritmo que de verdad sale por la tarjeta. Sobrevive
al reinicio porque recarga filas no terminales y al worker muerto porque su canal acotado no acumula backlog
infinito; sus dos deudas (lease y evidencia SOCKS5) ya están nombradas y son injertables.

**Condenada: P1 (pipeline por etapas).** Agravante concreto: **falla en silencio justo donde el cierre mira.**
Muerto el scheduler, el bucket deja de rodar, la compra no devuelve fila, cada ciclo consume `attempt` y los
candidatos mueren de `timeout_budget` sin haber tocado la red, mientras `/healthz` sigue dando 200 porque el
que responde es el worker (A2-03). Y el comando con el que pretende probar la condición 3 pregunta por
`recruit-worker@cred`, una instancia que su propio despliegue no habilita (solo habilita el target) — o sea,
falso verde operativo y falso rojo de cierre sobre el mismo error. Y remata con el ahogo de Postgres: 11
procesos × 8 conexiones = 88 sobre ~86 disponibles **antes** de que nuclei, archivebox y los contenedores
Docker pidan las suyas ⇒ `FATAL 53300` y `botnet-api` deja de enrolar.

## 6. Injertos a la ganadora (desde las perdedoras)

| # | Pieza | De | Destino exacto | Por qué entra |
|---|---|---|---|---|
| 1 | Cola durable con claim atómico: `FOR UPDATE SKIP LOCKED` + `LIMIT` sobre **índice parcial por fase** y `UNIQUE (ip,port,fase,intento)` | P1 §4 | `migrations/0009_recruit_flow.up.sql` (partir `recruit_flow_ready` en índices por fase) y `internal/recruit/db.go` | El CAS protege la transición pero no el reparto: hoy el productor relee `recruit_flow_ready` en cada tick, que es su única consulta caliente. Con claim atómico, un worker muerto no devuelve la misma fila a dos operadores |
| 2 | Lease con fencing: `lease_owner`/`lease_expires_at`, `RenewLease()` cada 60 s, commit `WHERE lease_owner=$1` y duración **por fase** (`grab` 30 s, `strike` 300 s, `deliver` 900 s) | P1 §4 y D1 De2 | `recruit_flow` + `internal/recruit/db.go`; `TimeoutStopSec` en `deploy/systemd/botnet-recruit.service` | D3 robó el lease pero no el fencing ni la duración por fase. Sin `WHERE lease_owner` el worker cuyo lease venció sigue pudiendo escribir, y `deliver` (SSH + copia) no cabe en un lease único |
| 3 | Reloj de edad que no avanza con `skip`: `age_clock_at` que solo progresa con `outcome <> 'skip'` | D1 De4 (P1) | columna en `recruit_flow` + regla del productor en `internal/recruit/targets.go` | Con `RECRUIT_WINDOWS=01:00-08:00,13:00-14:00` y `attempts <= 3`, un candidato sano puede agotar su vida en tiempo de reloj sin trabajar. Sin esta columna, `dead` por presupuesto queda indistinguible de `dead` por aparato malo |
| 4 | Presupuesto declarado en **tres unidades** (`pps` \| `conn` \| `attempt`) con tope de intentos por IP de destino y hora | D1 De3 (P1) + A2-10/A1-13 (P2) | columnas `pps_target`, `conn_target`, `attempt_target` en `recruit_control_tick`; reglas en `internal/recruit/controller.go` | El lazo de P3 solo gobernar SYN: `strike` (legba) emite logins y `deliver` (ssh) emite conexiones sin ningún techo. Y sin tope por destino, un router con bloqueo por intentos recibe 6 logins en un día y queda `burned` dos oleadas |

## 7. Riesgos residuales que la ganadora deja vivos

| # | Riesgo | Condición de aceptación |
|---|---|---|
| 1 | **El criterio SOCKS5 no es ejecutable hoy**: `build_matrix.sh:80` no pasa `-tags socks5`, `dist/botd-*` llevan el stub y `botnet-gw` no levanta front SOCKS5; nada de eso está en las épicas de P3 (EP-18 solo instala masscan/zgrab2) | Se acepta si hay una épica previa al primer alta con: `-tags "socks5 tcp"` en la matriz, listener en el gw, y el gate `strings dist/botd-amd64 \| grep -c 'bot-session malformado'` = 1 más `nc 127.0.0.1 1080` saludando. Sin eso, `state='verified'` relleno a mano **falsea el cierre** |
| 2 | **Arranque en frío del lazo**: `integral=0`, `pps_target=2000` y canales vacíos mientras la cola de Postgres está en su máximo (A2-12); las filas no terminales quedan sin lease hasta el injerto 2 | Se acepta si `recruit_control_tick` persiste y recarga `integral`, `pps_target` y `cooldown_until`, y arranca en `pps_min` (1500) con rampa. Gate: tras `systemctl restart botnet-recruit`, `botnet_recruit_pps_target <= 1500` en el primer tick y `pgrep -c masscan` ≤ 1 |
| 3 | **Monoproceso + start-limit systemd**: un crash-loop deja el escaneo parado y el gate en verde (Cadena C sin cerrar) | Se acepta con `StartLimitIntervalSec=0` en la unit y con el paraguas exigiendo `botnet_recruit_pps_effective > 0` leído en `/metrics` (no solo `systemctl is-active`) durante los 3 min siguientes al despliegue |
| 4 | **Decisiones con cohorte incompleta y denominador corto**: un barrido de un /16 tarda 218 s (327.680 SYN a 1500 pps; el ejemplo del propio `masscan_sweep.sh` es `--range=1.0.0.0/16`) frente a un tick de 60 s, y `yield_ratio` con 3 probes vale lo que quiera | Se acepta si el tick solo evalúa la cohorte cerrada (SYN emitidos hace ≥ 2× RTT p95) y la subida queda bloqueada con motivo `denominador_corto` cuando `probes_15m < 200` (A3 §4), con el motivo escrito en `recruit_control_tick.action/reason` |
