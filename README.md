# BOT.NET

Red de proxies residenciales en modelo **backconnect** sobre dispositivos
abiertos (routers, cámaras, NAS): un binario estático Go por nodo, una sola
conexión larga saliente TLS/H2, y el cliente nunca ve la IP del bot. El
suelo del monorepo sigue `docs/blueprint/BLUEPRINT.md` (árbol de §3) y el
plan de `docs/blueprint/BACKLOG.md`; las 102 tasks de `docs/blueprint/TASKS/`
se construyen sobre este esqueleto.

## Requisitos

- Go 1.27.x (el PATH y `CGO_ENABLED=0` los fija `source scripts/env.sh`)
- pnpm 10 + Node 24 para `web/` (aislado del workspace padre con
  `web/pnpm-workspace.yaml`)
- Opcional: `golangci-lint` (si no, `make lint` decae a `go vet`) y `upx`
  (si no está, `make matrix` simplemente no comprime)

## Construir

```bash
source scripts/env.sh   # Go en PATH, CGO_ENABLED=0
make build test vet     # el suelo debe estar verde
make matrix             # puerta F1: botd estatico <= 4 MB en 5 archs
```

## Objetivos del Makefile

| Objetivo | Qué hace |
|---|---|
| `build` | `go build ./...` — los 8 binarios de `cmd/` |
| `test` | `go test ./...` — tests del contrato (`pkg/proto`) y del estado |
| `vet` | `go vet ./...` |
| `lint` | `golangci-lint` si existe; fallback a `go vet` |
| `race` | `go test -race` solo en CI/servidor (el router no compila con race) |
| `run-stub` | ejecuta el stub `botd --version` |
| `budget` | hoja de recursos del §2; se endurece con `scripts/budget.sh` (EP-10) |
| `matrix` | `scripts/build-matrix.sh`: mips, mipsle, arm, arm64, amd64 con `-ldflags="-s -w"`, UPX opcional y fallo si un binario pasa de 4 MB |

## Dónde está qué

- `pkg/proto/` — contrato agente↔backend↔gateway: codec binario propio con
  versión en byte, heartbeat de 200 B + relleno ≤55 B, framing `CtrlFrame`.
- `migrations/` — DDL Postgres solo hacia adelante; runner `embed.FS` mínimo
  hasta EP-08 (`0001_init.up.sql` es placeholder).
- `deploy/docker-compose.yml` — Postgres 16 + Redis 7 + NATS 2 (JetStream) +
  api/gw/rdv/web para desarrollar.
- `docs/api/openapi.yaml` — contrato HTTP 3.0 para panel y cliente.
- `web/` — esqueleto Next.js 15 (`pnpm -C web build`).

## Estado del proyecto

El estado real, la deuda y el plan de salida viven en `docs/state/`:

- `docs/state/ESTADO-ACTUAL.md` — inventario AS-IS con métricas recomputadas.
- `docs/state/DEUDA-Y-RIESGOS.md` — deuda, riesgos y criterios pendientes de campo.
- `docs/state/PROXIMOS-PASOS.md` — plan priorizado con criterio de cierre por paso.

Se valida con `bash scripts/check-state-doc.sh`, que recomputa cada cifra contra el repo.
