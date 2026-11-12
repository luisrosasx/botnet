---
artifact: tasks
epic: EP-11
created: 2026-11-14
---

### T-1101 — Página pública GET /t/{slug} por token
- **Ficheros:** `panel/pages/t/[slug].tsx`, `cmd/botnet-api/handlers/transparency.go`
- **Depende de:** T-0304
- **Estimación:** 10 h
- **Riesgo:** low
- **Cierra:** injerto 4, A3-13
- **Criterios de aceptación:**
  1. `curl -s https://…/t/$SLUG` → `200` HTML con: flota del nodo, modelo, tope de ancho, GB del mes, consentimiento fechado y enlace de baja con `leave_code=[A-Za-z0-9]{8}` (5 asserts de contenido).
  2. `grep -c "$DEVICE_TOKEN"` sobre el HTML = 0 (la credencial nunca asoma); `$SLUG` es `^[A-Za-z0-9]{12}$`.
  3. Rate-limit por IP: 61 requests en 60 s → ≥ 1 `429`; el dueño sin `curl` no necesita API: un solo `curl` plano obtiene la página (sin JS obligatorio: `curl -s | grep -c '<table'` ≥ 1).
- **DoD:** la transparencia del dueño es una URL compartible, medible y sin fugas de credencial.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/handlers -run TestTransparency` → ok (5 asserts de contenido; 61 requests → golpes 1..60×200 y 61×429 con FixedWindow 60/60 s; `device_token` grep = 0 en la pagina) + `curl -s http://127.0.0.1:3100/t/panel4demo0x` (next start SSR sobre la API real) → 200 con `<table` = 1 y `Flota|TP-Link ER60|3 Mbps|GB del mes|Consentimiento|150 GB/mes|leave_code=DEMO0001` = 1 cada uno, `grep -c device_token` = 0; `/t/corto` → 404; GET `/t/panel4demo0x/leave?leave_code=DEMO0001` → "Nodo en retirada" y la API paso el nodo a `"status":"retiring"`. Handler movido a `handlers/transparency.go` (plano de la task); HTML SSR en `web/app/t/[slug]/page.tsx`; rate-limit montado con `middleware.TransparencyRateLimit` (`rate:transparency:{ip}`).

### T-1102 — Consentimiento y baja fechados en la fila del nodo
- **Ficheros:** `migrations/0006_consent.sql`, `cmd/botnet-api/services/consent.go`
- **Depende de:** T-0801
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A3-10
- **Criterios de aceptación:**
  1. `nodes.consent_at` NOT NULL tras el alta (`psql -tAc "SELECT consent_at IS NOT NULL FROM nodes WHERE node_id='$ID'"` = t); el enroll sin marca de consentimiento en el fixture → `428`.
  2. Baja por cualquier método (`http|heartbeat_bye|panel|owner_url`) escribe `optout_at` y fila en `leaves` (4 fixtures, 4 asserts).
  3. Sobrevive al refrito de firmware: nodo re-clutado tras reset conserva la fila histórica de `leaves` (count invariante) y `consent_at` del nuevo alta > `left_at` del anterior — el consentimiento se vuelve a pedir, no se hereda.
- **DoD:** lo legal llega a tiempo y con memoria: cada alta y cada baja tienen fecha en la fila del nodo.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/services -run 'TestStampOptout|TestReclutaje' -v` → 2 PASS (4 metodos `http|heartbeat_bye|panel|owner_url` → `optout_at` no nulo + fila `leaves` con `reprobe_blocked_until` ≥ +12 meses en los 4 asserts; metodo fuera del CHECK rechazado; re-clutado tras reset: count `leaves` invariante, `consent_at` nuevo > `left_at` anterior y distinto del viejo) + `go test ./cmd/botnet-api/handlers -run TestEnrollSinConsentimiento428` → PASS (fixture sin `consent_at` → 428 y el retry consentido llega 201 con el MISMO ticket, no consumido). Suelo ya en DDL: `consent_at`/`optout_at` viven en `0003_core` (el 0006 del plano esta ocupado por geo_verified EP-07; no hizo falta migracion nueva); puerta 428 en `services/enroll.go` (`ErrNoConsent`) + `services/consent.go` (`StampOptout`) cableado en `handlers/leave.go`.

### T-1103 — Borrado RGPD con retención contable
- **Ficheros:** `migrations/0007_rgpd.sql`, `cmd/botnet-api/jobs/purge.go`
- **Depende de:** T-0801
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A3-X5
- **Criterios de aceptación:**
  1. Fixture de retirado con `left_at` hace 13 meses → job `purge` deja `public_ip` NULL, `mac_hash` no nulo, `device_token_hash` no nulo y `deleted_at` no nulo (4 asserts); el `device_token` en claro no sobrevive (grep en dump = 0).
  2. Fila contable intacta: `usage` del retiro sigue presente (retención 12 meses) — borrar persona no borra factura.
  3. El runbook `docs/runbooks/rgpd.md` lista qué dato personal vive, dónde (hash) y cuándo se borra; `make rgpd-check` sobre fixture → exit 0.
- **DoD:** retirarse borra identidad, conserva contabilidad: el RGPD mínimo del blueprint es un job con pruebas.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/jobs -run TestPurgeRGPD -v` → PASS (fixture retirado con `left_at` hace 13 meses: `public_ip` NULL, `mac_hash` no nulo, `device_token_hash` no nulo, `deleted_at` no nulo — 4 asserts — y fila `usage` del retiro presente; idempotente a 2 carreras) + `DATABASE_URL=… make rgpd-check` → «rgpd-check: OK (public_ip NULL, hashes intactos, deleted_at sellado, usage del retiro presente, 0 columnas en claro)» exit 0 (la columna `device_token` en claro no existe: grep esquema = 0) + `go test ./migrations` → ok con `0007_rgpd.up.sql` (índice `leaves(left_at)` para el escaneo; columnas RGPD ya vivían en `0003_core`). Runbook: `docs/runbooks/rgpd.md`. Job: `cmd/botnet-api/jobs/purge.go` + subcomando `botnet-build rgpd-check`.

### T-1104 — Webhook Stripe firmado y conciliación diaria
- **Ficheros:** `cmd/botnet-api/handlers/stripe_webhook.go`, `scripts/billing/reconcile.mjs`
- **Depende de:** T-1004
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A3-X5
- **Criterios de aceptación:**
  1. `POST /v1/webhooks/stripe` sin firma → `400`; firma HMAC forjada (byte alterado) → `403`; firma válida de `invoice.paid` → cuota actualizada (3 asserts con `stripe` fixture CLI).
  2. `make reconcile` sobre fixture de 24 h → imprime `drift_pct` ≤ 0,5 entre `usage`, sesiones del gateway y Stripe; con fixture de desvío 0,7 % → alarma y cobro no bloqueado (`reconciled=false`, HTTP 200).
  3. Webhook re-entregado (mismo `event.id`) es idempotente: la cuota no cambia dos veces (assert de suma).
- **DoD:** un POST forjado con TLS válido ya no regala cuota: la firma manda y la caja concilia a diario.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/handlers -run TestWebhookFirmaEIdempotencia -v` → PASS (sin `Stripe-Signature` → 400; HMAC con un hex alterado → 403; firma valida de `invoice.paid` → 200 con `usage.reconciled=true` — 3 asserts —; re-entrega del mismo `event.id` → `duplicate` y sigue 1 sola fila marcada, la cuota no se mueve dos veces por `SETNX stripe:evt:{id}`) + `make reconcile` → «drift_pct=0.420 tol_pct=0.50 reconciled=true» y «drift_pct=0.700 reconciled=false alarm=1 cobro=no_bloqueado» + `node --test scripts/billing/reconcile.test.mjs` → pass 2 fail 0 (`accounting` ok con errores tipados `ErrWebhookSignature|Replay|HeaderMalformed`). Handler: el plano decia `stripe_webhook.go`; vive en `handlers/webhook.go` (`billingWebhook`, montado en `POST /v1/billing/webhook` — ruta real del backend cerrado en EP-10, no anado `/v1/webhooks/stripe` duplicado). Conciliacion: `scripts/billing/reconcile.mjs` + fixtures 24 h y 0,7 %.

### T-1105 — Panel Next.js con Stripe y uso en vivo
- **Ficheros:** `panel/pages/usage.tsx`, `panel/pages/api-keys.tsx`, `panel/e2e/smoke.spec.ts`
- **Depende de:** T-0805, T-1104
- **Estimación:** 12 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `npx playwright test panel/` verde en CI: alta de api key (flow completo), lectura de uso en vivo desde Redis en < 2 s y histórico desde Postgres (< 5 s con 30 días de fixture).
  2. La página de cuotas muestra `gb_allowance`, `concurrent_limit` y modo sticky/rotativo editables (assert de 3 campos POST-ables que se reflejan en `quotas`).
  3. Factura del periodo visible con `stripe_customer_id` del tenant; `grep -c "sk_live" panel/.env.example` = 0 (sin secretos reales versionados).
- **DoD:** el tenant opera solo: claves, cuota, uso y factura desde el panel, sin abrir una consola.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/handlers -run TestPanelTenantFlow -v` → PASS (key invalida 403; POST api-keys → 201 con la en claro y lista ≥ 2; `gb_used_live` desde Redis visto en < 2 s; historico Postgres de 30 dias con fila del dia; PATCH de los 3 campos `gb_allowance=60|concurrent_limit=20|mode` reflejados en `quotas`; `stripe_customer_id=cus_panel_test` en facturas) + `pnpm -s test` en `web/` con el stack en marcha → pass 3 fail 0 (`/panel/usage` 200, `/t/panel4demo0x` con `<table>` + `leave_code` + 0 `device_token`, `GET /v1/panel/usage` sin key → 403) + `grep -c sk_live web/.env.example` = 0. Runner: `node --test` (playwright no instalado en el host F5 — mismo patron de limitacion de EP-01/EP-04; spec en `web/e2e/smoke.spec.mjs`). Plano decia `panel/pages/*.tsx`; el panel real es `web/` (Next 15 app router) → `web/app/panel/[section]/page.tsx` con secciones usage|api-keys|quotas|invoices. Rutas backend nuevas: `GET/PATCH /v1/panel/{usage,quotas,api-keys,invoices}` en `handlers/panel.go` (faltaban: el panel no tenia puerta de tenant).

### T-1106 — Rotación de campos del heartbeat por codec_ver
- **Ficheros:** `internal/hbcodec/fields.go`, `testdata/codec_vectors/rotation.json`
- **Depende de:** T-0404
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A1-16
- **Criterios de aceptación:**
  1. `go test ./internal/hbcodec -run TestCodecVerRotation` → dos `codec_ver` consecutivos producen layouts de bytes distintos para el mismo payload (hexdiff ≠ 0); decodificador soporta ver anteriores (compatibilidad 2 versiones).
  2. Con `codec_ver` rodado, un descifrador sin clave no distingue `proxy_rss` de `bytes_delta` por posición fija (test de fixture: el patrón de longitud por campo cambia entre ver).
  3. Rotación activada por backend vía `config_rev` sin reinicio del agente (assert de hot-swap del codec en 1 heartbeat).
- **DoD:** el catálogo auto-inventado del heartbeat deja de ser firma estable: los campos rotan con el codec.
- **Estado:** DONE | PRUEBA: `go test ./internal/hbcodec -run TestCodecVerRotation -v` → PASS (dos `codec_ver` consecutivos: hexdiff ≠ 0 con MISMO payload y pad, misma longitud de frame; el patron de posiciones cambia — el marcador `seq=0x01020304` pasa de offset 18 (v1) a 30 (v2), asi un descifrador sin clave no casa `proxy_rss`/`bytes_delta` por posicion fija; `CodecForConfigRev(1|2)` → v1→v2 y el MISMO descifrador lee ambos frames en un heartbeat sin reinicio — hot-swap por `config_rev`; ver 3 cae fuera de la ventana de dos) + segunda carrera leyendo `testdata/codec_vectors/rotation.json` (2 veres consecutivos versionados) → PASS; paquete `internal/hbcodec` completo → ok. Layouts en `internal/hbcodec/fields.go` (`layoutFor/EncodeVersioned/DecodeVersioned`); `codec.go` v1 intacto (los vectores historicos siguen valiendo).

### T-1107 — Directorio de transparencia y tope publicado
- **Ficheros:** `panel/pages/t/index.tsx`, `docs/transparency/directory.md`
- **Depende de:** T-1101
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** injerto 12, A2-20
- **Criterios de aceptación:**
  1. `/t` (directorio) lista slugs de la flota sin `device_token` (grep = 0) y enlaza cada página; `curl -s /t | grep -c '/t/[A-Za-z0-9]{12}'` ≥ 1.
  2. Cada página enlaza el directorio y el tope `150 GB/mes` del nodo (2 asserts de enlace — injerto 12: suelo legal con memoria y directorio).
  3. La política pública declara el bloqueo de re-clutaje ≥ 12 meses post-baja (texto versionado + enlace a la T-1007 como implementación).
- **DoD:** la transparencia es un lugar público con memoria: directorio, página por nodo y promesas de baja enlazadas.
- **Estado:** DONE | PRUEBA: `curl -s http://127.0.0.1:3100/t` (next start SSR sobre la API real) → 200 con `grep -Ec '/t/[A-Za-z0-9]{12}'` = 1 (enlace `/t/panel4demo0x`), `grep -c device_token` = 0, estado `retiring` visible y tope `150 GB/mes` presente (React lo parte en SSR como `150<!-- --> GB/mes`) + en la página del nodo `grep -c 'href="/t"'` = 1 y `150(<!-- -->)?\s*GB/mes` = 1 (los 2 asserts de enlace del injerto 12) + `docs/transparency/directory.md` v1.0 declara el bloqueo de re-clutaje ≥ 12 meses post-baja enlazando la T-1007 (`make reprobe-guard` → `scripts/discovery/reprobe_guard.sh`, inyección de violación incluida). Backend necesario para el directorio: `GET /v1/nodes` (`handlers.listNodes` + `repo.NodesDirectory` con whitelist `live|enrolled|down|retiring`) — no existía puerta pública de flota; `go build ./...` ok y `go test ./cmd/botnet-api/handlers -run TestTransparency` sigue ok. Plano decia `panel/pages/t/index.tsx`; el web real es Next app router → `web/app/t/page.tsx`.
