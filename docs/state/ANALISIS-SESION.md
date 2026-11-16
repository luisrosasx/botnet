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
`compile.exe: exit status 0xc0000142` (`STATUS_DLL_INIT_FAILED`).

**Causa principal: Windows Defender.** El registro del propio antivirus lo prueba:
9 detecciones de `Trojan:Win32/Bearfoos.A!ml` (heurística ML, falso positivo típico de
binarios Go) sobre `%TEMP%\go-build*\b339\exe\a.out.exe`, el binario que `go.exe` acababa
de enlazar, con acción `Cuarentena` y origen `Protección en tiempo real`. Defender comía
el binario recién escrito y el link fallaba siempre en los mismos paquetes, incluso con
`-p 1`. Eso es lo que la hipótesis de memoria no explicaba: un problema de recursos no es
determinista por paquete.

Arreglo aplicado: `scripts/dev/defender-exclusions.ps1` excluye las rutas de Go (caché,
módulos, temporal, toolchain y proyecto) y los procesos del compilador, y **reactiva la
protección en tiempo real**, que estaba apagada desde las 13:39 del 14/11. Además
`scripts/env.sh` fija `GOTMPDIR=$HOME/go-tmp` para que el enlace no ocurra en `%TEMP%`.
Comprobado con la protección activa: 20 enlaces seguidos de `internal/watchdog` y
`pkg/geoip`, cero rojos y cero detecciones nuevas.

Factor secundario real: memoria y procesos del host (24 CPUs, 443 procesos, 1,8 GB libres).
Se conservan `-p=2`, la suite por tandas, el reaper y el preflight, ahora como protección
contra lentitud y fallos esporádicos, no como cura. Deuda D-18.

De paso salió un fallo real de portabilidad: `TestRebuildUnder1s` medía con el reloj del
cliente y el volcado de 86k filas por TCP sobre el Postgres portable de Windows tarda 19 s.
Ahora mide el tiempo del motor con `EXPLAIN ANALYZE` (JSON) y sigue exigiendo index scan.

Detalle completo en `docs/runbooks/dev-host.md`.

## 3.1. Compilación completa

Un solo comando, `bash scripts/build/all.sh`, encadena todo y guarda la evidencia en
`docs/verification/evidence/build-<fecha>.log`. Resultado del 2026-11-14, salida 0:

| Paso | Resultado |
|---|---|
| preflight | 450 procesos, 33 consolas, 2.051 MB libres |
| redis de test en 16379 | arriba |
| `go build` / `go vet` en serie | ok |
| suite completa por tandas de 6 | ok, cero `--- FAIL` |
| build del panel (Next) | ok |
| matriz cross 6 arquitecturas | ok: amd64 2,55 MB, arm64 2,19, armv5 2,24, armv7 2,23, mipsbe 2,21, mipsle 2,25 (todas estáticas, `CGO_ENABLED=0`) |
| firma y verificación ed25519 | ok |
| humo de arranque (doble nativo, endianness de los 6 ELF) | ok |
| gate de blueprint y de estado | `PASS` |

Por el camino aparecieron dos fallos propios, ya corregidos: el `reap.sh` mataba
`redis-server`, que es motor de test y no residuo (ahora solo con `--engines`), y el
caos de Redis leía `gb_used` una sola vez en vez de esperar a la reconstrucción
asíncrona. El gate además arranca el Redis de test si falta, para no dar un
«connection refused» que parece del código.


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

## 6. Fase de campo — habilitadores (F0)

La maquinaria para medir en hardware real ya existe y está probada. Seis herramientas nuevas,
todas con `--help`, `--dry` y salida JSON, y cada una con su prueba ejecutada:

| Herramienta | Para qué | Cierra |
|---|---|---|
| `scripts/field/bootstrap.sh` | instala y persiste el agente en un nodo (systemd → rc → `@reboot`), siembra mtimes y anuncia el `leave_code` | EP-05 |
| `scripts/field/collect.sh` | empaqueta una pasada de campo en `evidence/field-<fecha>-<label>/` con `INDEX.md`, sha256 y las filas de criterio citadas | toda la fase |
| `scripts/field/revoke_mass.sh` | revoca N nodos en paralelo, espera el cierre de sesiones y detecta reincidencia | S-14 |
| `scripts/field/latency_pings.sh` | p50/p95/p99 de 100 pings, con saturación opcional | EP-06 `T-0604 AC2` |
| `scripts/field/rss_watch.sh` | curva de RSS con umbral (3,1 MB clase C, 6 MB con 150 streams) | EP-03 `T-0301 AC3`, `T-0306 AC1` |
| `scripts/netem/{apply,clear}.sh` | pérdida, retardo y corrupción reales con `tc`, idempotente | EP-02 `T-0202 AC3`, EP-09 |
| `scripts/field/load_250_1000.sh` | P99 y saturación del gateway a 250 y 1.000 nodos | S-16 |

Medido, no supuesto: `revoke_mass` da `PASS` con 5 nodos contra Postgres real (`sessions_closed=10`,
`relapsed=0`) y falla cuando inyecta un latido posterior; `load_250_1000` da p99 0,55 ms y
saturación 4,86 % a 250 y 1.000; `netem` aplicó y retiró reglas reales en el banco Linux.

**El recuento de criterios de campo estaba mal.** El «49» salía de un `grep` de prosa que contaba
líneas del acta. Filas reales: **45** estrictas (solo marca `BLOCKED-HW`) y **47** contando las
frases equivalentes (`queda al soak`, `sin tshark aquí`), incluidas 2 de EP-04 y 4 de EP-13 que
nunca entraron en el recuento. `scripts/check-field-closure.sh` las cuenta por filas, cruza el
reparto con `DEUDA-Y-RIESGOS.md`, y se pone en rojo si una fila marcada pendiente ya está citada
por un fichero de evidencia. El gate de estado delega en él: una sola fuente de verdad.

Runbook de despliegue en `docs/runbooks/field-deploy.md` (VPS, DNS, TLS, headscale, verificación y
rollback). El compose valida con `docker compose config`.

Lo que sigue necesitando manos: los dispositivos del canario y del piloto, y un VPS con
credenciales para desplegar. Sin eso, F1/F2/F3 tienen la herramienta pero no el sujeto.

