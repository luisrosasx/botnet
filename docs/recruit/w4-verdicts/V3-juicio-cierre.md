---
artifact: V3-juicio-cierre
wave: 4
author_role: judge
judge_id: V3
lens: honestidad de los criterios y clausura demostrable
scores: [P1-pipeline, P2-orquestador, P3-flujo]
ganadora: P2
condenada: P3
created: 2026-11-17
---

# V3 — Juicio desde la honestidad de los criterios

Lente: con qué propuesta se puede **demostrar** el cierre sin trampa, y qué hay que arreglar en los gates
antes de escribir código para que un verde signifique algo. El repo ya tuvo tres verdes que no probaban
nada (`Estimacion` sin tilde, tolerancia de commits, suite que se omite si falta el servicio), así que
ninguna promesa de `exit 0` se acepta de palabra: solo lo que se reproduce en este host.

## 1. Matriz de puntuación

| # | Criterio | Peso | P1 | P2 | P3 | Base de la nota (medida, no opinada) |
|---|---|---|---|---|---|---|
| C1 | Criticidad y medibilidad de los AC | 0,25 | 5 | 7 | 5 | P2 es la única con fila por transición (`recruit_candidate_events`) y por intento (`recruit_socks5_checks`); P1 y P3 atan ACs a `check-blueprint.sh`, que no sabe qué es una oleada, y P1 pide `histogram_quantile` en un repo sin Prometheus. |
| C2 | Anti-falso-PASS | 0,20 | 5 | 6 | 6 | Ninguna de las tres toca `check-implementation.sh:85`. A favor de P2: regla de latido `now() - tick_at > 90 s` (D2 A1). A favor de P3: `ExecStartPre=test -x masscan` falla en vez de degradar en silencio y `RECRUIT_PPS_FIXED` da determinismo. |
| C3 | Evidencia por nodo del SOCKS5 | 0,20 | 4 | 7 | 7 | P1 cierra con un booleano `verify_ok`; P2 tiene tabla de checks por `(candidate_id, attempt)`; P3 imprime `ip_match=1` por nodo. Las tres fallan en que `dist/botd-amd64` no lleva el proxy (ver G3). |
| C4 | Cierre de las 5 condiciones del objetivo | 0,15 | 4 | 5 | 4 | Condición 2 descansa en un gate vaciado y la 1 en un gate ciego para las tres. P2 es la única que define la oleada en cifras (`oleadas=5 cerradas=5 deferred_vivos=0`). |
| C5 | Coste de la verificación | 0,10 | 7 | 4 | 6 | P1 mide con SQL y un script (barato); P2 necesita el orquestador vivo para observar estado (HTTP en el camino caliente); P3 necesita 24 h de ticks para decir «ritmo estable». |
| C6 | Riesgo de ablandar criterios a mitad | 0,10 | 5 | 5 | 3 | P3 acumula válvulas: `RECRUIT_FORCE=1` (se salta la ventana sin rastro), `--allow-degraded`, y `RECRUIT_PPS_MAX=3000` como «ritmo de cierre, no de capacidad» (D3 §5.2) — reescribir el criterio durante el vuelo. P2 tiene `manual_rate_class` (D2 A5); P1 no automatiza, así que no se auto-convence. |
| | **Nota bruta ponderada** | | **4,85** | **6,00** | **5,35** | |

## 2. Techo por S1 sin cerrar

Lista de S1 que bloquean una clausura sin trampa (las demás S1 de las oleadas 1-3 son de operación, no de prueba):

| S1 | Qué exige | P1 | P2 | P3 |
|---|---|---|---|---|
| H1 | SOCKS5 en el binario de release y sesión anclada por nodo (A1-01, A1-03) | abierto: gw «tal cual», `curl` sin sesión | abierto en el tag; medio cerrado en la sesión (`POST /api/v1/sessions` por nodo, D2 §6) | abierto: gw «tal cual», `socks5h` sin sesión |
| H2 | 20 = 20 `node_id` distintos, vivos y con IP de salida contrastada (A3-01..04, A1-04) | abierto: `count(*)`, sin `verified_at` | abierto: cuenta intentos, no aparatos | abierto: cuenta `state='verified'`; el `ip_match` no es computable porque nadie escribe `nodes.public_ip` |
| H3 | La condición 2 se apoya en un gate que casa el ID de task (A3-06) | abierto (D1 §6 canta PASS con el gate roto) | abierto | abierto |
| H4 | La oleada tiene definición medible y fila propia (A3-05) | abierto: delegada a `check-blueprint.sh` | **cerrado en especificación** | abierto: delegada a `check-blueprint.sh` |

| Propuesta | Nota bruta | S1 abiertos | Techo (10 − 2·S1) | Nota final |
|---|---|---|---|---|
| P1 pipeline | 4,85 | 4 | 2,0 | **2,0** |
| P2 orquestador | 6,00 | 3 | 4,0 | **4,0** |
| P3 flujo | 5,35 | 4 | 2,0 | **2,0** |

El techo manda sobre la nota bruta: sin H1 no hay prueba que ejecutar y sin H3 la condición 2 es decorativa.

## 3. Definición operativa de cierre

Cada condición reescrita en forma medible, y al lado lo que **no** cuenta.

| # | Condición medible (obligatoria en la 0009 ganadora) | Qué NO cuenta |
|---|---|---|
| 1 | La bitácora tiene 5 filas de oleada con `estado=cerrada`, `promovidos > 0`, `verified >= 1` y fecha; un `awk` del paraguas sale 1 si falta una fila. | `check-blueprint.sh` saliendo 0: valida ficheros y conteos, está en verde antes, durante y después de reclutar nada. |
| 2 | Toda task DONE con `>= 3` AC y **una fila PASS con la task anclada al inicio de línea** (`grep -hE "^\| T-XXXX \| AC[0-9]+ \|"`), con celda de evidencia no vacía y `sha256` del artefacto medido. | Filas PASS de otra task (hoy `T-9999` saca 26), filas con evidencia vacía, y «PASS» sin comando de medición. |
| 3 | Tras `systemctl kill` de la unidad, `is-active=active` y `/healthz` en 200 en menos de 10 s, con `NRestarts >= 1`, medido sobre las **instancias** y no sobre el target. | `is-enabled` a secas (prueba arranque, no resurrección) y preguntar por una unidad que el diseño no habilita; verde con 0 workers vivos. |
| 4 | `count(DISTINCT node_id) >= 20` con `nodes.status IN ('enrolled','live')`, `optout_at IS NULL`, latido < 15 min en el instante de medir, un único intento SOCKS5 ganador por nodo con `http_status=200`, `bytes_in > 0`, `latency_ms` entre 0 y 1500, e IP de salida igual a `nodes.public_ip` y distinta de 74.208.94.91. | Dos `(ip,port)` del mismo aparato; el mismo `node_id` con 3 intentos `ok=true` (cuenta 1, no 3); un `state='verified'` huérfano sin fila en `nodes`; un 200 cuya IP de salida es la del VPS; dos puertos con `node_id` NULL; el propio contenedor de pruebas. |
| 5 | Gates del repo en 0 **con las épicas nuevas ya admitidas**: `check-blueprint.sh`, `check-implementation.sh`, `check-field-closure.sh --strict`, `check-state-doc.sh`, `go test ./...` y `go test ./migrations/` con **cero SKIP**. | Un verde de `./migrations/` con el único test omitido; `grep -c '^botnet_recruit_' >= 12` como prueba de observabilidad (una métrica registrada y nunca actualizada cuenta igual). |

## 4. Arreglos obligatorios en los gates ANTES de implementar

| # | Gate y línea | Arreglo | Comando que lo prueba | Salida que debe dar |
|---|---|---|---|---|
| G1 | `scripts/check-implementation.sh:85` | Anclar el patrón al inicio de fila y a las dos primeras celdas: `grep -hE "^\| ${t} \| AC[0-9]+ \| .*PASS"` (hoy BRE con alternancia final vacía) | `grep -hE '^\| T-9999 \| AC[0-9]+ \|' docs/verification/EP-13.md \| grep -c PASS` y lo mismo con `T-1301` | `0` y `3`. **Medido hoy: 26 y 26** — con 26 el objetivo entero está sin verificar. |
| G2 | `scripts/check-blueprint.sh:21,23` | `EPIC_COUNT -ge 14` y techo de tasks a 140, en la misma PR que la épica nueva; añadir paridad `EPICS/`↔`docs/verification/` | `printf -- '---\nid: EP-15\n---\n' > docs/blueprint/EPICS/EP-15-probe.md && bash scripts/check-blueprint.sh; echo exit=$?` | Hoy: `FAIL epics=15 (esperado 14)` y **exit=2** (medido). Tras el arreglo: `ok epics=15` y exit 0. Y `comm -23 <(ls docs/blueprint/EPICS \| cut -d- -f1,2 \| sort -u) <(ls docs/verification \| sed 's/\.md//' \| sort -u)` vacío (hoy vacío: la comprobación nace verde y pilla la épica sin evidencia). |
| G3 | `scripts/cross/build_matrix.sh:80` | Añadir `-tags socks5` a la matriz y tratar el front SOCKS5 del gw como **trabajo nuevo**, no como «tal cual» | `grep -ac 'bot-session' dist/botd-amd64` | Hoy **0** (medido; `internal/proxy/socks5_stub.go` es `//go:build !socks5` y `Start` devuelve `ErrDisabled`). Debe dar `>= 1`. Sin esto la condición 4 no es ejecutable y `verify_ok` se rellena a mano. |
| G4 | `migrations/ep08_ddl_test.go:24` | El paraguas exige cero omitidos en `./migrations/` y `DATABASE_URL` presente (si falta, salida 2, no 0) | `go test ./migrations/ -v 2>&1 \| grep -c -- '--- SKIP'` | Hoy **1** con `ok` y exit 0 (medido en go1.27.4): la 0009 entraría sin aplicar nunca. Debe dar `0`. |
| G5 | Preflight de dependencias del paraguas (patrón `check-field-closure.sh:70-76`) | `psql`, `ssh`, `curl` comprobados antes de medir; falta ⇒ **exit 2** | `command -v psql \|\| echo NO` | Hoy **NO** (medido). Con un `n=$(psql ... 2>/dev/null)` el conteo sale vacío y el `-ge 20` se rescata con `:-0` o `true`: rojo convertido en verde por falta de herramienta. |
| G6 | `scripts/check-field-closure.sh` | Correr el paraguas con `--strict` y exigir celda de evidencia no vacía en toda fila PASS | `bash scripts/check-field-closure.sh --strict; echo exit=$?` | Hoy exit 0 en ambos modos (medido), así que el marcador estricto se fija **antes** del trabajo y no se rebaja luego. |

## 5. Ganadora y condenada

- **Ganadora: P2 (orquestador), final 4,0.** Es la única cuya arquitectura deja la fila que se firma en el
  cierre: `recruit_socks5_checks` por intento con `target`, `http_status`, `latency_ms` y `UNIQUE (candidate_id, attempt)`,
  más `recruit_candidate_events` con `actor` (auditoría de quién movió el estado) y la única definición de oleada
  en cifras. Paga caro el camino caliente (HTTP + fila de ventana) y es la más cara de verificar, pero el objetivo
  se cierra con evidencia por nodo, no con elegancia.
- **Condenada: P3 (flujo), final 2,0.** *Condena: P3 convierte el criterio en una decisión del lazo —el ritmo, la
  marcha y hasta el techo se renegocian sobre la marcha con `RECRUIT_FORCE`, `--allow-degraded` y `PPS_MAX=3000`—,
  de modo que puede dar verde el día que el lazo decidió no subir y contar 20 filas `verified` en un inventario
  donde nadie rellena la IP de salida.*

## 6. Injertos que P2 debe adoptar de las perdedoras

| # | Pieza | De | Dónde aterriza en P2 | Por qué es honestidad, no ornamento |
|---|---|---|---|---|
| I1 | Reconocer y parchear el propio gate en la misma PR (`-ge 14`, techo 140, fuera `btree_gist`) | P1/D1 De5 | Única propuesta que **leyó** sus puertas; P2 y P3 prometen `exit 0` sin tocar `check-blueprint.sh` | Si el plan no puede añadir una épica sin romper el gate, el gate es parte del plan. |
| I2 | Presupuesto con unidad tipada: `unit text CHECK (unit IN ('pps','conn','attempt'))` | P1/D1 De3 | `recruit_windows`: deja de existir `conn_budget` decorativo | Hoy en P2 `conn_used` no lo incrementa nadie, así que el `CHECK (pps_used <= pps_budget)` jura por una columna que no se mueve. |
| I3 | `KillMode=control-group` + `TasksMax=128` y `ExecStartPre=test -x /usr/bin/masscan` (fallar al arrancar) | P3 §7 / D3 | `botnet-recruit.service` y las workers | Un masscan huérfano emitiendo pps extra es un falso verde de ruido; degradar en silencio es la madre del falso PASS. |
| I4 | `RECRUIT_PPS_FIXED=<n>` (lazo congelado) y modo fixture NDJSON en CI | D3 §2.2 y P1 §6 (`SWEEP_JSON`) | `botnet-probe --sweep-json testdata/recruit/masscan.ndjson` | Sin modo determinista el cierre es un acta que nadie puede volver a reproducir (A3-09). |

## 7. Cómo debe ser `scripts/check-recruit.sh`

Contrato: contador `FAIL` propio y `exit "$FAIL"` al final (patrón `check-blueprint.sh:80`), `set -euo pipefail`,
y `comprobaciones=N` con fallo si `N < 5`. `set -euo pipefail` solo **no** basta: una asignación con suborden que
falla no aborta, y un guion que termina en `printf` sale 0 pase lo que pase.

Comprobaciones mínimas (todas con salida propia, ninguna delegada a otro gate):

1. `preflight`: `psql`, `ssh`, `curl`, `masscan` — salida 2 si falta una.
2. `oleadas`: 5 filas `cerrada` en la bitácora con `promovidos > 0` y `verified >= 1`.
3. `nodos`: `count(DISTINCT node_id) >= 20` con `status IN ('enrolled','live')`, `optout_at IS NULL` y latido < 15 min.
4. `socks5`: un único intento ganador por nodo con `http_status=200`, `bytes_in > 0`, `latency_ms` acotado e `ip_match=1` (IP de salida = `nodes.public_ip` y `!= 74.208.94.91`).
5. `servicio`: `systemctl kill` + `sleep 6` + `is-active` + `/healthz`, exigiendo `NRestarts >= 1`, medido sobre las instancias.
6. `gates`: los cinco del repo con `--strict`, y `go test ./migrations/` con cero SKIP.
7. `lazo`: `up_total >= 1` y `down_total >= 1` con motivo en la tabla de ticks; si todo es `hold`, el lazo no decidió nada y eso es fallo, no verde.
8. `evidencia`: fichero fechado con `sha256` en `docs/verification/evidence/`; el script imprime el conteo, no solo `OK`.

Sabotajes que debe detectar (si siguen en verde, el criterio está blando):

| Sabotaje | Cómo | Salida esperada |
|---|---|---|
| Nodo fantasma | `node_id = NULL` en 3 filas de éxito | `distinct_node_id=17` y exit 1 (hoy sale 0 porque `count(*)` no mira `node_id`) |
| Cuenta por intentos | 5 filas `ok=true` del mismo `candidate_id` | el conteo **no** sube; si sube, la puerta cuenta intentos y no aparatos |
| SOCKS5 sin flota | flota vacía y verify contra un `ssh -D 1080` local | exit 1 con `bytes_in=0`; un 200 del portal cautivo no vale |
| IP de salida local | forzar que el destino devuelva 74.208.94.91 | exit 1 (`ip_match=0`): el tráfico no salió del VPS |
| Puerta vaciada | quitar una fila PASS y correr el paraguas; luego quitar `psql` del PATH | el primero exit 1 (hoy 0 por el `\|` de G1); el segundo **exit 2**, no 0 |
| Servicio zombi | `systemctl kill` y medir sin `NRestarts` | exit 1 si nadie resucitó el proceso |
| Ticks en pausa | tabla de ticks con `action` siempre `hold` | exit 1 (comprobación 7), no verde |
