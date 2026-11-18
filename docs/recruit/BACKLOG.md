---
artifact: backlog-recruit
wave: 6
author_role: refiner
based_on: BLUEPRINT.md
created: 2026-11-17
---

# BACKLOG de reclutamiento — EP-15 … EP-21 (39 tasks)

Troceo del blueprint ganador (**P2 orquestador**, con los 12 injertos y los 18 S1 de A1/A2/A3 absorbidos) en 7 épicas ejecutables. Ninguna task añade arquitectura: el esquema es el §6 del blueprint, el ritmo es el §7, el proxy es el §8, el despliegue es el §9, las métricas son el §10 y el paraguas es el §13. Ninguna task cierra un hallazgo sin un comando que lo mida.

## 1. Mapeo con el blueprint §14 (que enumeraba 8 épicas)

El blueprint renombraba R0–R7 → EP-15..EP-22. Este plan consolida en **7 épicas** (EP-15..EP-21) con el reparto pedido; el contenido no cambia, solo la frontera.

| Épica de este plan | Absorbe del blueprint §14 | Coherencia con el blueprint |
|---|---|---|
| EP-15 Esquema y migración | su EP-15 (0009, advisory lock) | §6 y gate G4 |
| EP-16 Orquestador y planificador | su EP-17 (estado, eventos, cola) + su EP-18 (planificador, presupuesto) | §4, §5, §7 |
| EP-17 Workers de etapa | su EP-19 (workers) + su EP-20 (credencial y entrega) | §4, A1-02 |
| EP-18 Verificación SOCKS5 por nodo | su EP-16 (SOCKS5 real) | §8 (decisión de proxy), gate G3 |
| EP-19 Despliegue VPS y systemd | su EP-15 (spike VPS) + su EP-21 (despliegue) | §9 |
| EP-20 Observabilidad, presupuesto de red y gate | su EP-22 (paraguas, parte de gates) | §10, §13 |
| EP-21 Puerta de cierre | su EP-22 (las 5 oleadas y los 20 nodos) | §5, §12, §14 |

## 2. Épicas, tasks y estimación

| Épica | Capa | Fase | Tasks | Estimación |
|---|---|---|---:|---:|
| EP-15 Esquema `0009_recruit` y migración sin carrera | db | F1 | 5 | 20 h (2,5 d) |
| EP-16 Orquestador y planificador | service | F2 | 7 | 50 h (6,5 d) |
| EP-17 Workers de etapa (`discovery`, `intrusion`) | workers | F3 | 6 | 46 h (6 d) |
| EP-18 Verificación SOCKS5 real por nodo | proxy | F3 | 5 | 40 h (5 d) |
| EP-19 Despliegue VPS y unidades systemd | infra | F4 | 5 | 36 h (4,5 d) |
| EP-20 Observabilidad, presupuesto de red y gate | obs | F4 | 6 | 50 h (6,5 d) |
| EP-21 Puerta de cierre | cierre | F5 | 5 | 48 h (6 d) |
| **Total** | | | **39** | **290 h ≈ 36,5 días-persona** |

Rangos de ID: T-1501…T-1505, T-1601…T-1607, T-1701…T-1706, T-1801…T-1805, T-1901…T-1905, T-2001…T-2006, T-2101…T-2105. Sin colisión con las existentes (T-0101…T-1407). Con las 102 tasks del plan anterior el total es **141**, dentro del techo de 200 del gate (A2-15).

## 3. Fases F1–F5 y su puerta

| Fase | Épicas | Puerta de salida (umbral ejecutable) |
|---|---|---|
| **F1 — Esquema** | EP-15 | Dos `migrate` concurrentes: `grep -c 'already exists'` = **0** y `schema_migrations LIKE '0009%'` = **1**; `pg_tables LIKE 'recruit_%'` = **11**; `go test ./migrations/ -v \| grep -c -- '--- SKIP'` = **0** con DSN y **FAIL** sin DSN (G4). |
| **F2 — Núcleo del orquestador** | EP-16 | `recruit_candidates WHERE lease_expires_at < now()` = **0** a los 240 s de un restart; `/healthz` **200** con latido fresco y **503** con `tick_at` de 2 min; dos `POST /v1/work/claim` concurrentes no devuelven el mismo `candidate_id` (**0** solape); `recruit_planner_ticks` con `up` ≥ 1 y `down` ≥ 1. |
| **F3 — Etapas y nodo real** | EP-17, EP-18 | Fixture de 200 candidatos: ≥ **180** en `bannered|assessed` y `cred_protocol IS NULL` = **0** entre los con credencial; `strings dist/botd-amd64 \| grep -c 'bot-session malformado'` = **1**; `netstat` con `127.0.0.1:1080.*LISTEN` = **1**; `count(DISTINCT node_id)` de los verify = nº de nodos probados (hoy **1** con 20 filas). |
| **F4 — Servicio desplegado** | EP-19, EP-20 | `is-enabled botnet-recruit` = **enabled** y `is-active 'botnet-recruit-worker@*'` = **3 × active**; `pg_stat_activity` ≤ **20** y `53300` = **0**; `nodes.public_ip IS NULL AND status IN ('enrolled','live')` = **0**; `check-recruit.sh` con `comprobaciones=N`, **N ≥ 5**, y **2** sin `psql`. |
| **F5 — Cierre** | EP-21 | `recruit_waves` con **5** filas `cerrada` + `promoted>0` + `verified>=1`; **≥ 20** `count(DISTINCT node_id)` con latido < 15 min, `ip_match` en 20/20 y `device_class='A'`; `check-recruit.sh` = **0** con los **6** sabotajes en **≥ 1** y reversión a **0**; `go test ./...` con 0 `FAIL`. |

## 4. Camino crítico

```text
EP-15 ──→ EP-16 ──┬─→ EP-17 ──→ EP-21
                  └─→ EP-19 ──→ EP-20 ──→ EP-21
EP-18 (rama aparte desde el día 1) ──────┘ (alimenta EP-20 y EP-21)
```

- **Camino crítico (esfuerzo encadenado):** **EP-15 → EP-16 → EP-17 → EP-20 → EP-21 = 27,5 días**. El blueprint §14 declaraba 23 días con su propio reparto de 8 épicas; la diferencia son las tasks que aquí explícitamente prueban que el gate muerde (T-2004, T-2005, T-2105) y el arreglo de G4 (T-1505).
- **Totales:** 290 h de esfuerzo ≈ 36,5 días-persona; calendario de épicas **33 días** con las dos pistas en paralelo.
- **Primer nodo verificado:** entre los días **13 y 17** (necesita EP-15, EP-16, EP-17, EP-18 y EP-19; el blueprint estimaba 12-15).
- **No es paralelizable:** EP-21. Pide flota enrolada y 3 ventanas limpias para la primera subida de marcha (A2-11), y sus 20 nodos tienen que estar vivos **a la vez**.

## 5. Calendario y lo paralelizable

| Ventana de calendario | En curso | Por qué cabe |
|---|---|---|
| Días 1-2 | EP-15 (esquema) + EP-18 arrancada en rama | El tag `socks5`, el frente del gw y la deny-list no tocan la cola ni el esquema nuevo (así lo pide A1-01: «trabajo nuevo, no tal cual»). |
| Días 3-9 | EP-16 | Estado, cola, lease, ventanas y lazo: todo lo demás reclama aquí. |
| Días 9-12 | EP-19 **en paralelo** con EP-17 | EP-19 solo necesita el binario (EP-16) y el gw con tag (EP-18, ya cerrado el día 5). |
| Días 10-15 | EP-17 y spike de barrido | Las etapas corren en modo fixture (`--sweep-json`, `RECRUIT_PPS_FIXED`) mientras el VPS recibe los SYN reales. |
| Días 16-22 | EP-20 | Métricas, RBL diario (7 días de observación que se solapan con las oleadas) y las 8 comprobaciones del paraguas. |
| Días 23-33 | EP-21 | Las 5 oleadas y los 20 nodos, sin paralelo: es la puerta. |
| Continuo, desde el día 1 | Sonda RBL y `abuse@` (T-2006) | 7 días limpios son condición de subida de marcha, no una tarea que se pueda acelerar. |

## 6. Trazabilidad obligatoria: los 18 S1 y los 12 injertos

Sin filas sin task. La columna «Task» es la que cierra; «Apoyo» es la que lo hace medible.

### 6.1 Los 18 S1 (A1-01 … A3-07)

| Hallazgo | Contenido del S1 | Task | Apoyo |
|---|---|---|---|
| A1-01 | `-tags socks5` en la matriz y front SOCKS5/CONNECT del gw como trabajo nuevo | **T-1801** | T-1802 |
| A1-02 | Entrega según el protocolo con que se ganó (`ssh` → `authorized_keys`, fallback `sshpass`; sin SSH ⇒ `no_delivery`) | **T-1705** | T-1502, T-1704 |
| A1-03 | Una sesión SOCKS5 anclada al `node_id` (RFC1929 + `node_secret`, anti-replay 30 s) | **T-1803** | T-1504 |
| A1-04 | `nodes.public_ip` escrita desde el `201` y el primer heartbeat; purge solo a los 13 meses | **T-1904** | T-1502 |
| A1-05 | Reputación del origen: PTR propio + `abuse@`, sonda RBL diaria, tope de SYN por /24 | **T-2006** | T-1504, T-1606 |
| A2-01 | Pool **por proceso** recortado (`--db-pool=2`) y preflight del despliegue | **T-1902** | T-1901, T-1905 |
| A2-02 | Un solo `0009_recruit.up.sql` con `pg_advisory_lock(6009)`, `IF NOT EXISTS` y sin `ALTER` en tablas calientes | **T-1501** | T-1502, T-1503, T-1504 |
| A2-03 | La compra rueda la ventana sola, `/healthz` 503 con latido caducado y `no_budget` no consume intento | **T-1606** | T-1601, T-1905 |
| A2-04 | Lease **por fase** (30/300/900 s), `RenewLease()` y commit con fencing | **T-1604** | T-1903 |
| A2-05 | `StartLimitIntervalSec=0` en unit y plantilla; el cierre mide instancias | **T-1902** | T-1903 |
| A2-06 | Unidad de cierre exacta: unit **y** plantilla habilitadas, cierre por `'botnet-recruit-worker@*'` | **T-1903** | T-2005 |
| A3-01 | El conteo del cierre es `count(DISTINCT node_id)` con `JOIN nodes` y `node_id IS NOT NULL` | **T-2004** | T-2102 |
| A3-02 | Un único intento ganador por candidato (`recruit_socks5_one_win`) y conteo con `FILTER (WHERE ok)` | **T-1504** | T-2004, T-2102 |
| A3-03 | `egress_ip` + `ip_match` obligatorios, IP de salida ≠ 74.208.94.91, `bytes_in>0`, destino fijo | **T-1805** | T-1504, T-2004, T-2102 |
| A3-04 | `verified_at` + `status IN ('enrolled','live')` + `optout_at IS NULL` + latido < 15 min al medir | **T-1502** | T-2004, T-2102 |
| A3-05 | Oleada medible en `recruit_waves` (`cerrada`, `promoted>0`, `verified>=1`, `closed_at`); la bitácora es espejo | **T-2101** | T-1504, T-2004 |
| A3-06 | Patrón de la fila PASS anclado al inicio de línea y a las dos primeras celdas | **T-2001** | T-2103 |
| A3-07 | Paraguas propio con contrato de salida (`FAIL`, `exit "$FAIL"`, `comprobaciones=N`, preflight 2) | **T-2003** | T-2004, T-2005, T-2105 |

### 6.2 Los 12 injertos obligatorios

| Injerto | Contenido | Task | Apoyo |
|---|---|---|---|
| V1-1 | Evidencia por intento con `bytes_in>0` y `latency_ms BETWEEN 0 AND 1500` como condición dura de `ok`, más `verified_at` y el worker `botctl recruit verify --json` | **T-1804** | T-1504, T-1805 |
| V1-2 | Latido `recruit_control.tick_at` con fallo a 90 s y `StartLimitIntervalSec=0` | **T-1905** | T-1601, T-1902 |
| V1-3 | Contraste de la IP de salida (`egress_ip`, `ip_match=1`, descarte de `74.208.94.91`) | **T-1805** | T-2004 |
| V1-4 | Freno de embudo `enroll_backlog > 500`, que deja de promover SYN | **T-1607** | T-2002 |
| V2-1 | Cola durable con claim atómico `FOR UPDATE SKIP LOCKED` + `LIMIT` sobre índice parcial y `UNIQUE (candidate_id, stage, attempt)` | **T-1603** | T-1502 |
| V2-2 | Lease con fencing: `RenewLease()` 60 s, commit `WHERE lease_owner=$1`, duración por fase y `TimeoutStopSec` | **T-1604** | T-1502, T-1903 |
| V2-3 | Reloj de edad que no avanza con `skip`: `age_clock_at` | **T-1602** | T-1502, T-1606 |
| V2-4 | Presupuesto en tres unidades con tope de intentos por IP de destino y hora | **T-1606** | T-1503, T-1704 |
| V3-1 | El propio gate se parchea en la misma PR (`-ge 14`, techo de tasks, fuera `btree_gist`) | **T-2001** | T-1501 |
| V3-2 | Presupuesto con unidad tipada `unit text CHECK (unit IN ('pps','conn','attempt'))` | **T-1503** | T-1606 |
| V3-3 | `KillMode=control-group` + `TasksMax` + `ExecStartPre=test -x /usr/bin/masscan` | **T-1902** | T-1903 |
| V3-4 | `RECRUIT_PPS_FIXED=<n>` (lazo congelado) y modo fixture NDJSON en CI | **T-1706** | T-1607 |

## 7. Lo que este plan no abre

Ninguna task de EP-15..EP-21 introduce una tabla, un servicio o un puerto que el blueprint no nombre ya. Los nombres que usan las tasks son los del blueprint: tablas `recruit_candidates`, `recruit_candidate_events`, `recruit_jobs`, `recruit_windows`, `recruit_window_policy`, `recruit_control`, `recruit_planner_ticks`, `recruit_socks5_checks`, `recruit_exclusions`, `recruit_waves`, `recruit_source_ip`; puerto `127.0.0.1:9110` (9108 y 9109 ya prometidos); VPS `74.208.94.91`; slice `recruit.slice`; units `botnet-recruit.service` y `botnet-recruit-worker@.service`.
