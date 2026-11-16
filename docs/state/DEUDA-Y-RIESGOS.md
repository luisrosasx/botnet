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
| D-16 | S2 | El cliente Stripe solo tiene doble HTTP en test; sin cuenta de test no se ha ejercitado contra el proveedor | `internal/accounting/stripe_client.go` | La conciliación real puede divergir | Ejecutar `StripeGBFromEnv()` con claves `sk_test_` y pegar una conciliación |
| D-06 | S2 | El bucle WS vivo del gateway no se mide en runtime; solo el contrato | Nota de verificación EP-08 (T-0803 AC2) | El drenaje puede comportarse distinto con sockets vivos | Test con conexión WS real y corte contabilizado |
| D-07 | S3 | `pg_stat_statements` no precargado: el delta de consultas se mide con wrapper propio | `internal/db.Counted`; nota EP-08 | El conteo no es el del motor | Precargar la extensión en compose y medir con ella |
| D-08 | ✅ | ~~`tmp_*` en la raíz~~: movidos a `testdata/doubles/` | fuera de `go list ./...` | cerrado en S-03 | — |
| D-09 | ✅ | ~~dist/raw a mano~~ | `build_matrix.sh` lo genera; sonda en `scripts/qemu/probe` | cerrado en S-04 | — |
| D-10 | ✅ | ~~`*.exe` global~~ | `.gitignore` acota a `/*.exe` y `/dist/` | cerrado en S-01 | — |
| D-17 | ✅ | ~~Windows Defender pone en cuarentena el binario recién enlazado por Go (`a.out.exe`) y el link muere con `0xc0000142`~~ | `docs/runbooks/dev-host.md`; 9 detecciones de `Trojan:Win32/Bearfoos.A!ml` el 2026-11-11 sobre `%TEMP%\go-build*\exe\a.out.exe` | Rojos deterministas en los mismos paquetes, con la protección en tiempo real activa | Exclusiones + `GOTMPDIR` fuera de `%TEMP%` + protección reactivada (S: `scripts/dev/defender-exclusions.ps1`); 20 enlaces seguidos sin rojos |
| D-18 | S3 | Presión de memoria y de procesos del host Windows: 24 CPUs y sesión cargada hacen que `compile.exe` no inicialice | `docs/runbooks/dev-host.md`; medido 1,8 GB libres con 443 procesos | Lentitud y fallos esporádicos bajo carga (ya no deterministas) | Tope `-p=2`, gate en serie por tandas, reaper y preflight (aplicado) |
| D-15 | S3 | El reintento de transporte solo cubre la lectura del gate de enroll (`queryRowRetry`); el resto de lecturas siguen devolviendo 500 ante un reset | `cmd/botnet-api/repo/retry.go` | Un reset bajo carga sigue dando 500 en otras rutas | Extender `queryRowRetry` a las lecturas de sesión y panel |
| D-11 | ✅ | ~~Fragilidad de timing en tests de loopback~~: guarda del buffer y reintento de dial en `rdv` | 10 ejecuciones seguidas verdes | cerrado en S-09 | — |
| D-12 | ✅ | ~~`deps.go` sin explicación~~: comentario del porqué | — | cerrado en S-20 | — |
| D-14 | ✅ | ~~Repositorio privado sin GitHub Pro: protección de rama inactivable~~ | repo público y `gh api .../branches/master/protection` con checks requeridos `estructura`, `panel`, `suite` | cerrado: un merge en rojo ya no puede entrar | — |

## 2. Riesgos de producto

| ID | Sev | Riesgo | Evidencia | Cierre o aceptación |
|---|---|---|---|---|
| R-01 | S1 | Detección por destino único: toda la flota comparte destinos, una DNS pasiva o una ACL del ISP la identifica y la apaga | Hallazgo A1-18, mitigado débil; nota EP-13 | ≥ 2 ASNs de control, SNI rotado por nodo y jitter de puerto ya diseñados: falta medirlo en campo con drill de DNS pasiva |
| R-02 | S1 | CA de flota como punto único: su compromiso permite emitir certificados a cualquiera | `deploy/signing/ca_policy.md` | Emisión offline, CRL con revocación < 60 s y rotación a 90 días; falta el ejercicio de revocación masiva |
| R-03 | S2 | Dueño same-uid: puede leer claves en flash y matar procesos | Modelo de amenaza, nota EP-05 | Rotación de secreto y cert a 90 días; el churn se vigila con `reprobe_guard` |
| R-04 | S2 | El dispositivo no es nuestro: puede desaparecer, reiniciarse o saturarse | BRIEF-BASE §restricciones | Watchdog, guard de cron y `--stage`; el tope de 2-5 Mbps evita que el dueño lo note |
| R-05 | ✅ | ~~Stripe solo simulado~~: cliente real con `STRIPE_API_BASE`/`STRIPE_SECRET` | probado con doble HTTP; falta la cuenta | cerrar D-16 con claves de test |
| R-06 | S2 | Abuso del cliente: sin tope por inquilino en el nodo, un tenant satura el dispositivo | Tope de 8 streams y 8 tenants aplicados en gateway | Rate-limit por api key y alerta por nodo caliente |
| R-07 | ✅ | ~~`leave_code` sin caducidad~~: TTL configurable (`BOTNET_LEAVE_CODE_TTL`, 90 d por defecto) | cerrado en S-18 | — |

## 3. Limitaciones aceptadas

| ID | Sev | Limitación | Motivo |
|---|---|---|---|
| L-01 | S2 | UPX no sobrevive a `qemu-user`: el gate emula la copia desempaquetada | El artefacto entregado sigue empaquetado; la emulación solo prueba el ELF |
| L-02 | S3 | Bajo emulación el RSS medido incluye el emulador; se descuenta una línea base | No hay hardware MIPS/ARM en el host |
| L-03 | S3 | El Postgres portable suelta dial-timeouts: el gate serializa paquetes con `-p 1` | Se mitiga la contención, no el motor |
| L-04 | S3 | Preflight CORS del panel se resuelve con flag de Chromium en el test | Artificio del entorno de desarrollo, no del producto |
| L-05 | ✅ | ~~Config de headscale antiguo~~: migrado a v0.29 y versión fijada en VERSION | `headscale serve` arranca |
| L-06 | S4 | `go.mod` declara `go 1.27` y el banco trae 1.26.8: exige `GOTOOLCHAIN=auto` | Se resuelve fijando toolchain en el banco o bajando el requisito |

## 4. Criterios pendientes de medición de campo (45 estrictos / 47 extendidos)

Criterios cuyo veredicto `PASS` se sostiene con doble fiel y llevan anotación de campo en la propia
fila de `docs/verification/EP-*.md`. El reparto lo computa **`scripts/check-field-closure.sh`**, que
cuenta filas de tabla y no prosa: `bash scripts/check-field-closure.sh`.

Dos columnas porque hay dos definiciones honestas: `estricto` cuenta solo la marca `BLOCKED-HW`;
`extendido` añade las frases equivalentes que usaron algunos adversarios (`queda al soak`,
`sin tshark aquí`). El número anterior, 49, salía de un `grep` de prosa que contaba líneas de texto
del acta, no criterios.

| Épica | Estricto | Extendido | Qué exige el criterio |
|---|---|---|---|
| EP-01 | 5 | 5 | `masscan`, `zgrab2`, `iftop`, `make` reales |
| EP-02 | 7 | 7 | `legba`, RouterSploit, `tc netem`, QEMU clase A/C |
| EP-03 | 3 | 3 | RSS ≤ 3,1 MB en clase B real, iperf3, soak 30 min |
| EP-04 | 0 | 2 | Captura `tshark` de 1 h sobre el heartbeat |
| EP-05 | 4 | 4 | QEMU reboot ≤ 90 s, matriz de cadena de arranque, `iostat` |
| EP-06 | 3 | 3 | iperf3 5 min, 100 pings SSH al router, RSS con 150 streams |
| EP-07 | 9 | 9 | `headscale` vivo, SSH al nodo, `tcpdump`, `crontab` del dispositivo |
| EP-08 | 2 | 2 | Bucle WS vivo, `pg_stat_statements`, `journalctl` |
| EP-09 | 4 | 4 | `netem`, `tcpdump`, reasignación < 5 min medida |
| EP-12 | 4 | 4 | QEMU clase B para 1.000 reinicios, job de fuzz 72 h con runner |
| EP-13 | 4 | 4 | `netem`, `dig`/`s_client`, `tcpdump`, score de Redis, `psql` |
| **Total** | **45** | **47** | |

EP-10, EP-11 y EP-14 no tienen filas pendientes de campo.


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
