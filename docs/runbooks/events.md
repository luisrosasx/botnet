---
artifact: runbook
epic: EP-08
created: 2026-11-14
---

# Topologia de eventos NATS (T-0807)

Regla madre: **NATS es auditoria, nunca reloj maestro**. Lo facturable
se apoya en durables; lo demas puede perderse sin deformar la factura.
El bootstrap (`cmd/botnet-api/nats/bootstrap.go`) es idempotente:
rearrastrar el backend no duplica streams ni reinicia durables.

## Streams JetStream (con nombre)

| Stream | Sujetos | Retencion | Para que |
|---|---|---|---|
| `NODE_STATE` | `node.{id}.state` | Limits, 7 dias | el panel lee el ultimo estado; el viejo no interesa |
| `ACCT_FLUSH` | `acct.{node_id}.flush` | **WorkQueue** (cada flush se factura una sola vez), 72 h | la contabilidad fina del gateway al backend |

## Consumers

| Consumer | Stream | Tipo | Notas |
|---|---|---|---|
| `acct_flush` | `ACCT_FLUSH` | **durable**, AckExplicit, AckWait 60 s, DeliverAll, MaxWaiting 1024 | el consumer de billing puede morir 5 min: los flushes quedan pendientes y se redeliveran hasta `pending=0` — el total facturado es invariante (test `TestDurableAcctFlushNoLoss`) |

## Plano core (sin persistencia, no facturable)

| Sujeto | Productor | Consumidor | Nota |
|---|---|---|---|
| `quota.exhausted` | backend (`Publisher.PublishQuotaExhausted`) | gateway | invalida la carrera del cache de 30 s (A3-04); el test mide <1 s |
| `usage.audit` | backend/gateway | auditoria | **marcado no-facturable**: es prueba, no medidor; el medidor autoritativo es `usage` en Postgres (diario) y `sessions` (grano sesion) |
| `node.{id}.hb` | gateway | quien escuche | latidos crudos; mejor esfuerzo |
| `ctl.{node_id}.cmd` | backend | agente via gateway | update/reboot/burn; el ack viaja por `CmdAckMsg` (T-0409) |
| `gw.{gw_id}.alloc` | — | gateway | peticion de asignacion con nonce (T-0808) |

## Comprobacion en vivo (equivalente a `nats stream ls` / `nats consumer ls`)

`TestBootstrapTopology` asserts exactly the same (StreamInfo/ConsumerInfo):
NODE_STATE sobre `node.*.state`, ACCT_FLUSH sobre `acct.*.flush` y el
durable `acct_flush` con AckExplicit. Prueba:
`go test ./cmd/botnet-api/nats/` (testenv arranca nats-server -js portable).
