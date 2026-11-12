---
artifact: runbook
epic: EP-08
created: 2026-11-14
---

# Esquema Postgres del núcleo contable (T-0801)

## Cómo se migra

No hay `make` en este host: el equivalente a `make migrate` es

```bash
DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable \
  bash scripts/backend/migrate.sh
```

que invoca `botnet-api migrate` (runner embebido de `migrations/migrations.go`:
aplica `.up.sql` pendientes por transacción y las registra en
`schema_migrations`). Solo adelante: no existen archivos `*_down.sql`.

El plan del task pedía `0002_core.sql`; el número 0002 ya lo ocupa
`0002_discovered.up.sql` (EP-01), así que el core entró como **0003** y los
índices como **0004**, respetando el orden lexicográfico del runner.

## Tablas y sus preguntas contables

| Tabla | Pregunta que responde | Invariantes duros |
|---|---|---|
| `tenants` | quién paga | `stripe_customer_id UNIQUE`, status ∈ active/suspended/closed |
| `api_keys` | qué puerta del tenant abre cada request | `key_hash char(64) UNIQUE`, mode sticky/rotating, `sticky_ttl_s` 30–3600 |
| `nodes` | qué dispositivo hay en la malla | `slug char(12) UNIQUE` (transparencia A3-01), `device_token_hash char(64) UNIQUE`, arch/device_class CHECK, status ∈ enrolled/live/down/retiring/retired/burned |
| `node_secrets` | con qué HMAC firma este nodo (A3-06) | `octet_length(secret)=32`; índice único parcial `(node_id) WHERE revoked_at IS NULL` → **un secreto vivo por nodo** |
| `sessions` | qué hizo cada sesión (retención 30 días) | `bytes_in/out >= 0`, `closed_reason` ∈ quota/error/client/goaway/drain/purge |
| `usage` | medidor autoritativo diario por nodo (12 meses) | PK `(day, node_id)`, `bytes >= 0` |
| `quotas` | cuánto GB y cuántas concurrentes por periodo | `gb_allowance > 0`, `concurrent_limit` 1–500, UNIQUE `(tenant_id, period_start)` |
| `leaves` | cuándo y cómo se bajó un dueño | method CHECK, `reprobe_blocked_until` ≥12 meses |

## Índices nombrados (0004)

- `api_keys_active_tenant (tenant_id) WHERE status='active'` — warmup y validación de keys vivas (T-0804).
- `nodes_status_seen (status, last_seen_at)` — watchdog: qué nodo lleva sin heartbeat.
- `nodes_geo (country, asn, city)` — selección de exit del cliente (T-0805).
- `nodes_optout (optout_at) WHERE optout_at IS NULL` — suelo legal.
- `node_secrets_live_per_node UNIQUE (node_id) WHERE revoked_at IS NULL` — invariante A3-06.
- `sessions_node_started (node_id, started_at)` / `sessions_tenant_open (tenant_id, started_at) WHERE ended_at IS NULL`.
- `leaves_node_left (node_id, left_at)` / `leaves_code (leave_code)` — puerta legal no numerable (A3-13).

## Verificación

`DATABASE_URL=... go test ./migrations/...` → aplica 0003/0004 sobre una base
scratch (`CREATE DATABASE` efímero), comprueba las 8 tablas, los tipos
`char(12)`/`char(64)` con su UNIQUE, el CHECK de 32 B, el rechazo del segundo
secreto vivo y la ausencia de `*_down.sql`. Sin `DATABASE_URL` el test se omite
por diseño (el build global no requiere motor).

Sin ORM: `grep -rn "gorm\|sqlx" cmd/botnet-api` = 0 impactos.
