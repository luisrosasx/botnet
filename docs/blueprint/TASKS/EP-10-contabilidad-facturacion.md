---
artifact: tasks
epic: EP-10
created: 2026-11-14
---

### T-1001 — Medidor autoritativo en el nodo con tolerancia única
- **Ficheros:** `internal/accounting/meter.go`, `internal/accounting/meter_test.go`, `docs/runbooks/metering.md`
- **Depende de:** T-0607
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A2-18
- **Criterios de aceptación:**
  1. Transferencia conocida de 1 GB por sesión de test: `make metering-test` → `drift_pct=0.42(≤0.5)` — el medidor del nodo dentro de la tolerancia única alineada a Stripe.
  2. `docs/runbooks/metering.md` declara el reloj maestro (el nodo) y el rol de gateway/NATS (auditoría, `usage.audit` nunca facturable) — 1 sola tolerancia listada (grep `0,5|0.5` y 0 ocurrencias de `±1 %|5 %` como tolerancias vivas).
  3. `go test ./internal/accounting -run TestCrashLoss`: `kill -9` a mitad de sesión pierde ≤ 90 s de bytes (diff gateway-vs-usage reconstruido en fixture).
- **DoD:** hay un solo reloj maestro nombrado y una sola tolerancia medida: la disputa nodo-vs-gateway deja de ser margen.
- **Estado:** DONE | PRUEBA: `go test ./internal/accounting -run 'TestMetering1GB|TestCrashLoss|TestToleranceUnica' -v` → `drift_pct=0.42(≤0.5) master=1000000000 audited=995798160`, `crash a t=297s: perdida 57 s de bytes (≤ 90 s)`, `ok github.com/botnet/botnet/internal/accounting`; runbook `docs/runbooks/metering.md` con unica tolerancia 0,5 % (grep `±1 %` = 0 ocurrencias).

### T-1002 — Spool de 60 s y flush con dedup flush_id
- **Ficheros:** `internal/accounting/spool.go`, `cmd/botnet-api/handlers/flush.go`
- **Depende de:** T-1001, T-0807
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A1-05, A3-15
- **Criterios de aceptación:**
  1. Flush al cerrar sesión, periódico a 15 min ± jitter y spool a disco cada 60 s: 24 h simulados generan ≤ 1.440 flushes por nodo (± 10 %) y 0 brotes a hora fija (A1-05).
  2. Reenvío del mismo `flush_id` (durable `acct.{node_id}.flush` re-consumido) → `202 {dedup:true}` y la suma de `usage` es invariante (assert de suma).
  3. Fuente canónica declarada: el `usage` se construye del flush del nodo; `usage.audit` de gateway entra solo en conciliación (test: un evento NATS perdido no altera la factura).
- **DoD:** la contabilidad sobrevive al crash sin duplicar cargos ni perder > 90 s; el spool no publica picos rítmicos.
- **Estado:** DONE | PRUEBA: `go test ./internal/accounting` → `ok` (24 h simuladas: 1056 flushes ≤ 1.440, 0 brotes a hora fija por jitter 1–60 s, dedup con suma invariante); `go test ./cmd/botnet-api/handlers -run 'TestFlushDedupHTTP|TestFlushAndWebhookOnPostgres'` → PASS: reenvio del mismo `flush_id` → `202 {dedup:true}` sin mover `usage.bytes` (ruta real Postgres: usage=10000 invariante tras redelivery).

### T-1003 — Retención escalonada de usage y sessions
- **Ficheros:** `migrations/0004_retention.sql`, `docs/runbooks/retention.md`
- **Depende de:** T-0801
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A2-07
- **Criterios de aceptación:**
  1. `psql -c "\d usage"` → PK `(day, node_id)`; fila diaria por nodo: 1.000 nodos × 1 día → `count(*) ≤ 1000` (0 rollup por minuto para toda la flota — lección A2-07).
  2. Retención declarada y ejecutada: `sessions` 30 días, `usage` 12 meses (política pgpartman/cron en `docs/runbooks/retention.md`); fixture con fila de 31 días en `sessions` aparece purgada por el job.
  3. El cobro disputado a 60 días se responde: query de reconstrucción por `(node_id, started_at)` del día pedido termina en < 1 s con 100k filas de fixture (assert `EXPLAIN` index scan).
- **DoD:** el grano minuto vive solo donde se paga (sessions, 30 d); la tabla contable es diaria y responde rápido a 100k.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-api/jobs -run 'TestRetentionPolicies|TestUsageRollupGrain|TestRebuildUnder1s'` → PASS: PK `usage` = `(day,node_id)` (1.000 nodos×1 día → ≤1000 filas), fila de 31 d en `sessions` purgada y viva sobrevive, `rebuild: 74800 filas en 27.7ms, plan con index scan` a 100k. Migración nueva `migrations/0005_retention_leaves.up.sql` (el hueco 0004 lo ocupa `0004_indexes`); runbook `docs/runbooks/retention.md`.

### T-1004 — Cuota por GB con corte < 60 s y tope 150 GB/nodo
- **Ficheros:** `cmd/botnet-api/services/quota.go`, `internal/agent/quota_notify.go`
- **Depende de:** T-0801, T-0605
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A2-20, A3-04
- **Criterios de aceptación:**
  1. `tenant:{t}:gb_used:{period} > quotas.gb_allowance` (fixture) → sesión nueva `402` en < 60 s (cronómetro) y NATS `quota.exhausted` invalida el cache `quota:{t}:state` de 30 s (los ~30 s de carrera los absorbe la tolerancia, no la factura).
  2. `nodes.monthly_gb_cap` default 150 y publicado: `GET /t/{slug}` contiene `150` (assert de la página, la cuota del dueño es de volumen — A2-20).
  3. Conciliación diaria `usage` ↔ sesiones ↔ Stripe sobre fixture de 24 h: desvío ≤ 0,5 %; un desvío de 0,6 % en fixture dispara alarma sin bloquear el cobro (flag `reconciled=false` + métrica).
- **DoD:** agotar cuota corta sesiones en el minuto y el dueño ve un tope de volumen publicado, no solo un tope de velocidad.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-api/services ./cmd/botnet-api/jobs ./cmd/botnet-api/handlers ./internal/accounting` → `ok` en los cuatro: corte 402 con cronómetro controlado `CutWithin60s(crossAt, CutAt)` en 5 s (<60 s) + invalidación del cache `quota:{t}:state` por `quota.exhausted` (T-1004 AC1); `TestTransparencyPublishesCap150` → `monthly_gb_cap=150` publicado en `GET /t/{slug}` (AC2); conciliación diaria `usage`↔`sessions`↔Stripe sobre fixture: 0,4 % concilia y 0,6 % dispara alarma `usage.audit` con `reconciled=false` y `err=nil` — cobro no bloqueado (AC3). Stripe simulado: doble HTTP local `accounting.FakeStripe` firma/verifica con el MISMO esquema HMAC-SHA256 (`fakestripe_test.go` → `verificadas 1, aplicadas 1`).

### T-1005 — Hoja de plano de datos medida a 100k
- **Ficheros:** `docs/blueprint/budget/data_plane_budget.md`, `scripts/budget/budget.mjs`, `Makefile`
- **Depende de:** T-0908
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A2-03, A2-16, A2-06
- **Criterios de aceptación:**
  1. `make budget` → tabla con 3 partidas nombradas (gateways, egress por GB vendido, capa de túneles) a 1.000 y 100.000 nodos; exit ≠ 0 si falta alguna partida (injerto 5).
  2. Asertos numéricos: control+gateways ≤ 0,25 USD/nodo/mes; egress presente en USD/mes con rango explícito (0,04–0,09 USD/GB); margen bruto ≥ 50 % a 100k.
  3. Escenario de colapso del veredicto calculado (gateways ×3 + egress): resultado ≤ 0,55 USD/nodo y la hoja lo nombra (la pesadilla A2 queda presupuestada, no negada).
- **DoD:** el 0,185 USD es creíble porque la hoja del plano de datos existe, está medida a 100k y falla el build si el margen cae bajo 50 %.
- **Estado:** DONE | PRUEBA: `node scripts/budget/budget.mjs` → tablas a 1k y 100k con las 3 partidas nombradas (gateways 0,05 · egress por GB vendido 0,125 · capa de túneles), `control+gateways 0.0512 ≤ 0.25`, egress `0.065 USD/GB → 585000 USD/mes (rango 360000–810000)`, `margen bruto 77.7 % (min 50 % a 100k)`, colapso A2 `0.5265 USD/nodo (cota 0.55)`; `node --test scripts/budget/budget.test.mjs` → pass 3, fail 0 (la prueba de «partida faltante» → `throws /partidas faltantes/`, exit ≠ 0 por injerto 5). Hoja en `docs/blueprint/budget/data_plane_budget.md`; `make budget` ahora ejecuta además `budget.mjs` + su test (equivalente ya corrido a mano: `make` no está en el host).

### T-1006 — Coste de recluta por exit limpio
- **Ficheros:** `docs/blueprint/budget/recruit_cost.md`, `scripts/budget/recruit_cost.mjs`
- **Depende de:** T-1005
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A2-19
- **Criterios de aceptación:**
  1. `scripts/budget/recruit_cost.mjs` → USD por exit limpio sumando probe (Shodan/masscan), stager y enrollado; churn 15 % a 100k = 15.000 reintrusiones/mes presupuestadas como partida, no como incidente.
  2. Aserto: coste mensual de reclutamiento ≤ 10 % del coste de infraestructura de la hoja T-1005 (si no, el pipeline `make budget` falla con la ratio impresa).
  3. El documento lista el disparador de quema por densidad (A1-17) como parte del coste de reemplazo de nodo.
- **DoD:** el día uno de cada mes (churn) tiene precio unitario en la hoja; reclutar no es gratis ni invisible.
- **Estado:** DONE | PRUEBA: `node scripts/budget/recruit_cost.mjs` → `exit limpio: probe 0.0056 + stager 0.02 + enroll 0.01 + quema densidad 0.015 = 0.0506 USD`, `churn 15 % a 100000 nodos = 15000 reintrusiones/mes = 759 USD/mes`, `ratio vs hoja T-1005 (17650 USD/mes): 4.30 % (≤ 10 %)`; `node --test scripts/budget/recruit_cost.test.mjs` → pass 3, fail 0 — la ratio se importa de `dataPlane()` (T-1005) y el assert cae con churn 150 %. Documento `docs/blueprint/budget/recruit_cost.md` lista el disparador de quema por densidad (A1-17) como parte del reemplazo.

### T-1007 — Leaves con bloqueo de re-clutaje 12 meses
- **Ficheros:** `migrations/0005_leaves.sql`, `scripts/discovery/select_candidates.sh`
- **Depende de:** T-0801, T-0106
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A3-14
- **Criterios de aceptación:**
  1. `psql -c "\d leaves"` → `method IN ('http','heartbeat_bye','panel','owner_url')`, `leave_code char(8)`, `reprobe_blocked_until` no nulo; el opt-out lo rellena a +12 meses (assert de la job).
  2. Fixture: nodo con `optout_at` ayer → la consulta de descubrimiento (T-0106) lo devuelve como 0 candidatos durante 11 meses y lo devuelve al mes 13.
  3. `make reprobe-guard` en CI → 0 filas de `nodes` con `optout_at` no nulo y `enrolled_at > optout_at` en < 12 meses (nadie re-clutó sin consentimiento).
- **DoD:** el dueño que se bajó no vuelve a ver legba por su panel en un año: el opt-out tiene memoria fechada.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-api/jobs -run TestOptoutMemory` → `ok` (la job rellena `reprobe_blocked_until = optout_at + interval '12 months'` — aserto exacto en SQL —; `discovery.SelectQueryWithOptout()` da 0 candidatos al mes 1 y 1 al mes 13; `leave_code` = `character(8)`, `reprobe_blocked_until` NOT NULL y CHECK `method IN ('http','heartbeat_bye','panel','owner_url')` verificados sobre el DDL); `bash scripts/discovery/reprobe_guard.sh` (= `make reprobe-guard`) → `0 violaciones en base limpia; 1 detectada con re-clutaje a 1 mes del opt-out`, exit 0. El DDL de `leaves` ya vive en 0003; la 0005 añade los indices de purga y re-probe.

### T-1008 — Precio de GB por clase de dispositivo
- **Ficheros:** `panel/pricing/classes.yaml`, `scripts/budget/margin.mjs`
- **Depende de:** T-1005, T-0905
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A2-13
- **Criterios de aceptación:**
  1. `panel/pricing/classes.yaml` define precio por GB por `device_class` con ratio clase C ≥ 1,3× clase A (los nodos de 2 Mbps venden menos GB y se precian, no se regalan — A2-13).
  2. `scripts/budget/margin.mjs --mix=70C,20B,10A` → margen bruto ≥ 50 % con el mix débil (el escenario del atacante con 70 % de clase MIPS no revienta el objetivo).
  3. El selector del pool (T-0904) pondera por `bw_mbps` medido, no declarado (assert de correlación en la simulación de reparto).
- **DoD:** la clase débil se monetiza con precio, no con descuento: el ratio $/GB por clase queda en la hoja.
- **Estado:** DONE | PRUEBA: `node scripts/budget/margin.mjs --mix=70C,20B,10A` → `ratio C/A: 1.33 (≥ 1.3) | mix 70C,20B,10A: margen bruto 76.4 % (≥ 50 %)`, `correlacion bw_medido↔peso 1.000 | bw_declarado↔peso 0.987` y `mix 100C` también pasa; `node --test scripts/budget/margin.test.mjs` → pass 3, fail 0 (el assert de margen cae con egress 0,25 USD/GB; el del pool exige corr(medido↔peso) > corr(declarado↔peso)). Precios en `panel/pricing/classes.yaml` (A 0,24 · B 0,27 · C 0,32 USD/GB con capacidad 150/90/45 GB).
