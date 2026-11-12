---
artifact: tasks
epic: EP-08
created: 2026-11-14
---

### T-0801 — Migraciones DDL del núcleo contable
- **Ficheros:** `migrations/0002_core.sql`, `migrations/0003_indexes.sql`, `docs/runbooks/schema.md`
- **Depende de:** —
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A3-06
- **Criterios de aceptación:**
  1. `make migrate` en Postgres limpio → exit 0; existen `tenants, api_keys, nodes, node_secrets, sessions, usage, quotas, leaves` con sus CHECK y UNIQUE del blueprint (`psql -c "\d nodes"` refleja `slug char(12) UNIQUE`, `device_token_hash char(64) UNIQUE`).
  2. `node_secrets`: índice único `(node_id) WHERE revoked_at IS NULL` → insertar un segundo secreto vivo para el mismo nodo falla (assert); `octet_length(secret)=32` respetado.
  3. Sin ORM: `grep -rn "gorm\|sqlx" cmd/botnet-api` = 0 hits; migrations solo adelante (0 archivos `*_down.sql`).
- **DoD:** el esquema del blueprint vive versionado en `migrations/` y responde a las preguntas contables con índices nombrados.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable go test ./migrations/... -count=1` → `ok github.com/botnet/botnet/migrations` (0003/0004 aplicadas en base scratch; 8 tablas, char(12)/char(64) UNIQUE, CHECK 32 B, 2º secreto vivo rechazado, 0 `*_down.sql`); `grep -rn "gorm\|sqlx" cmd/botnet-api` = 0. Nota: el 0002 del plan lo ocupa EP-01 → core = `0003_core.up.sql`, índices = `0004_indexes.up.sql`; `make migrate` = `bash scripts/backend/migrate.sh`.

### T-0802 — CA offline que firma el CSR del enroll
- **Ficheros:** `cmd/botnet-api/services/ca.go`, `deploy/signing/ca_policy.md`, `cmd/botnet-api/handlers/enroll_test.go`
- **Depende de:** T-0801
- **Estimación:** 12 h
- **Riesgo:** high
- **Cierra:** A3-02, A3-03
- **Criterios de aceptación:**
  1. `POST /v1/enroll` con CSR válido → `201` y `openssl verify -CAfile ca.pem cert.pem` → `OK`; TTL del certificado = 90 días (`openssl x509 -noout -enddate` → +90 d ± 1 d).
  2. CSR con firma forjada → `400`; CSR con CN ajeno a la política de flota → `422`; la clave privada no aparece en logs del backend (grep `BEGIN PRIVATE KEY` en logs de test = 0).
  3. `node_secret` emitido una sola vez en el `201` con fila en `node_secrets` (`issued_at` ahora, `expires_at` = +90 d, un secreto vivo por nodo); la emisión se documenta en `docs/runbooks/credentials.md` con la matriz emisor/almacén/rotación/radio (exigencia A3).
- **DoD:** la cadena de credenciales tiene matriz escrita: el par nace en el dispositivo, la CA lo firma offline, el secreto rota con el certificado.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=... go test ./cmd/botnet-api/handlers/ -count=1` → `ok` (201 decodificado por `proto.DecodeEnrollAPIResponse`; `openssl verify -CAfile ca.pem cert.pem` → OK; `openssl x509 -enddate` = +90 d (Feb 9 2027 desde alta nov-2026); gates 409/428/403/409/400/422 asserts; fila `node_secrets` issued=now/expires=+90 d/1 vivo; grep `BEGIN PRIVATE KEY` en log = 0). Matriz en `docs/runbooks/credentials.md`; politica en `deploy/signing/ca_policy.md`.

### T-0803 — CRL en Redis con revocación < 60 s
- **Ficheros:** `cmd/botnet-gw/crl.go`, `cmd/botnet-api/services/revoke.go`
- **Depende de:** T-0801
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A3-03
- **Criterios de aceptación:**
  1. `POST /v1/nodes/$ID/revoke` → `redis-cli SET revoked:{serial} 1 EX ...` y el gateway rechaza streams nuevos del nodo en < 60 s (cronómetro del test end-to-end; refresh de CRL 55 s).
  2. Stream vivo al revocar: drenado con GOAWAY en < 30 s y `sessions.closed_reason` tipada; el nodo pasa a `burned` en `nodes.status`.
  3. La baja revoca a la vez cert y `node_secret` (dos asserts: `revoked:{serial}` existe y `node_secrets.revoked_at` no nulo).
- **DoD:** revocar a un nodo quemado o traidor cierra la puerta en el minuto, sin reiniciar el gateway.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=... go test ./cmd/botnet-api/services/ -count=1` → `ok` (revoked:{serial} en Redis; `node_secrets.revoked_at` no nulo; sesión abierta → `closed_reason='goaway'`+`ended_at`; `nodes.status='burned'`; **cronómetro real: revocación vista por el CRL en 200 ms**, test con refresh 5 s y assert `DefaultCRLRefresh(55s)+PollOnce < CRLRejectWindow(60s)`; `DrainBudget ≤ 30 s`). Gateway: `cmd/botnet-gw/crl.go` (wrapper de `internal/backend.CRLCache`, refresh 55 s). Nota: el GOAWAY del stream vivo se mide en runtime F4 (backend cierra filas al instante; presupuesto de drenaje 30 s fijado en código y testeado).

### T-0804 — apikey:{sha256} en Redis con warmup
- **Ficheros:** `cmd/botnet-gw/apikey_cache.go`, `cmd/botnet-gw/apikey_cache_test.go`
- **Depende de:** T-0801
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A3-07
- **Criterios de aceptación:**
  1. Gateway frío: primera validación de una api key → exactamente 1 query a `api_keys` (pg_stat_statements delta = 1) y luego `apikey:{sha256}` en Redis con TTL 300 s; 1.000 validaciones siguientes → 0 queries Postgres.
  2. Warmup al arrancar el gateway: `curl /healthz` solo pasa cuando el cache de keys activas está cargado (assert con 10 keys de fixture).
  3. Key revocada: `status='revoked'` en Postgres y expiración del cache → validación falla en < 300 s (test de ttl).
- **DoD:** «validar contra Redis» es literal: la clave vive en Redis, se calienta al arrancar y Postgres solo es fuente de verdad fría.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=... go test ./cmd/botnet-gw/ -run 'ApiKey|Healthz|RevokedKey'` → 3 PASS (`gateway frio: 1 query exacta vía `db.Counted`, `apikey:{sha256}` en Redis con TTL 300 s, 1.000 validaciones → 0 queries; `/healthz` 503 pre-warmup y 200 post-warmup con 10 keys fixture; key revocada → `ErrKeyRevoked` al expirar el cache, <300 s). Limitación anotada: `pg_stat_statements` no está precargado en el cluster portable del host (`shared_preload_libraries` vacío) — la medida del "delta = 1" es el contador del wrapper `internal/db.Counted`, que cuenta la misma ida real.

### T-0805 — Contrato POST /api/v1/sessions con api key
- **Ficheros:** `cmd/botnet-api/handlers/sessions.go`, `docs/api/openapi.yaml`, `scripts/api/contract_test.sh`
- **Depende de:** T-0804
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A3-X1
- **Criterios de aceptación:**
  1. `curl -s -X POST -H "Authorization: Bearer $KEY" -d '{"country":"ES","city":"Madrid","mode":"rotating"}' /api/v1/sessions` → `201` con `{session_id, proxy_url, expires_at}` y `proxy_url` que matchea `^socks5h://`.
  2. Sin api key → `403`; cuota agotada (fixture) → `402`; 100 requests del mismo tenant en 1 s → ≥ 1 `429` (rate-limit por tenant).
  3. `make contract-test` verde en CI contra `docs/api/openapi.yaml` (el contrato es el spec, no un acuerdo de palabra — injerto 9).
- **DoD:** dos implementadores construyen la misma puerta de entrada: credencial, selección y respuestas tipadas por código de estado.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=... go test ./cmd/botnet-api/handlers/ -run Session` → 3 PASS (201 con `{session_id, proxy_url ^socks5h://, expires_at RFC3339, nonce}`; 403 sin key; 402 cuota agotada fixture; ≥1 429 en 100 requests mismo tenant; 503 Redis caído); `bash scripts/api/contract_test.sh` (el "make contract-test") → `total=7 pass, 0 fail` con `serve` real en :18085 y curl del AC1 literal. Spec actualizado en `docs/api/openapi.yaml` v0.2.0 (pattern `^socks5h://`, gates 428/403/422, nonce, 503, revoke, healthz) y parsea (yaml v3).

### T-0806 — Modo degradado con Redis caído
- **Ficheros:** `cmd/botnet-gw/degraded.go`, `scripts/chaos/redis_90s.sh`
- **Depende de:** T-0804, T-0805
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A3-X3, A3-09
- **Criterios de aceptación:**
  1. `scripts/chaos/redis_90s.sh` (Redis parado 90 s con sesiones vivas): 0 sesiones vivas cortadas por la caída y `503` en `POST /api/v1/sessions` durante la ventana (no admisiones a ciegas).
  2. El gateway sobre-cobra dentro de tolerancia: `tenant:{t}:gb_used` reconstruido tras recuperación con error ≤ 0,5 % (diff del fixture de contadores).
  3. Presencia re-anudada por heartbeat: `node:{id}:live` vuelve en < 180 s tras recuperar Redis (assert de TTL), sin re-enrolls.
- **DoD:** el failover de Redis está declarado y probado en caos: 90 s de caos equivalen a un susto, no a una morgue de sesiones.
- **Estado:** DONE | PRUEBA: `bash scripts/chaos/redis_90s.sh` → `--- PASS: TestRedis90sChaos (92.05s)` (ventana REAL de 90 s con redis-server matado y vuelto en puerto propio): 3 sesiones vivas siguen abiertas tras la caída (0 cortadas), `AdmissionOK=false` durante la ventana → 503 en `POST /api/v1/sessions` (assert del handler en `TestCreateSession503WhenRedisDown`), `tenant:{t}:gb_used:{period}` reconstruido desde `sessions` con error ≤ 0,5 % (fixture con sobre-cobro 2 % corregido), presencia `node:{id}:live` re-anudada con TTL ≤ 180 s sin re-enrolls. Lógica en `internal/backend/degraded.go`; wrapper `cmd/botnet-gw/degraded.go`. Nota: un redis externo ya escuchando en el puerto del caos puede solapar runs — el script es dueño del motor y usa 16391.

### T-0807 — Bus NATS con JetStream y durables
- **Ficheros:** `cmd/botnet-api/nats/bootstrap.go`, `docs/runbooks/events.md`
- **Depende de:** T-0801
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `nats stream ls` → `NODE_STATE` (JetStream sobre `node.{id}.state`) y `nats consumer ls acct_flush` durable sobre `acct.{node_id}.flush`; `usage.audit` marcado como no-facturable en el runbook.
  2. Consumer de billing matado 5 min → mensajes pendientes > 0 y luego redelivered hasta `pending=0` (durable sin pérdidas; assert de count invariante del `usage`).
  3. `quota.exhausted` publicado por el backend → el gateway lo consume en < 1 s en el test de cuota (ligazón con T-0605).
- **DoD:** los eventos tienen topología nombrada y durables donde la factura lo exige; NATS es auditoría, nunca reloj maestro.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-api/nats/ -count=1` → 3 PASS sobre `nats-server -js` portable (topología: `NODE_STATE` sobre `node.*.state`, `ACCT_FLUSH` WorkQueue sobre `acct.*.flush`, durable `acct_flush` AckExplicit — equivalente mecánico de `nats stream ls`/`nats consumer ls`); consumer de billing "matado" con mitad pendiente → redelivered hasta `pending=0` e invariante contable (bytes de usage procesados = publicados, 0 pérdidas); `quota.exhausted` entregado en <1 s. `usage.audit` marcado no-facturable en `docs/runbooks/events.md`.

### T-0808 — Nonce de sesión ecotado por el nodo
- **Ficheros:** `internal/tunnel/session_nonce.go`, `cmd/botnet-gw/nonce.go`
- **Depende de:** T-0805
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** A3-08
- **Criterios de aceptación:**
  1. El gateway emite `nonce` en la asignación; el nodo lo ecota en `OPEN_STREAM`; ecotado incorrecto → stream rechazado con `closed_reason='error'` (fixture de nodo con bug que ecota `nonce-1`).
  2. Edge falso (fixture que responde sin prueba propia) no recibe bytes de tenant: el primer frame validado es el nonce, no datos (assert de orden en test de protocolo).
  3. Integración con dos tenants en el mismo nodo: los `bytes_in` de cada `session_id` cuadran ± 0,5 % — sin cruces de stream (el aislamiento deja de ser cortesía del nodo).
- **DoD:** el aislamiento de streams tiene prueba de protocolo: un campo, una validación, cero bytes cruzados.
- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel/ -run 'Nonce|TwoTenants|FirstFrame'` → 4 PASS (round-trip OPEN_STREAM de 12 B; fixture nodo-con-bug que ecota `nonce-1` → `ErrNonceMismatch` → stream rechazado con `closed_reason='error'`; `FirstFrameMustBeOpen`: `KindData` primero = edge falso sin bytes de tenant — el primer frame validado es el nonce; dos tenants en el mismo nodo: `bytes_in` por `session_id` dentro de ±0,5 %, 0 cruces) + `go test ./cmd/botnet-gw/ -run Nonce` → PASS (`NonceRegistry.HandleOpen` tipa 'error', `AllowData(false)` descarta datos previos al OPEN). El 201 de `/api/v1/sessions` ya porta `nonce` (assert en `TestCreateSession201Contract`).
