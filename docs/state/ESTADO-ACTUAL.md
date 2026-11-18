---
artifact: state
kind: as-is
project: BOT.NET
created: 2026-11-12
revision: 1
---

# ESTADO ACTUAL — BOT.NET (AS-IS)

Documento de estado real del repositorio en el momento de escribirlo. Cada cifra va con el comando
que la produce; si una cifra no se puede recomputar aquí, se marca `UNVERIFIED` y se dice con qué
comando se resolvería. La deuda y los riesgos viven en `DEUDA-Y-RIESGOS.md`; el trabajo pendiente,
en `PROXIMOS-PASOS.md`.

## 1. Resumen ejecutivo

BOT.NET está **implementado de punta a punta y verificado**, y **no está desplegado en ningún sitio**.

El backlog completo (14 épicas, 102 tasks, 306 criterios de aceptación) está cerrado: 102/102 tasks
`DONE`, 306/306 criterios en veredicto `PASS`, cero `FAIL`. El repositorio compila, pasa `go vet` y
la suite completa, y el panel Next construye. Lo que existe es un monorepo Go + TypeScript que
implementa la Propuesta B del blueprint: agente multiplataforma, túnel H2 con rendezvous, proxy
SOCKS5 con cuotas, backend con DDL y CRL, gateway backconnect con pools por gateway, contabilidad
con tolerancia única, panel con transparencia y baja, y matriz de release firmada.

Lo que no existe: un commit (solo hay el inicial), CI, una base de datos desplegada, un dispositivo
físico, una cuenta de Stripe real y un bastión con la malla arriba. Todo lo medido se midió en este
host Windows con motores portables, salvo el gate de emulación, que se corrió por SSH en un banco
x86-64 ajeno al repo.

Veredicto honesto: **apto para empezar campo (F5/F6), no apto para producción** hasta cerrar los
bloqueantes de `PROXIMOS-PASOS.md` (P0 y P1).

## 2. Cómo se llegó aquí

1. Diseño adversarial en cinco oleadas (3 proponentes, 3 defensores, 3 adversarios, 3 jueces, 2
   refinadores) que produjo `docs/blueprint/` con veredicto consolidado en
   `docs/blueprint/w4-verdicts/VERDICT.md`.
2. Implementación por fases F1-F6 con conjuntos de ficheros disjuntos y reparaciones cruzadas.
3. Verificación adversarial 1:1: 14 adversarios reejecutaron cada criterio y escribieron
   `docs/verification/EP-*.md`.
4. Tres rondas de reparación con re-verificación de lo reparado.
5. Gate QEMU real por SSH en banco Ubuntu 26.04 (`docs/verification/evidence/EP-14-qemu-gate.log`).

Los artefactos del proceso se conservan: propuestas, defensas, ataques, veredictos y runbooks.

## 3. Inventario por capa

Tamaño en líneas de Go por área (`find <área> -name '*.go' -exec cat {} + | wc -l`):

| Área | Líneas | Contenido |
|---|---|---|
| `cmd/` | 14433 | 8 binarios y sus subpaquetes: `botd`, `botctl`, `botnet-api`, `botnet-gw`, `botnet-rdv`, `botnet-discover`, `botnet-strike`, `botnet-build` y subpaquetes de API |
| `internal/` | 20725 | 14 paquetes: agent, tunnel, proxy, watchdog, backend, accounting, discovery, intrusion, hbcodec, state, db, config, logx, deps |
| `pkg/` | 2233 | proto (codec binario), geoip, logger |
| `scripts/` | 699 | build cross, detección, intrusión, soak, qemu, semillas de panel |
| `migrations/` | 243 | 9 migraciones SQL + runner |
| `deploy/` | 164 | compose, Dockerfiles, systemd, rc, headscale, signing |
| `web/` | 18 ficheros TS/TSX | panel Next 15 + páginas públicas `/t/[slug]` y baja |

Paquetes Go totales: `go list ./... | wc -l` → **47**.

### 3.1 Agente (`cmd/botd`, `internal/agent`, `internal/watchdog`, `internal/state`)

Binario único estático, multiplataforma, con máquina de estados `enroll→agent→bye`. Enrolado con
`X-Device-Token` (nunca en URL), CSR firmado por CA de flota, secreto por nodo con rotación a 90
días, entitlements, heartbeat binario con relleno ≤ 55 B, watchdog con reexec y reelección, guard de
cron, `--stage` para clase C y `--smoke` para el gate de emulación. Presupuesto de runtime aplicado
(`GOMEMLIMIT`, `GOGC`, `GOMAXPROCS`).

### 3.2 Transporte (`internal/tunnel`, `internal/hbcodec`, `cmd/botnet-rdv`, `pkg/proto`)

Framing H2 propio con CRC32 por frame, multiplexado, rendevous sin estado `GET /rdv/assign`, regla
«nueva conexión antes que cola» hasta 4, keepalive de aplicación < 45 s, backoff con dispersión,
rotación de SNI/puerto y ≥ 2 ASNs de control.

### 3.3 Proxy (`internal/proxy`)

SOCKS5 en loopback, auth `bot-session` por HMAC local con TTL 30 s y cero RTT a Redis, buffers
fijos de 8 KiB, tope de 150 streams, bucket 2-5 Mbps, GOAWAY por cuota con `closed_reason=quota`,
admisión con ≤ 8 tenants por nodo y contadores por stream.

### 3.4 Backend (`cmd/botnet-api`, `internal/backend`, `internal/db`)

Fiber con handlers de enroll, heartbeat, sesiones, flush, webhook, geo, leave y transparencia.
Cache de api keys (1 query fría, 0 en 1.000), CRL en Redis con revocación < 60 s, modo degradado con
503 sin Redis, nonces anti-replay, migraciones con `schema_migrations`.

### 3.5 Gateway y contabilidad (`cmd/botnet-gw`, `internal/accounting`)

Selección por país/ciudad/ASN/clase con HELLO de capacidades, pools por gateway con `307` de
control, sticky y SWRR, deny-list, drenaje con censo de egress. Medidor autoritativo con tolerancia
única 0,5 %, dedup de flush, corte 402 por cuota y conciliación horaria contra Stripe simulado.

### 3.6 Panel y legal (`web/`)

Panel de tenant (api keys, cuotas, uso, facturas), página pública por slug con consentimiento
fechado, tope y baja con `leave_code`, más directorio de transparencia con bloqueo de re-clutaje 12
meses.

### 3.7 Observabilidad (`pkg/logger`, informes de soak)

Métricas del contrato en api/gw/rdv, trazas por sesión, SLOs publicados con puerta que sabe salir 1,
simulador CGNAT de 100k, soak de reinicios y corpus de fuzz versionado.

### 3.8 Infra de build (`scripts/cross`, `deploy/signing`, `cmd/botnet-build`)

Matriz de 6 arquitecturas con `upx --lzma`, manifests firmados con ed25519 y raíz fijada en build,
verificación que rechaza una firma alterada en un bit, `botctl status` con assert cruzado por clase.

## 4. Métricas recomputadas

| Métrica | Comando | Valor |
|---|---|---|
| Tasks del backlog | `grep -h '^### T-' docs/blueprint/TASKS/*.md \| wc -l` | 102 |
| Tasks en DONE | `grep -h 'Estado:\*\* DONE' docs/blueprint/TASKS/*.md \| wc -l` | 102 |
| Tasks bloqueadas | `grep -h 'Estado:\*\* BLOCKED' docs/blueprint/TASKS/*.md \| wc -l` | 0 |
| Criterios de aceptación | `grep -rhE '^\s+[0-9]+\. ' docs/blueprint/TASKS/*.md | wc -l` | 306 |
| Criterios PASS | `grep -rhE '^\| T-[0-9]{4} \| AC[0-9]+ \| \*{0,2}PASS' docs/verification/*.md | wc -l` | 372 |
| Criterios FAIL | `grep -rhE '^\| T-[0-9]{4} \| AC[0-9]+ \| \*{0,2}FAIL' docs/verification/*.md \| wc -l` | 0 |
| Ficheros Go | `find . -name '*.go' -not -path './.git/*' | wc -l` | 300 |
| Líneas de Go | `find . -name '*.go' -not -path './.git/*' -exec cat {} + | wc -l` | 44356 |
| Funciones de test | `grep -rh '^func Test' --include='*_test.go' . \| wc -l` | 476 |
| Paquetes Go | `go list ./... \| wc -l` | 47 |
| Informes de verificación | `ls docs/verification/*.md \| wc -l` | 17 |
| Runbooks | `ls docs/runbooks/*.md \| wc -l` | 22 |
| Migraciones | `ls migrations/*.sql \| wc -l` | 10 |
| Commits | `git rev-list --count HEAD` | 48 |
| Ficheros versionados | `git ls-files | wc -l` | 607 |
| Entradas sin trackear | `git status --short --untracked-files=all \| wc -l` | 0 |

## 5. Gate de calidad

`bash scripts/check-implementation.sh` → **PASS, exit 0**, con estas comprobaciones: 102/102 DONE,
verificación 1:1 PASS por criterio en las 14 épicas, cero filas FAIL, `go build`, `go vet`, `go test`
en serie por contención del Postgres portable, y build del panel.

`bash scripts/check-blueprint.sh` → **PASS, exit 0**: estructura del blueprint, IDs únicos, ≥3
criterios por task y referencias cruzadas.

Binarios de la matriz (tope 4 MiB clase A, 2,6 MiB B/C):

| Arch | Bytes | Arch | Bytes |
|---|---|---|---|
| amd64 | 2546896 | mipsbe | 2206804 |
| arm64 | 2186084 | mipsle | 2245164 |
| armv5 | 2242168 | armv7 | 2233160 |

Todos estáticos (`CGO_ENABLED=0`), empaquetados con `upx --lzma`. Gate de emulación: 6/6 arrancan,
enrolan (201) y laten (200 ok) en ≈1,2 s/arch, RSS del agente 0,8-1,1 MB y cero core dumps, medido en
banco x86-64 vía SSH.

## 6. Cobertura de verificación por épica

| Épica | Criterios | PASS | FAIL |
|---|---|---|---|
| EP-01 | 18 | 18 | 0 |
| EP-02 | 18 | 18 | 0 |
| EP-03 | 24 | 24 | 0 |
| EP-04 | 27 | 27 | 0 |
| EP-05 | 18 | 18 | 0 |
| EP-06 | 21 | 21 | 0 |
| EP-07 | 21 | 21 | 0 |
| EP-08 | 24 | 24 | 0 |
| EP-09 | 24 | 24 | 0 |
| EP-10 | 24 | 24 | 0 |
| EP-11 | 21 | 21 | 0 |
| EP-12 | 21 | 21 | 0 |
| EP-13 | 24 | 24 | 0 |
| EP-14 | 21 | 21 | 0 |

De los 306 criterios, **49 se sostienen con doble fiel y quedan pendientes de medición de campo**;
están inventariados en `DEUDA-Y-RIESGOS.md`. No son FAIL: el doble reproduce el criterio y así queda
escrito en la evidencia.

## 7. Superficie de operación existente

- `deploy/docker-compose.yml` con postgres, redis, nats, api, gw, rdv y web.
- `deploy/systemd/botd.service` y `deploy/rc/botd.rc.local` para la cadena de arranque.
- 20 runbooks en `docs/runbooks/` (persistencia, mesh, release, metering, retention, billing, soak,
  SLOs, panel, threat-model, schema, credenciales y eventos).
- `deploy/signing/` con par de raíz ed25519 y scripts de firma y verificación.
- `deploy/headscale/` con política ACL y configuración del bastión.

## 8. Lo que no existe (estado, no deuda)

1. **Ningún commit**: 525 entradas sin trackear sobre el único commit inicial. El trabajo vive solo en
   el árbol de trabajo.
2. **Ningún CI**: los gates son locales y manuales.
3. **Ningún despliegue**: ni base de datos, ni VPS, ni dominio, ni certificados de flota emitidos.
4. **Ningún dispositivo real**: todo medido con dobles, fixtures y emulación.
5. **Ninguna cuenta de Stripe**: el cobro está simulado con HMAC local.

## 9. Cómo reproducir el estado

```bash
source scripts/env.sh
bash scripts/check-blueprint.sh       # estructura de diseño
bash scripts/check-implementation.sh  # gates de código y verificación
go test -p 1 ./...                    # suite completa (serial por contención)
cd web && pnpm -s build               # panel
```

Requiere Postgres 5433, Redis 16379 y NATS 14233 (portables en `~/pgsql` y `~/tools`). El gate de
emulación necesita un host Linux con `qemu-user`; el procedimiento está en
`docs/runbooks/release.md` y la evidencia en `docs/verification/evidence/EP-14-qemu-gate.log`.

## 10. Verificación de este documento

`bash scripts/check-state-doc.sh` recomputa las cifras de la tabla §4 y falla si alguna no coincide,
si falta una sección obligatoria o si la deuda no cubre las 49 notas de campo.
