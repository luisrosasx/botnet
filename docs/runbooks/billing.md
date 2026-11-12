# Runbook: facturación EP-10 (contabilidad, cuotas y Stripe simulado)

La ruta contable completa, de la transferencia al cobro, con los
comandos que se corren a mano en este host (no hay `make`: se ejecuta
el comando de cada target). Postgres portable en `DATABASE_URL`,
redis/nats portables en `~/tools` (patrón `internal/backend/testenv`).

## La ruta, en orden

```
nodo (botd)                    backend (botnet-api)                Stripe (doble local)
─────────                      ──────────────────                  ──────────────────
medidor autoritativo  ──flush──▶ POST /v1/nodes/{id}/flush
contador + spool 60 s            dedup flush_id (SETNX Redis)       webhook invoice.paid
periodico 15 min ±jitter         usage(day,node_id) += total         firma HMAC-SHA256
                                 sessions.bytes_in/out por stream   Stripe-Signature:
                                                                    t=,v1=
                                 cuota: tenant:{t}:gb_used:{period}
                                 ≥ allowance → quota:{t}:state +
                                 NATS quota.exhausted → 402 < 60 s
                                 conciliación diaria usage↔sessions↔Stripe
```

- **Reloj maestro:** el nodo. `usage` se construye del flush del nodo;
  `usage.audit` (gateway/NATS) nunca se factura (T-1001,
  `docs/runbooks/metering.md`).
- **Dedup:** el reenvío del durable `acct.{node}.flush` responde
  `202 {dedup:true}` y no mueve la suma de `usage` (T-1002).
- **Tolerancia única 0,5 %:** la conciliación alarma; el cobro nunca se
  bloquea (`usage.reconciled=false` + métrica, T-1004).

## Comandos de verificación (todos en este host)

| Qué | Comando |
|---|---|
| Medidor y tolerancia (T-1001, `make metering-test`) | `go test ./internal/accounting -run 'TestMetering1GB|TestCrashLoss|TestToleranceUnica' -v` |
| Spool/flush/dedup (T-1002) | `go test ./internal/accounting -run 'TestSpool24h|TestFlushDedup|TestAuditPerdidoNoFactura'` y `go test ./cmd/botnet-api/handlers -run 'TestFlush'` |
| Retención (T-1003) | `go test ./cmd/botnet-api/jobs -run 'TestRetentionPolicies|TestUsageRollupGrain|TestRebuildUnder1s'` |
| Cuota < 60 s y 150 GB publicado (T-1004) | `go test ./cmd/botnet-api/services -run TestQuotaCut && go test ./cmd/botnet-api/handlers -run TestTransparencyPublishesCap150` |
| Stripe simulado (webhook firmado) | `go test ./internal/accounting -run TestFakeStripeDeliver` + `go test ./cmd/botnet-api/handlers -run TestFlushAndWebhookOnPostgres` |
| Conciliación diaria 3 patas (T-1004 AC3) | `go test ./cmd/botnet-api/jobs -run TestBillingReconcile` |
| Hoja plano de datos (T-1005, `make budget`) | `node scripts/budget/budget.mjs && node --test scripts/budget/budget.test.mjs` |
| Coste de recluta (T-1006, `make recruit-cost`) | `node scripts/budget/recruit_cost.mjs` |
| Guardia de re-clutaje (T-1007, `make reprobe-guard`) | `bash scripts/discovery/reprobe_guard.sh` |
| Precio por clase / margen (T-1008, `make margin-check`) | `node scripts/budget/margin.mjs --mix=70C,20B,10A` |

## Stripe: qué es real y qué es doble

EP-10 no toca red externa: el doble HTTP local
(`internal/accounting/fakestripe.go`) sirve `GET /v1/invoices?day=` y
`POST /deliver?day=&target=` entregando `invoice.paid` **firmado con
el mismo código** que verifica el backend
(`accounting.SignWebhook`/`VerifyWebhook`, esquema Stripe:
`Stripe-Signature: t=<unix>,v1=<hmac-sha256>`, ventana anti-replay
5 min). Cuando F5 conecte el Stripe real, el handler
`POST /v1/billing/webhook` y el verificador no cambian: cambia la
URL base. El secreto vive en `Deps.StripeSecret` (config), nunca en el
payload.

## Piezas del backend

| Pieza | Fichero |
|---|---|
| Medidor, tolerancia, GB decimal | `internal/accounting/meter.go` |
| Spool, envelope, dedup, plan 24 h | `internal/accounting/spool.go` |
| Webhook Stripe (firma + invoice.paid) | `internal/accounting/stripe.go`, `cmd/botnet-api/handlers/webhook.go` |
| Handler de flush + sink Postgres/Redis | `cmd/botnet-api/handlers/flush.go` |
| Puerta de cuota (Redis + NATS) | `cmd/botnet-api/services/quota.go` |
| GOAWAY con closed_reason='quota' | `internal/agent/quota_notify.go` |
| Conciliación diaria 3 patas | `cmd/botnet-api/jobs/billing_reconcile.go` |
| Retención 30 d / 12 meses | `cmd/botnet-api/jobs/retention.go` |
| Memoria del opt-out (+12 meses) | `cmd/botnet-api/jobs/leaves.go` |
| DDL de índices EP-10 | `migrations/0005_retention_leaves.up.sql` |

## Alarmas que se esperan (y qué no bloquean)

- `usage.audit` con `drift_pct > 0.5` → ticket; **el cobro sigue**.
- `quota.exhausted` publicado → sesiones nuevas 402; la carrera del
  cache de 30 s la absorbe la tolerancia, no la factura.
- Redelivery masivo del durable → `202 {dedup:true}` en cascada; si
  Redis cae, el dedup Falla CERRADO (no se aplica a ciegas): mejor
  redeliver de más que cobro duplicado.
