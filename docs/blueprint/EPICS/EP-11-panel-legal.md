---
artifact: epic
id: EP-11
title: Panel y transparencia legal
owner_layer: frontend
closes: A1-16, A3-X5, A3-10, injertos 4, 12
estimate_days: 5
risk: low
phase: F5
---

## Objetivo

Dar cara al negocio: panel del tenant (Next.js + Stripe) con cuotas, uso en vivo e histórico, y la página pública de transparencia por token (injerto 4) para el dueño sin `curl`: una URL, no una API. El suelo legal con memoria (injerto 12): consentimiento y baja fechados en la fila del nodo, borrado RGPD con retención contable y directorio de transparencia.

## Alcance

- `GET /t/{slug}`: flota, modelo, tope de ancho, GB del mes, consentimiento fechado y enlace de baja con `leave_code`; rate-limit por IP; sin fuga del `device_token`.
- Consentimiento y baja fechados: `consent_at` en el alta, `optout_at` en la baja; tabla `leaves` con `method` y `reprobe_blocked_until` (injerto 12, A3-10).
- Borrado RGPD: `mac`, `device_token` y `public_ip` con fecha de borrado; persisten `mac_hash` y `device_token_hash`; `deleted_at` con retención contable (A3-X5).
- Webhook Stripe firmado con HMAC y conciliación diaria con `usage` (A3-X5).
- Panel Next.js: `api_keys`, cuotas, uso en vivo (Redis) e histórico (Postgres), facturas por webhook.
- Rotación de campos del heartbeat por `codec_ver` (A1-16): el plano de control deja de auto-denunciar `proxy_pid/proxy_rss/bytes_delta` con catálogo fijo.
- Página enlaza el directorio de transparencia y el tope de 150 GB/nodo/mes publicado.

## Fuera de alcance

- La baja por pull con flag `bye` la ejecuta el agente (EP-04/EP-03: el `POST /v1/nodes/{id}/leave` queda para el panel); el `leave_code` lo emite EP-07. El cobro real e2e es puerta F5 entre EP-08/EP-10/EP-11; aquí el panel que lo opera. Menos de 8 tasks no hace falta: 7 tasks reales cubren página, consentimientos, RGPD, webhook, panel, codec y directorio.

## Hallazgos que cierra

- A3-X5 (S3): webhook firmado, borrado con política y consentimiento registrado — los tres cabos legales dejan de estar sueltos.
- A3-10 (S3): lo legal ya no llega tarde — `consent_at`/`optout_at` sobreviven al reboot y al refrito de firmware.
- A1-16 (S3): campos del heartbeat rotados por `codec_ver` — el control sin credenciales no regala un catálogo estable al DPI.
- Injertos 4 y 12: página pública por token y suelo legal con memoria.

## Criterios de aceptación de la épica

1. `curl -s https://…/t/$SLUG | grep -c "$DEVICE_TOKEN"` = 0 y `grep -cE 'leave_code=[A-Za-z0-9]{8}'` = 1; `$SLUG` valida `^[A-Za-z0-9]{12}$`; rate-limit: rama de 61 requests en 60 s desde una IP → ≥ 1 respuesta `429`.
2. `psql -c "SELECT consent_at IS NOT NULL FROM nodes WHERE node_id='$ID'"` = t tras el alta; baja por página → `optout_at` no nulo y fila en `leaves` con `method IN ('http','heartbeat_bye','panel','owner_url')`.
3. Fixture de retiro con `left_at` hace 13 meses → `public_ip` NULL, `mac_hash` no nulo, `deleted_at` no nulo; `make reprobe-guard` → 0 nodos con `optout_at` no nulo devueltos como candidatos de descubrimiento.
4. Webhook: POST sin firma → `400`; firma HMAC forjada → `403`; firma válida → cuota actualizada. `make reconcile` sobre fixture de 24 h → desvío `usage`↔Stripe ≤ 0,5 %.
5. `npx playwright test panel/` (smoke: alta de api key, lectura de uso en vivo desde Redis en < 2 s, histórico desde Postgres) pasa en CI; `go test ./internal/hbcodec -run TestCodecVerRotation` → dos `codec_ver` producen layouts de bytes distintos (hexdiff ≠ 0).

## Dependencias

EP-03 entrega `slug` y `consent_at`; EP-07 entrega `leave_code` y `geo_verified`; EP-08 entrega `leaves` y el contrato; EP-10 entrega `usage`, cuotas y conciliación; EP-04 consume `bye` y `codec_ver`.

## Definición de hecho

Un dueño sin terminal puede ver qué corre su router, cuánto consumió y darse de baja desde un enlace con código; un retirado hace 13 meses no tiene IP ni token en claro ni vuelve al barrido; y un cobro de Stripe concilia con el medidor del nodo dentro de 0,5 %.
