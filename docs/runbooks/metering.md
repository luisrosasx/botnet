# Runbook: medidor autoritativo (EP-10 T-1001)

El BLUEPRINT §11 cierra A2-18 con un mandato: **un solo reloj maestro y
una sola tolerancia**. Este runbook declara cuál es cada uno y cómo se
mide, para que la disputa nodo-vs-gateway deje de ser margen.

## El reloj maestro: el nodo

El agente (`botd`) cuenta los bytes de cada stream con su propio
contador (`internal/proxy/counters.go`) y los vuelca en el flush
`acct.{node_id}.flush`. Ese contador del nodo es **la única fuente
facturable**: la tabla `usage` se construye del flush del nodo.

El **gateway** y **NATS** son auditoría: `usage.audit` viaja por el
plano de eventos, se concilia y **nunca se factura**. Perder un evento
NATS de auditoría jamás cambia una factura (test T-1002).

## La tolerancia única: 0,5 %

`accounting.TolerancePct = 0.5` — alineada con el redondeo de Stripe
(GB decimal, `accounting.GB = 10^9`). Fuera el menú de las rivales: no
hay tolerancia de un punto porcentual ni de cinco puntos; cualquier
desvío mayor del 0,5 % es **alarma** (conciliación `usage.audit`), no
segunda capa de margen.

| Qué | Quién lo mide | Rol |
|---|---|---|
| `usage.bytes` (facturable) | medidor del nodo | reloj maestro |
| bytes del gateway | speculo del proxy | auditoría |
| `usage.audit` (NATS) | plano de eventos | auditoría, nunca cobro |

## Pruebas que lo garantizan

- `go test ./internal/accounting -run TestMetering1GB` → transferencia
  conocida de 1 GB por sesión; `drift_pct=0.42(≤0.5)` queda impreso.
  Equivalente a `make metering-test`.
- `go test ./internal/accounting -run TestCrashLoss` → `kill -9` a
  mitad de sesión pierde ≤ 90 s de bytes: spool a disco cada 60 s
  (`accounting.SpoolInterval`) + 30 s de detección
  (`accounting.CrashDetectionMargin`); el diff gateway-vs-usage se
  reconstruye del último spool (fixture en el test, reloj controlado).
- `go test ./internal/accounting -run TestToleranceUnica` → 0,51 % de
  desvío ya es alarma: no existe un segundo umbral que lo absorba.

## Regla de oro operativa

Si el nodo y el gateway discrepan, **gana el nodo** y se factura; la
conciliación diaria (`cmd/botnet-api/jobs`) abre ticket si el desvío
pasa del 0,5 %, pero el cobro nunca se bloquea.
