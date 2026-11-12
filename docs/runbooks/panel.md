# Runbook — Panel del tenant y URL del dueño (EP-11)

Dos caras del mismo sistema: el **panel** (el tenant gestiona su cuota,
sus claves y sus facturas con una API key Bearer) y la **URL del dueño**
(página pública de transparencia con consentimiento fechado y salida
por `leave_code`). Todo ello en `web/` (Next 15 app router) contra la
API Fiber real — sin backend de fachada.

## Rutas

| Cara | Ruta | Nota |
|---|---|---|
| Panel | `GET /v1/panel/usage` | cuota + `gb_used_live` desde Redis (< 2 s) + histórico 30 días desde Postgres |
| Panel | `GET/PATCH /v1/panel/quotas` | PATCH mueve los 3 campos: `gb_allowance`, `concurrent_limit`, `mode` (sticky) |
| Panel | `GET/POST /v1/panel/api-keys` | el claro **solo viaja** en la respuesta del POST; en Postgres vive `sha256` (contrato `apikey:{sha256}` del gateway); la lista no tiene secretos |
| Panel | `GET /v1/panel/invoices` | facturas FakeStripe con `stripe_customer_id` |
| Público | `GET /t` (web) ← `GET /v1/nodes` (API) | directorio: solo estados `live\|enrolled\|down\|retiring`; jamás `device_token` |
| Público | `GET /t/{slug}` (web y API) | SSR con tabla, tope `GB/mes`, GB del mes y consentimiento fechado; rate-limit 60 req/60 s por IP (`rate:transparency:{ip}`, Redis) |
| Salida | `GET /t/{slug}/leave?leave_code=XXXXXXXX` | el código de 8 chars es la puerta `owner_url`; baja → `status: retiring` |

Autenticación del panel: `Authorization: Bearer <api key>`, validada
contra Redis vía `backend.ApiKeyCache` (miss 30 s); clave desconocida →
403 sin revelar por qué.

## Arranque local (receta e2e)

```bash
source scripts/env.sh
# 1) Postgres con migraciones + semilla demo (slug 12 chars + código 8):
DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet_panel?sslmode=disable \
  go run ./scripts/panel/seed_demo            # → slug=panel4demo0x leave_code=DEMO0001
# 2) API:
PORT=18080 DATABASE_URL='postgres://botnet@127.0.0.1:5433/botnet_panel?sslmode=disable' \
  REDIS_URL='redis://127.0.0.1:16379' ./dist/botnet-api serve
# 3) Web (build previo con pnpm -s build):
cd web && PORT=3100 BOTNET_API_URL='http://127.0.0.1:18080' pnpm -s start
# 4) Humo con el stack arriba:
make panel-e2e        # o: cd web && pnpm -s test  (node --test, sin playwright)
```

`web/.env.example` documenta `BOTNET_API_URL` (y NUNCA un `sk_live` —
`grep -c sk_live web/.env.example` = 0: la clave secreta de Stripe no
baja al navegador). Las páginas de panel leen la API key del
`localStorage` del navegador ("use client" en
`web/app/panel/[section]/panel-section.tsx`); las de transparencia son
SSR puro (el navegador no necesita clave).

## Desviaciones del plano (cierre F5)

- El plano decía `panel/pages/*.tsx`; el web real es Next app router en
  `web/app/**` (`/panel/[section]` con secciones
  usage|api-keys|quotas|invoices, `/t`, `/t/[slug]`, `/t/[slug]/leave`).
- El webhook sigue montado en `POST /v1/billing/webhook` (ruta real del
  backend desde EP-10); no se duplicó `/v1/webhooks/stripe`.
- La puerta 428 de enroll (sin `consent_at`) y `StampOptout` viven en
  `cmd/botnet-api/services` (`enroll.go`, `consent.go`); el método
  whitelisted de `leave` es `http|panel|owner_url` (+`heartbeat_bye`
  interno) según `handlers/leave.go`.
- Runner de humo: `node --test web/e2e/smoke.spec.mjs` (playwright no
  instalado en el host F5).

## Límites y guardia

- Transparencia pública: 60 peticiones por IP por minuto; la 61.ª → 429
  (ventana fija Redis). El directorio `/v1/nodes` no lleva credencial:
  si se llena de bots, se le monta el mismo limiter.
- El `device_token` no asoma por ninguna ruta pública (grep SSR = 0);
  la purga RGPD a los 13 meses se opera con `make rgpd-check`
  (ver `docs/runbooks/rgpd.md`).
