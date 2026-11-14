---
artifact: state
kind: session-analysis
project: BOT.NET
created: 2026-11-14
revision: 1
---

# ANÁLISIS DE LA SESIÓN — BOT.NET

Análisis pedido antes de cerrar la sesión. Responde a tres cosas: qué hay de verdad
en el repositorio, cómo se llegó hasta aquí y qué viene después. Todo lo afirmado
lleva el comando que lo produce; lo que no se pudo medir, se dice.

Cifras recomputadas el 2026-11-14:

| Dato | Valor | Comando |
|---|---|---|
| Commits | 31 | `git rev-list --count HEAD` |
| Ficheros versionados | 547 | `git ls-files \| wc -l` |
| Ficheros Go | 280 | `git ls-files '*.go' \| wc -l` |
| Líneas Go | 39.901 | `git ls-files '*.go' \| xargs wc -l` |
| Épicas | 14 | `ls docs/blueprint/TASKS/*.md \| wc -l` |
| Tasks | 102, todas `DONE` | `grep -rh '^- \*\*Estado:\*\* DONE' docs/blueprint/TASKS/ \| wc -l` |
| Criterios de aceptación | 306, todos `PASS` | `grep -rhE '^\| T-[0-9]{4} \| AC' docs/verification/*.md \| wc -l` |
| Criterios en `FAIL` | 0 | mismo grep con `\| FAIL` |
| PRs fusionadas | 5 (#1 a #5) | `gh pr list --state all` |
| CI en `master` | verde: `gates` 48 s, `tests` 10 m 29 s | `gh run list` |

## 1. Estado actual

El proyecto está **implementado de punta a punta, verificado y en un repositorio
privado con CI**, y **no está desplegado en ningún sitio**. Esa es la frase que
resume todo y conviene no suavizarla.

Lo que existe:

- Monorepo Go + Next.js con 47 paquetes: agente multiplataforma (`botd`, MIPS/ARM/
  amd64, 2,2 MB empaquetado), túnel H2 con rendezvous, proxy SOCKS5 en loopback con
  cuotas y admisión, watchdog con reelección, backend Fiber con DDL, CRL y cache de
  claves, gateway backconnect con pools por gateway y deny-list, contabilidad con
  tolerancia única y conciliación, panel de tenant con transparencia y baja, y matriz
  de release firmada con ed25519.
- Backlog cerrado: 102/102 tasks `DONE`, 306/306 criterios con veredicto `PASS`
  probado y citado en `docs/verification/`, cero `FAIL`.
- `master` verde en CI (Linux) con tres workflows: `gates`, `tests` con Postgres,
  Redis y NATS reales, y `matrix` con emulación 6/6. Los gates se probaron con
  sabotaje deliberado: rojo al romper, verde al revertir (PR #1).
- Cinco PRs fusionadas, 30 commits con mensajes temáticos, árbol de trabajo limpio
  salvo los cambios de esta sesión.

Lo que no existe:

- Ningún despliegue: ni VPS, ni base de datos gestionada, ni bastión con la malla.
- Ningún dispositivo real. Las 49 mediciones de campo siguen sin nota de campo.
- Cuenta de Stripe real: el cliente está cableado por entorno y probado contra un
  doble, sin claves `sk_test_`.
- Protección de rama (D-14): imposible en repositorio privado sin GitHub Pro.
- Los 49 criterios de campo no se pueden cerrar sin piloto.

## 2. Cronología de lo construido

1. **Diseño adversarial (5 oleadas, 14 subagentes).** Tres arquitecturas rivales,
   tres defensas, tres adversarios con 54 hallazgos, tres jueces y dos refinadores.
   Produjo `docs/blueprint/` con `BLUEPRINT.md`, `BACKLOG.md`, 14 épicas y 102 tasks
   con 306 criterios ejecutables. Ganó la propuesta frugal por desempate.
2. **Implementación por fases F1-F6.** 102 tasks en dominios de ficheros disjuntos,
   con compilación y suite verdes al cerrar cada fase.
3. **Verificación adversarial 1:1.** Catorce adversarios reejecutaron cada criterio y
   escribieron la prueba en `docs/verification/`. Encontraron 61 criterios en `FAIL`
   con nueve causas raíz reales: limitadores y CRL cableados a `nil`, re-enrolado roto
   tras reset, tickets sin unicidad, `botctl` de mentira, targets de Makefile
   inexistentes, `--dry` de zgrab2, fixture que no casaba con la DDL y cadena de
   self-update sin consumidor.
4. **Cinco rondas de reparación** y re-verificación de cada épica reparada hasta
   dejar 306/306 en `PASS`.
5. **Gate de emulación por SSH** en el banco Linux `PC-Blanca` (root por tailnet):
   6/6 arquitecturas enrolan y laten bajo `qemu-user`, 0 core dumps. Quedó como
   evidencia en `docs/verification/evidence/EP-14-qemu-gate.log`.
6. **Documentación de estado**: `ESTADO-ACTUAL.md`, `DEUDA-Y-RIESGOS.md` (12 deudas,
   7 riesgos) y `PROXIMOS-PASOS.md` (20 pasos con criterio de cierre), con validador
   propio `scripts/check-state-doc.sh` que recomputa cada cifra.
7. **S-01 y S-02**: 13 commits temáticos, `.gitignore` acotado, `.gitattributes` con
   LF, raíz de firma documentada como DEV, repositorio privado y CI en tres workflows
   probados con sabotaje.
8. **Cuatro sprints de código** (PRs #2 a #5): contratos de pérdida con consumidor,
   `CommitTimer` y `HeartbeatAlive` real; estabilidad de loopback y drenaje; Stripe
   por entorno y headscale v0.29 fijado; deuda menor y CORS del panel.

## 3. El bloqueo de hoy: `0xc0000142`

El gate local empezó a fallar de forma repetida en el mismo sitio con
`compile.exe: exit status 0xc0000142` (`STATUS_DLL_INIT_FAILED`). No es código:

- Causa medidas: 24 CPUs hacen que Go abra hasta 24 `compile.exe` a la vez; con la
  sesión cargada (443 procesos, 22 `powershell.exe`, 23 `OpenConsole.exe`) y **1,8 GB
  libres de 16 GB** con Chrome, el editor y WSL delante, Windows deja de inicializar
  procesos.
- Aplicado: `-p=2` en `GOFLAGS`, gate en serie (`-p 1`), reaper de residuos, preflight
  y runbook `docs/runbooks/dev-host.md`.
- Seguía fallando porque la suite completa en un proceso acumula memoria. Se partió en
  tandas de 6 paquetes: cada `go test` termina y libera, y el pico queda acotado.
- El bug de la partición era mío: `for x in $(...)` parte por palabras y dejaba
  `go test` sin paquetes. Corregido con `while read` sobre proceso sustituido.
- Con la tanda ya ejecutándose salió un fallo **real de portabilidad**:
  `TestRebuildUnder1s` medía con el reloj del cliente y el volcado de 86k filas por TCP
  sobre el Postgres portable de Windows tarda 19 s. El criterio habla de la consulta,
  no del transporte: ahora mide el tiempo del motor con `EXPLAIN ANALYZE` (JSON) y
  sigue exigiendo index scan. Verde en 30 s de paquete.

Detalle completo en `docs/runbooks/dev-host.md` y deuda D-17.

## 4. Próximos pasos

Ordenados por lo que desbloquean. El detalle ejecutable está en
`docs/plan/PLAN-S01-S12.md` y `docs/state/PROXIMOS-PASOS.md`.

1. **Commitear y empujar esta sesión** (el árbol tiene 6 ficheros modificados y 3
   nuevos): el runbook, el reaper, el preflight, el arreglo del test y la chunking.
   Dejar `master` verde en CI antes de cualquier otra cosa.
2. **S-12, piloto real con 20-50 dispositivos.** Es lo único que cierra los 49
   criterios de campo. Antes, S-05 a S-11 ya están cerrados; el piloto puede arrancar.
3. **S-13, drill de DNS pasiva** durante el piloto: sin él se crece a ciegas y el
   umbral es ≤ 60 % de destinos correlacionables.
4. **S-14, revocación masiva de la CA de flota**: 100 nodos cerrados en < 60 s.
5. **S-15 ya está**, pero S-16 (iperf3 y latencia a 250 y 1.000 nodos) necesita
   hardware real y va con el piloto.
6. **D-15**: extender el reintento de transporte a las lecturas de sesión y panel; hoy
   un reset del motor sigue devolviendo 500 fuera del gate de enrolado.
7. **D-14**: repositorio público o GitHub Pro para poder exigir los gates en el merge.
8. **D-16**: claves `sk_test_` reales para conciliar contra Stripe de verdad.
9. **P3**: rate-limit por api key (S-17), revisión legal (S-19).

## 5. Riesgo residual de esta sesión

El gate local tarda ~22 minutos y depende de que el host tenga memoria. CI en Linux
tarda 10 minutos y no sufre el problema. Para trabajo diario conviene usar CI como
verdad y el gate local solo antes de empujar, con `reap.sh` y `wsl --shutdown` previos.
