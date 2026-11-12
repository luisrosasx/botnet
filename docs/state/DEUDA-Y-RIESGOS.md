---
artifact: state
kind: debt-and-risk
project: BOT.NET
created: 2026-11-12
revision: 1
---

# DEUDA, RIESGOS Y LIMITACIONES — BOT.NET

Registro completo de lo que falta, lo que está flojo y lo que puede doler. Cada entrada lleva
evidencia anclada a fichero y línea. Sin esta evidencia una entrada no entra.

Tipos: `D` deuda técnica, `R` riesgo (de producto o de operación), `L` limitación aceptada.
Severidad: `S1` bloquea producción, `S2` duele en campo, `S3` molesto, `S4` cosmético.

## 1. Deuda técnica

| ID | Sev | Deuda | Evidencia | Impacto | Cierre |
|---|---|---|---|---|---|
| D-01 | ✅ | ~~Repo sin commit~~ | 13 commits temáticos, árbol limpio | cerrado en S-01 | — |
| D-02 | ✅ | ~~Sin CI~~ | `.github/workflows/{gates,tests,matrix}.yml`, probados con rojo provocado | cerrado en S-02 | — |
| D-03 | ✅ | ~~Empuje de pérdida sin llamante~~: `internal/loss`, `LossPusher` y `LossConsumer` con e2e | Definidos en `cmd/botnet-gw/loss_policy.go:43,46`; sin usos fuera del paquete | El gateway calcula pérdida y nunca se la empuja al nodo; el nodo no adapta | Llamarlos desde el bucle de heartbeat del gateway con test de extremo a extremo |
| D-04 | ✅ | ~~`CommitAfter` sin temporizador~~: `CommitTimer` cableado en el latido | `internal/agent/update.go:36-38,317`; commit solo en `cmd/botd/main.go` vía `--commit` | Un nodo que actualiza y no reinicia no fija versión; rollback depende de un camino único | Timer de latidos sanos que dispare el commit |
| D-05 | ✅ | ~~`HeartbeatAlive()` constante~~: ventana de 75 s sobre la marca real | `internal/agent/watermark.go:131` | La histéresis de watermark tiene un assert parcialmente ciego | Derivar de la marca real de último latido |
| D-06 | S2 | El bucle WS vivo del gateway no se mide en runtime; solo el contrato | Nota de verificación EP-08 (T-0803 AC2) | El drenaje puede comportarse distinto con sockets vivos | Test con conexión WS real y corte contabilizado |
| D-07 | S3 | `pg_stat_statements` no precargado: el delta de consultas se mide con wrapper propio | `internal/db.Counted`; nota EP-08 | El conteo no es el del motor | Precargar la extensión en compose y medir con ella |
| D-08 | S3 | `tmp_ep05adv`, `tmp_ep06adv`, `tmp_gwseed`, `tmp_ticket` viven como paquetes sueltos en la raíz | `go list ./...` los lista; 4 entradas de primer nivel | Ruido en el árbol y en `go test ./...` | Moverlos a `testdata/doubles/` o `internal/*/testdata` |
| D-09 | ✅ | ~~dist/raw a mano~~ | `build_matrix.sh` lo genera; sonda en `scripts/qemu/probe` | cerrado en S-04 | — |
| D-10 | ✅ | ~~`*.exe` global~~ | `.gitignore` acota a `/*.exe` y `/dist/` | cerrado en S-01 | — |
| D-11 | S3 | Fragilidad de timing en tests de loopback Windows (`TestExpiredTokenNoStream`, `rdv`) | Nota EP-06 §deudas; fallos intermitentes documentados | Falsos rojos que erosionan la confianza en la suite | Reintento acotado o asserts sin carrera |
| D-12 | S4 | `internal/deps/deps.go` ancla dependencias con imports en blanco | `internal/deps/deps.go` | Patrón poco común: parece código muerto | Comentario de porqué, o `tools.go` con tag `tools` |
| D-14 | S2 | Repositorio privado sin GitHub Pro: la protección de rama no se puede activar | `gh api .../branches/master/protection` → 403 "Upgrade to GitHub Pro" | Los gates corren pero no impiden un merge en rojo | Hacer el repo público o pasar a Pro, y activar checks requeridos |

## 2. Riesgos de producto

| ID | Sev | Riesgo | Evidencia | Cierre o aceptación |
|---|---|---|---|---|
| R-01 | S1 | Detección por destino único: toda la flota comparte destinos, una DNS pasiva o una ACL del ISP la identifica y la apaga | Hallazgo A1-18, mitigado débil; nota EP-13 | ≥ 2 ASNs de control, SNI rotado por nodo y jitter de puerto ya diseñados: falta medirlo en campo con drill de DNS pasiva |
| R-02 | S1 | CA de flota como punto único: su compromiso permite emitir certificados a cualquiera | `deploy/signing/ca_policy.md` | Emisión offline, CRL con revocación < 60 s y rotación a 90 días; falta el ejercicio de revocación masiva |
| R-03 | S2 | Dueño same-uid: puede leer claves en flash y matar procesos | Modelo de amenaza, nota EP-05 | Rotación de secreto y cert a 90 días; el churn se vigila con `reprobe_guard` |
| R-04 | S2 | El dispositivo no es nuestro: puede desaparecer, reiniciarse o saturarse | BRIEF-BASE §restricciones | Watchdog, guard de cron y `--stage`; el tope de 2-5 Mbps evita que el dueño lo note |
| R-05 | S2 | Stripe solo simulado con HMAC local; el cobro real puede divergir | `internal/accounting` (FakeStripe) | Cambiar URL base y secreto, y correr una conciliación con la cuenta real |
| R-06 | S2 | Abuso del cliente: sin tope por inquilino en el nodo, un tenant satura el dispositivo | Tope de 8 streams y 8 tenants aplicados en gateway | Rate-limit por api key y alerta por nodo caliente |
| R-07 | S3 | Re-clutaje de un nodo dado de baja si el `leave_code` se filtra | `handlers/leave.go`, `leaves` con 12 meses | Código de 8 caracteres con rate-limit; caducidad configurable |

## 3. Limitaciones aceptadas

| ID | Sev | Limitación | Motivo |
|---|---|---|---|
| L-01 | S2 | UPX no sobrevive a `qemu-user`: el gate emula la copia desempaquetada | El artefacto entregado sigue empaquetado; la emulación solo prueba el ELF |
| L-02 | S3 | Bajo emulación el RSS medido incluye el emulador; se descuenta una línea base | No hay hardware MIPS/ARM en el host |
| L-03 | S3 | El Postgres portable suelta dial-timeouts: el gate serializa paquetes con `-p 1` | Se mitiga la contención, no el motor |
| L-04 | S3 | Preflight CORS del panel se resuelve con flag de Chromium en el test | Artificio del entorno de desarrollo, no del producto |
| L-05 | S3 | Config de headscale en formato antiguo; v0.29 lo rechaza sin adaptar | El bastión fijará versión; el runbook lo advierte |
| L-06 | S4 | `go.mod` declara `go 1.27` y el banco trae 1.26.8: exige `GOTOOLCHAIN=auto` | Se resuelve fijando toolchain en el banco o bajando el requisito |

## 4. Criterios pendientes de medición de campo (49)

Criterios con veredicto PASS sostenido por doble fiel y anotación `BLOCKED-HW de campo`. Reparto por
épica, recomputado con `grep -c 'BLOCKED-HW de campo' docs/verification/EP-*.md`:

| Épica | Pendientes de campo | Qué exige el criterio |
|---|---|---|
| EP-01 | 6 | `masscan`, `zgrab2`, `iftop`, `make` reales |
| EP-02 | 8 | `legba`, RouterSploit, `tc netem`, QEMU clase A/C |
| EP-03 | 5 | RSS ≤ 3,1 MB en clase B real, iperf3, soak 30 min |
| EP-05 | 5 | QEMU reboot ≤ 90 s, matriz de cadena de arranque, `iostat` |
| EP-06 | 2 | iperf3 5 min, 100 pings SSH al router |
| EP-07 | 11 | `headscale` vivo, SSH al nodo, `tcpdump`, `crontab` del dispositivo |
| EP-08 | 4 | Bucle WS vivo, `pg_stat_statements`, `journalctl` |
| EP-09 | 6 | `netem`, `tcpdump`, reasignación < 5 min medida |
| EP-12 | 2 | QEMU clase B para 1.000 reinicios, job de fuzz 72 h con runner |
| **Total** | **49** | |

## 5. Deuda cerrada durante el proyecto (para no reabrirla)

Re-enrolado con `409` que devuelve `node_id`; unicidad de ticket en el modelo de datos con índice
único parcial; revocación solo en re-enrolado real, no en replay idempotente; `LeaveLimiter` y
`TransparentLimiter` cableados en `main.go`; `StripeSecret` desde entorno con alias de webhook;
`botctl status` real; `botd --print-root` con inyección `-X`; `--stage` de clase C; cadena de
self-update de extremo a extremo; target `contract-test` y compañía en el Makefile; `zgrab2_grab.sh`
sin `--dry` forzado; empaquetado `--lzma` que devuelve el binario a 2,2 MB.

## 6. Prioridad de cierre

`PROXIMOS-PASOS.md` ordena estas entradas. Regla: ningún S1 queda abierto al declarar producción, y
los 49 pendientes de campo se cierran en el piloto, no antes.
