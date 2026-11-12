---
artifact: runbook
topic: release
epic: EP-14
created: 2026-11-14
---

# Runbook de release — BOT.NET (EP-14)

## Cadencia: ventanas, no constantes globales

Una release no empuja a toda la flota a la vez: se publisha por **ventanas
por cohorte** — clase de hardware (A→B→C),ISP/país y `config_rev` — con
ventanas de 4–6 h separadas entre cohortes. El agente solo baja binario
cuando el backend le responde `stable_version` distinta en el entitlements
del heartbeat; la cadencia cadencea desde el plano de control, no desde un
cron global que despierte a 100k nodos en el mismo minuto.

## Coste de una release a 100.000 nodos

Una release es **un binario por arch** (no módulos): egress por nodo = bytes
del binario de su arch. Presupuesto (BLUEPRINT §2, perfil A ≤ 4,5 MB/nodo
en la ventana):

| Concepto | Valor |
|---|---|
| `release_egress.sh --nodes=100000` | medido: botd-amd64 ≈ 2,7 MB → **269,8 GB totales** |
| Tope de egress por release | ≤ **500 GB** (worst case 4,5 MB × 100k = 450 GB) |
| Bytes por nodo (perfil A) | ≤ **4,5 MB** — un binario, no 3 módulos de P2 |

Comprobación: `bash scripts/release_egress.sh --nodes=100000` (usa el
binario real de `dist/` si existe; si no, worst case presupuestado).

## Línea en la hoja del plano de datos (BLUEPRINT §2)

| Partida de la release | Coste incremental |
|---|---|
| Capa de túneles | **0 incremental** — la release cabalga en la única TLS/H2 saliente; cero WireGuard, cero conexiones nuevas |
| Egress | 269–450 GB por release a 100k (la partida nombrada; se presupuesta arriba) |
| Control | 1 entrada de manifest por nodo en el entitlements del heartbeat (viaja en el frame que ya existe; `bytes_delta` y `stable_version` no añaden requests) |
| DNS | mover destino de control = 1 registro (resolver TTL+jitter, A2-10); no es un release |

## Pasos de la release

1. `make cross` — 6 ELFs estáticos en `dist/` con `CGO_ENABLED=0` y topes
   por clase (A ≤ 4 MiB; B/C ≤ ~2,6 MiB post-upx). Puerta F1 incluida.
2. `bash scripts/qemu/smoke.sh` (`make qemu-smoke`) — humo por arch contra
   `scripts/qemu/stub_api.go`: `ENROLL 201` + `HEARTBEAT 200 ok` en <60 s,
   endian magic BE/LE en el ELF, RSS clase C ≤ 3,1 MB y 0 core dumps.
   Sin qemu-user en el host corre el doble nativo (contract + endianness).
3. `make sign` → `dist/manifest.json` con `{version, sha256, url, sig}` por
   arch, firmado ed25519 con la raíz de `deploy/signing/keys/` (fijada en
   build vía `-X internal/agent.RootPubKeyBase64`; `botd --print-root`).
4. `make verify-release` — exit 0; alterar 1 byte del `sig` → exit ≠ 0
   (`update=rejected_sig`); la verificación ocurre ANTES de ejecutar.
5. Publish por ventanas: backend fija `nodes.stable_version`; el nodo hace
   self-update (botd.new → rename → exit 75 → watchdog → commit a 5 min;
   crash-loop de 3 salidas 75 en 15 min = rollback server-side).
6. `make reconcile-hourly` — conciliación `bytes_delta`↔`usage` ≤ 0,5 % con
   los datos que ya viajan; alarma `usage.audit` sin bloquear el cobro.

## Dispersión de destino (anti-DPI, A1-13)

Por release, los agentes que actualizan no convergen en un destino único:
multi-SNI (≥2 nombres/ASNs de control) y jitter de puerto con ≥8 buckets
(`internal/tunnel/dispersion.go`; el test captura 50 agentes simulados).

## Limitaciones del host actual

- qemu-user ausente: el gate QEMU 6/6 queda pendiente en build box con
  `qemu-mips{,el}-static`, `qemu-arm`, `qemu-aarch64`, `qemu-x86_64`.
- `make` no está en el PATH del host de desarrollo: los objetivos corren
  como los comandos base indicados arriba.
