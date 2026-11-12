# Runbook: retención escalonada (EP-10 T-1003)

La lección A2-07 prohíbe el rollup por minuto para toda la flota: a
100.000 nodos serían 144 M de filas al día. El DDL (0003 + 0005) deja
el grano donde se paga y el resto diario:

| Tabla | Grano | Retención | Quién la purga |
|---|---|---|---|
| `sessions` | una fila por sesión (el minuto vive aquí) | **30 días** | `jobs.RetentionJob` (cron diario) |
| `usage` | una fila por `(day, node_id)` — PK | **12 meses** | `jobs.RetentionJob` |
| `leaves` | una fila por baja | permanente (memoria legal del opt-out) | nadie |

## La purga

`cmd/botnet-api/jobs/retention.go` corre un `DELETE` por tabla:

- `sessions` con `ended_at IS NOT NULL AND started_at < now() − 30 días`
  — una sesión viva de hace 31 días es anomalia operativa, no basura, y
  se deja para el triage.
- `usage` con `day < now() − 12 meses`.

En EP-10 la política es SQL plano bajo cron del backend (no pg_partman:
la dependencia no está en `go.mod` ni en el suelo del host); al migrar a
pg_partman, `docs/runbooks/schema.md` registra el cambio y el job queda
como respaldo idempotente.

## El cobro disputado a 60 días

La reconstrucción responde por `(node_id, started_at)` del día pedido:

```sql
SELECT session_id, gw_id, bytes_in, bytes_out, closed_reason
  FROM sessions
 WHERE node_id = $1 AND started_at >= $2 AND started_at < $3
 ORDER BY started_at;
```

El índice `sessions_node_started (node_id, started_at)` (0004) la sirve
en index scan: el test `TestRebuildUnder1s` siembra 100.000 filas y
aserta < 1 s con `EXPLAIN` en index scan. Como `usage` dura 12 meses,
la disputa de 60 días siempre encuentra la fila contable diaria.

## Verificación

- `go test ./cmd/botnet-api/jobs -run TestRetentionPolicies` → la fila
  de 31 días en `sessions` aparece purgada; la viva sobrevive.
- `go test ./cmd/botnet-api/jobs -run TestUsageRollupGrain` → PK
  `(day, node_id)` y 1.000 nodos × 1 día dan ≤ 1.000 filas de `usage`.
- `go test ./cmd/botnet-api/jobs -run TestRebuildUnder1s` → recon. a
  100k filas < 1 s con index scan.
