---
artifact: epic
id: EP-08
title: "Backend: API, modelo de datos, credenciales y aislamiento"
owner_layer: backend
closes: A3-02, A3-03, A3-08, A3-X1, A3-X3, A3-19, injerto 9
estimate_days: 8
risk: medium
phase: F3
---

## Objetivo

Levantar `botnet-api` (Go + Fiber, handler → service → repo, SQL plano sin ORM) con el DDL contable e inventorío del blueprint, la cadena de credenciales (CA offline que firma CSR, CRL en Redis) y el contrato cliente↔gateway `POST /api/v1/sessions` (injerto 9). El gateway no lleva lógica de negocio: consume los mismos services por API interna y NATS.

## Alcance

- `migrations/0001→000N` adelante, sin ORM: `tenants`, `api_keys`, `nodes`, `node_secrets`, `sessions`, `usage`, `quotas`, `leaves` con los índices del blueprint.
- `POST /v1/enroll`: validación de CSR y firma de la CA offline; el par nunca sale del dispositivo (A3-02); emisión del `node_secret` con columna y un secreto vivo por nodo (A3-06 backend, A3-03).
- CRL en Redis `revoked:{serial}` con refresh 55 s: revocar cierra la puerta en < 60 s.
- `apikey:{sha256}` → tenant con warmup (A3-07): cero queries Postgres por request en caliente.
- `POST /api/v1/sessions` con Bearer api key y selección país/ASN/ciudad/sticky → `201 {session_id, proxy_url, expires_at}`; `402`/`403`/`429` (A3-X1, injerto 9).
- Redis degradado declarado (A3-X3, absorbe A3-09): sobre-cobra dentro de tolerancia, 503 a sesiones nuevas, presencia re-anudada por heartbeat; test de caos de 90 s en F3.
- Nonce de sesión ecotado y validado (A3-08): el stream no etiqueta a ciegas.
- NATS: `node.{id}.hb`, `node.{id}.state` (JetStream `NODE_STATE`), `acct.{node_id}.flush`, `ctl.{node_id}.cmd`, `gw.{gw_id}.alloc`, `quota.exhausted`, `usage.audit`.

## Fuera de alcance

- La selección de pool y el 307 de localidad (EP-09); el medidor autoritativo y conciliación (EP-10); el panel que consume las rutas (EP-11). Redis/NATS se aprovisionan en infra, no se diseñan aquí.

## Hallazgos que cierra

- A3-02 (S2): CSR en el enroll — la clave privada no viaja ni deja copia.
- A3-03 (S2) y A3-06 (S1, pata backend): `node_secrets` con columna, emisión y rotación; la HMAC pasa a ser por nodo.
- A3-08 (S2): nonce ecotado — aislamiento de stream con prueba, no cortesía del nodo.
- A3-07 (S2): `apikey:{sha256}` existe y se calienta; «validar contra Redis» deja de ser eufemismo.
- A3-09 (S2) / A3-X3 (S2): modo degradado con Redis caído, declarado y probado en caos.
- A3-X1 (S2) / injerto 9: el contrato cliente↔gateway que P1 no tenía.

## Criterios de aceptación de la épica

1. `make migrate` sobre Postgres limpio → 0 errores; `psql -c "\d nodes"` lista `slug char(12) UNIQUE` y `device_token_hash char(64) UNIQUE`; no existe migración down (grep `DOWN` en migrations/ = 0).
2. `POST /v1/enroll` con CSR válido → `201` con `cert_pem` verificable contra `ca_pem` (`openssl verify -CAfile ca.pem cert.pem` → OK); CSR con firma forjada → `400`; `node_secret` presente en el `201` y en `node_secrets` (1 fila viva por nodo, índice único).
3. `psql -c "SELECT count(*) FROM pg_stat_statements WHERE query LIKE 'api_keys%'"` delta = 0 tras 1.000 validaciones de api key en caliente (warmup hecho); cache frío → exactamente 1 query y luego 300 s de cache.
4. `make contract-test` → `POST /api/v1/sessions` devuelve `201` con `proxy_url` parseable `socks5h://`, `403` sin key, `402` con cuota agotada (fixture) y `429` al superar el rate-limit por tenant (100 req en 1 s).
5. Caos: `redis-cli DEBUG RELOAD` / parar Redis 90 s con sesiones vivas → 0 sesiones vivas cortadas, `503` en sesiones nuevas durante la caída, error de `gb_used` tras recuperación ≤ 0,5 %.

## Dependencias

EP-03 fija el contrato del enroll y del heartbeat que aquí se sirve. EP-09 consume apikey/contrato/pools. EP-10 escribe sobre `sessions`/`usage`/`quotas`. EP-12 mide las SLO de estas rutas.

## Definición de hecho

Dos implementadores que lean `migrations/` y el contrato OpenAPI construyen la misma puerta: el enroll firma CSR sin ver la clave privada, la revocación cierra la puerta en < 60 s, y 90 s sin Redis no cortan ni una sesión viva.
