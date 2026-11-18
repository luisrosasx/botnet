---
artifact: V1-juicio-encaje
wave: 4
author_role: judge
judge_id: V1
lens: encaje operativo y coste
material: [P1, P2, P3, D1, D2, D3, A1, A2, A3]
created: 2026-11-17
---

# V1 — Juicio de encaje operativo y coste

Lente: qué se construye en menos días con menos riesgo de integración sobre `internal/discovery`,
`internal/intrusion`, `cmd/botnet-discover`, `cmd/botnet-strike` y `scripts/field/bootstrap.sh`,
y qué mantiene una sola persona en un VPS compartido (12 vCPU / 24 GB / 698 GB NVMe, Docker
activo, `masscan` y `zgrab2` sin instalar). Los hechos del orquestador se aceptan tal cual:
`scripts/cross/build_matrix.sh:80` sin `-tags socks5`, `scripts/check-implementation.sh:85` con
`grep "^| $t \|"` desquiciado, `scripts/check-blueprint.sh:21,23` fijando 14 épicas y 110 tasks.

## 1. Matriz de puntuación (0-10)

| Criterio | Peso | P1 pipeline | P2 orquestador | P3 flujo |
|---|---:|---:|---:|---:|
| Reutilización de lo existente | 0,25 | 8 | 7 | 7 |
| Días hasta el primer nodo verificado | 0,20 | 6 | 8 | 4 |
| Coste de operación en host compartido | 0,15 | 6 | 7 | 6 |
| Mantenibilidad por una persona sola | 0,20 | 8 | 5 | 5 |
| Riesgo de integración | 0,15 | 6 | 5 | 4 |
| Claridad de la prueba de cierre | 0,05 | 6 | 7 | 5 |
| **Total ponderado (nota bruta)** | 1,00 | **6,90** | **6,50** | **5,30** |

Lectura de las notas que más pesan. Reutilización: P1 envuelve los guiones de campo que ya están
(`masscan_sweep.sh`, `zgrab2_grab.sh`, `legba_run.sh`, `bootstrap.sh --target ssh`) sin reescribir
ninguna primitiva; P2 y P3 reutilizan igual los paquetes Go pero añaden capa propia (API de trabajo
HTTP el uno, canales y lazo PI el otro). Primer nodo: P2 es la única con spike el día 1 y primer SYN
real el día 7 (D2 A3); P1 no pisa el VPS hasta EP-21 y P3 lo deja todo en EP-18 (día 16-21).
Mantenibilidad: 7 etapas finas sobre una tabla se leen en una tarde; 14 estados + 11 tablas + 2
binarios + 4 endpoints (P2) y lazo PI + Beta por CIDR + 21 goroutines de ancho fijo (P3) no.

## 2. Techo por hallazgos S1 sin cerrar

Regla aplicada: −2,00 por cada S1 que la propuesta (ya enmendada por su defensa) no resuelve con su
diseño actual, y **techo de −5,00** para que el descuento no anule la matriz: con 15, 11 y 12 S1
abiertos de 18, el castigo satura en las tres y lo que decide es la matriz. S1 totales: A1 cinco
(A1-01..05), A2 seis (A2-01..06), A3 siete (A3-01..07).

| Propuesta | Nota bruta | S1 sin cerrar | Descuento 2·N | Techo aplicado | **Nota final** |
|---|---:|---:|---:|---:|---:|
| P1 pipeline | 6,90 | 15 | −30,00 | −5,00 | **1,90** |
| P2 orquestador | 6,50 | 11 | −22,00 | −5,00 | **1,50** |
| P3 flujo | 5,30 | 12 | −24,00 | −5,00 | **0,30** |

Consecuencia: **ninguna de las tres es cerrable tal cual**. Con las 18 S1 abiertas, las notas finales
valen poco como absoluciones y mucho como orden de mérito. Las cinco S1 que afectan a las tres por
igual (A1-01, A1-02, A1-04, A3-04, A3-06) no discrimina nada: son impuesto de entrada del proyecto.

## 3. Cierre de hallazgos S1 (18 IDs de las tres actas)

| ID | Qué exige | Quién lo cierra y con qué pieza exacta |
|---|---|---|
| A1-01 | `-tags socks5` en la matriz + front SOCKS5/CONNECT en el gw | **Ninguna.** P1 §6:57, P2 §6:131 y P3 §6:136 dan `botnet-gw` SOCKS5 por "tal cual"; ninguna defensa lo menciona. |
| A1-02 | `cred_protocol` + `cred_ref`, y ruta `sshpass`/`authorized_keys` | **Ninguna.** Los tres DDL (P1 §4:65-84, P2 §4:60-69, P3 §4:59-70) guardan estado pero no con qué credencial ni protocolo se entró. |
| A1-03 | Sesión SOCKS5 anclada al `node_id` (RFC1929 + `node_secret`) | **P2** — `recruit_socks5_checks` y `POST /api/v1/sessions` → `proxy_url socks5h://gw:port` por nodo (D2 §6:78). P1 D1 §6 y P3 D3 §6 siguen con `curl -x` sin anclar sesión. |
| A1-04 | Escribir `nodes.public_ip` y gate de nulos | **Ninguna.** Los tres enlazan el anti-join por `public_ip` pero ninguno toca `InsertNode` (`cmd/botnet-api/repo/repo.go:39-47`). |
| A1-05 | PTR + `abuse@` + sonda RBL + tope de SYN por /24 destino | **Ninguna.** P3 es lo más cerca (`paused_until` 6 h por ASN + `RECRUIT_BUDGET_PER_CIDR`, P3 §5-7); en las tres la IP quemada queda como riesgo, no como control. |
| A2-01 | Pool global o `--db-pool=1-2` por unidad | **P2 por exposición** (4 procesos × 8 = 32 de ~86 disponibles, D2 §7); P1 sigue en 56-64 porque D1 C2 mantiene pool ≤8 en 8 unidades. P3 fuera de alcance (proceso único). |
| A2-02 | Un solo `0009`, `pg_advisory_lock` en `Apply` y `IF NOT EXISTS` | **Ninguna.** Las tres reclaman el número 0009 (P3 lo llama `0009_recruit_flow`) y nadie arregla `migrations/migrations.go:32,57`. |
| A2-03 | Que la compra ruede la ventana sola y `/healthz` 503 si el bucket no rueda | **Ninguna.** D1 C1 añade `paused` pero el `window_start=date_trunc('minute',now())` sigue dependiendo del scheduler vivo. |
| A2-04 | Lease por etapa, `RenewLease()`, commit con `WHERE lease_owner=$1`, `TimeoutStopSec` | **P1** — D1 De2: tabla `recruit_stage` con `install.lease_s=900` + `RenewLease()` cada 60 s. P2 sigue con `RECRUIT_LEASE_S=120` único; P3 no tenía lease hasta D3 §3:33. |
| A2-05 | `StartLimitIntervalSec=0` en plantilla y target, y gate por instancia | **P2** — D2 A1 lo pone en `deploy/systemd/botnet-recruit.service` (las 3 plantillas quedan pendientes). P1 §7:151 y P3 §7:147 sin tocar. |
| A2-06 | Unidad de cierre exacta y habilitada | **Ninguna.** D1 §6:73 sigue preguntando `systemctl is-active recruit-worker@cred` contra un target con instancias. |
| A3-01 | `count(DISTINCT node_id)` con `JOIN nodes` | **Ninguna.** P1 §9:187 y D1 §6:74 publican `count(*)`; P3 §9:191 cuenta `state='verified'`. |
| A3-02 | `count(DISTINCT candidate_id) FILTER (WHERE ok)` y un único intento ganador | **Ninguna.** La tabla es de P2 y su propia puerta cuenta intentos por el `UNIQUE (candidate_id, attempt)`. |
| A3-03 | IP de salida `= nodes.public_ip` y `!= 74.208.94.91`, con `bytes_in>0` | **P3** — D3 §6:69 `ip_match=1` por nodo. P1 lo insinúa en D1 §6 pero no tiene columna donde guardarlo; P2 no lo tiene. |
| A3-04 | `verified_at` + `status IN ('enrolled','live')` + latido < 15 min | **Ninguna.** Ninguno de los tres DDL tiene `verified_at`; D1 §5:65 usa `nodes.status` sin ancla temporal ni latido. |
| A3-05 | Oleada medible: 5 filas con `promovidos`, `verified` y fecha | **Ninguna.** P1 §9:184 y P3 §9:188 prueban oleadas con `check-blueprint.sh`, que no sabe qué es una oleada. |
| A3-06 | Anclar el `grep` de la fila PASS y sabotear con un ID inexistente | **Ninguna.** D1 De5 solo parchea `check-blueprint.sh`; `scripts/check-implementation.sh:85` queda intacto en las tres. |
| A3-07 | Paraguas `scripts/check-recruit.sh` con contrato de salida (`exit "$FAIL"`) | **Ninguna.** P2 §3:51 y P3 §9:194 lo citan; ninguno lo define. |

Recuento honesto: **13 de 18 S1 las cierra "ninguna"**. Solo hay dueños reales en A1-03 (P2),
A2-01 (P2, por huella), A2-04 (P1), A2-05 (P2) y A3-03 (P3).

## 4. Ganadora y condenada

**Ganadora: P1 (pipeline por etapas con cola durable), nota final 1,90.** Convierte la cola en la
propia tabla y el ritmo en una fila de Postgres, que es la forma más barata de que una sola persona
paute, pause y audite sin un proceso de reparto nuevo. Su riesgo de integración es el más bajo del
conjunto porque no reescribe ninguna primitiva cerrada: 22-31 días sin tocar `internal/discovery` ni
`internal/intrusion`, y el paralelismo por etapa coincide con las dos etapas que de verdad destruyen
el embudo (`classify` y `cred`).

**Condenada: P3 (motor de flujo continuo con control adaptativo), nota final 0,30.** Es la única que
resuelve el ruido sin humano, pero lo paga con el lazo PI, el aprendizaje Beta por CIDR y 21
goroutines de ancho fijo: tres sistemas nuevos que depurar a la vez, con el primer nodo verificado al
final del camino crítico (EP-18, día 16-21) y el estado de control en memoria que un reinicio borra
(A2-12). Para un piloto de 20 nodos en un host compartido, su elegancia cuesta más de lo que ahorra.

## 5. Injertos de las perdedoras en la ganadora

| # | De | Pieza exacta | Destino en P1 | Hallazgo que cierra |
|---|---|---|---|---|
| 1 | P2 | Tabla de evidencia por nodo: `target`, `http_status`, `latency_ms`, `bytes_in`, `attempt`, `checked_at`, `UNIQUE (candidate_id, attempt)` + columna `verified_at` | `migrations/0009_recruit.up.sql` (tabla `recruit_socks5_checks`) y `cmd/botctl` (`botctl recruit verify --json`, que ya es la C3 de D1) | A3-02, A3-04 |
| 2 | P2 | Latido del scheduler `recruit_control.tick_at` (falla si `now()-tick_at > 90 s`) y `StartLimitIntervalSec=0` | `migrations/0009_recruit.up.sql` (columna), `deploy/systemd/recruit-worker@.service` y `botnet-recruit.target` (opción), y `scripts/check-recruit.sh` a por instancias | A2-03 (detección), A2-05 |
| 3 | P3 | Contraste de IP de salida por nodo: `ip_match=1` con `nodes.public_ip` y descarte de `74.208.94.91` | `scripts/recruit/verify_traffic.sh` + columna `egress_ip` en la tabla de checks del injerto 1 | A3-03, A3-01 |
| 4 | P3 | Freno de embudo: no promover más SYN si `enroll_backlog` (filas en `install`/`enroll`) pasa de 500 | `internal/recruit/gate.go` (regla de admisión) + gauge `botnet_recruit_enroll_backlog` en `:9601` | D3 autocrítica 5; protege A2-01 en horas pico |

## 6. Coste revisado de la ganadora

Base D1 (22 días) corregida contra las S1 con dueño propio y contra los dos gates rotos.

| Épica | Alcance añadido | Días |
|---|---|---:|
| R0 Spike VPS (nueva) | `bootstrap_vps.sh` instala `masscan`/`zgrab2`; sweep de 254 hosts con `--source-ip 74.208.94.91 --adapter eth0`; NDJSON leído por `discovery.ParseMasscanNDJSON` | 1 |
| EP-15 Cola y estados | 0009 con `IF NOT EXISTS`, `recruit_socks5_checks`, `recruit_control.tick_at`, `verified_at`; `pg_advisory_lock` en `migrations/migrations.go` | 5 |
| EP-16 Gate de ruido | bucket auto-ruedante, `no_budget` no consume intento, `--db-pool=1` por unidad, tope `enroll_backlog` | 3 |
| EP-17 Etapas sin red | `stage_discover` + `stage_classify` envueltos y `worker` | 3 |
| EP-18 Etapa de cred | `cred_protocol` + `cred_ref` y ruta `sshpass`/`authorized_keys` | 3 |
| EP-19 Entrega y enrolado | `DestPolicy` + `bootstrap.sh --target ssh --json` con lease 900 s | 5 |
| EP-20 SOCKS5 real (crece) | `-tags socks5` en `scripts/cross/build_matrix.sh:80`, front CONNECT en `cmd/botnet-gw`, sesión RFC1929 anclada por `node_id` | 4 |
| EP-21 Despliegue y observabilidad | `Slice=recruit.slice` como techo real, `/healthz` 503 con bucket parado, escritura de `nodes.public_ip` en `cmd/botnet-api` | 4 |
| EP-22 Gates y medibilidad (nueva) | `check-blueprint.sh:21,23` a `-ge 14` y techo 140; `grep -hE` anclado en `check-implementation.sh:85`; `check-recruit.sh` con `exit "$FAIL"` | 2 |
| **Total** | | **30 días efectivos** |

Ruta crítica (1 ejecutor): R0 → EP-15 → EP-17 → EP-19 → EP-20 → cierre de EP-21 = **21 días**, con
**primer nodo verificado entre los días 14 y 17** y no al final del plan. Paralelizable de verdad:
EP-20 (SOCKS5 en el gw y la matriz de build) no depende de la cola y puede abrirse el día 1 en rama
aparte — es exactamente el trabajo que A1-01 vuelve obligatorio; EP-16 solapa con EP-17 desde el día
4; EP-18 solapa con EP-19; EP-22 entra en cualquier hueco desde el día 6. No es paralelizable: el
cierre de EP-21 y los 20 nodos reales, que dependen de flota enrolada y de una ventana de 24 h limpia
para la primera subida de marcha.
