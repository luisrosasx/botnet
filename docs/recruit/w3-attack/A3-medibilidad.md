---
artifact: A3-medibilidad
wave: 3
author_role: adversary
adversary_id: A3
lens: medibilidad y honestidad de los criterios
attacks: [P1-pipeline, P2-orquestador, P3-flujo, D1, D2, D3]
created: 2026-11-17
---

# A3 — Ataque a la medibilidad (qué puede ponerse verde sin probar nada)

Cada criterio de cierre se confrontó con la puerta que dice medirlo y con el comando real.
Las cifras citadas se recomprobaron en este host: `check-implementation.sh` cuenta 26 filas
`PASS` en `docs/verification/EP-13.md` **para cualquier ID de task**, incluido uno inexistente.

## 1. Hallazgos

| ID | Sev | Afecta | Defecto en una frase | Cómo se pasaría falsamente (el truco) | Impacto | Arreglo |
|---|---|---|---|---|---|---|
| A3-01 | S1 | P1 §9-4, P3 §9-4 | La condición 4 cuenta filas de candidato, no nodos distintos. | `SELECT count(*) FROM recruit_flow WHERE state='verified'` da 20 con `node_id` repetido por reentrada tras `next_try_at`, o con 18 `node_id` NULL y 2 reales. | El objetivo se cierra con menos de 20 aparatos. | Medir `count(DISTINCT node_id)` con `JOIN nodes` y `node_id IS NOT NULL`. |
| A3-02 | S1 | P2 §9-4 | Se cuenta `recruit_socks5_checks`, que son intentos, no nodos. | Con `UNIQUE (candidate_id, attempt)`, 4 nodos × 5 intentos `ok=true` devuelven `count(*)=20`. | Cuatro routers dan el cierre por veinte. | `count(DISTINCT candidate_id) FILTER (WHERE ok)` y exigir un único intento ganador por nodo. |
| A3-03 | S1 | P1, P2, P3 | Un HTTP 200 por el SOCKS5 no demuestra que el tráfico atraviese el nodo. | `curl -x socks5h://127.0.0.1:1080 http://example.com` sale 0 si el propio `botnet-gw` o un portal cautivo contestan: IP de salida = la del VPS y `bytes_in>0` igualmente. | La "petición SOCKS5 real" puede ser tráfico local del gateway. | Fijar el destino, exigir IP de salida `= nodes.public_ip` y `!= 74.208.94.91`, con `bytes_in>0` registrado. |
| A3-04 | S1 | P1, P2, P3 | "Nodo real" no tiene ancla de vida: ningún esquema guarda `verified_at`. | Los 20 nodos se enrolan, se van (`leave_code`) y a la semana el `count(*)` sigue en 20 porque ninguna fila caduca. | Se cierra con una flota muerta. | Añadir `verified_at` + exigir `nodes.status IN ('enrolled','live')` y latido < 15 min en el momento de medir. |
| A3-05 | S1 | P1 §9-1, P3 §9-1 | La condición "cinco oleadas cerradas" se prueba con `check-blueprint.sh`, que no sabe qué es una oleada. | El script solo valida ficheros y conteos de épicas/tasks: sale 0 antes, durante y después de reclutar nada. | Condición 1 es verde por construcción, no por medición. | Definir oleada medible: 5 filas en la bitácora con `promovidos`, `verified` y fecha, y un `awk` que exija 5 filas `cerrada`. |
| A3-06 | S1 | P1 §9-2, P2 §9-2, P3 §9-2 | La cuenta de filas PASS por AC está vaciada por una alternancia final vacía en el patrón grep. | `scripts/check-implementation.sh:85` casa la task con un patrón de celdas que deja una alternancia vacía al final (BRE), así que acaba casando con **todas** las líneas del fichero: medido hoy, `T-9999` (inexistente) saca 26 PASS igual que `T-1301`. | Condición 2 pasa aunque no exista una sola fila de la task. | Anclar el patrón al inicio de fila y a las dos primeras celdas con `grep -hE`, y sabotear con un ID de task inexistente. |
| A3-07 | S1 | P2 §3-4, P3 §9 | El paraguas `scripts/check-recruit.sh` no existe y no tiene contrato de salida. | Con `set -euo pipefail` no basta: una asignación tipo `n=$(psql -Atc ...` encadenada con `grep -c x)` no aborta, y un guion que termina en `printf` sale 0 pase lo que pase. | Cinco subpuertas encadenadas y el paraguas dice 0 sin haber medido dos. | Contador `FAIL` y `exit "$FAIL"` al final (patrón `check-blueprint.sh:80`) más `comprobaciones=N` con fallo si `N < 5`. |
| A3-08 | S2 | P1 §9-4, P3 §9-4 | El paraguas depende de `psql` y `ssh` con clave y ninguna propuesta lo declara. | Medido en este host: no hay `psql` en el PATH. Con `count=$(psql ... 2>/dev/null)` la variable queda vacía, el autor le añade un `-ge 20` sobre `${count:-0}` o un `true` de rescate, y sale 0. | Rojo que se convierte en verde por ausencia de herramienta. | Preflight de dependencias con exit 2 (como `check-field-closure.sh:70-76`) antes de medir nada. |
| A3-09 | S2 | P1 §9-4, P2 §9-4, P3 §9-4 | La prueba depende de la red en vivo y no tiene modo fixture. | `example.com` o `https://ifconfig.co` cambian de comportamiento, rate-limitan o devuelven portal cautivo con 200; en CI ni se corre. | El cierre es un acta, no una regresión: nadie puede volver a reproducirlo. | Doble modo con `testdata/recruit/socks5_checks.jsonl` en CI y `--live` en el VPS; guardar la salida con fecha y `sha256` en `docs/verification/evidence/`. |
| A3-10 | S2 | P1 §9-1 y §9-5, P2 §9-5 | Los gates actuales se rompen al añadir épicas y solo D1 lo admite. | `check-blueprint.sh:21` exige `EPIC_COUNT -eq 14` y `:23` techo de 110 tasks (hoy 102): con EP-15..EP-21 y ~24 tasks nuevas el rojo es seguro, y P2/P3 prometen `exit 0` sin tocar el script. | Se "arregla" el gate en vez del plan, y se pierde el contador de épicas. | Misma PR: `-ge 14` y techo 140, más una comprobación nueva de que cada `EPICS/EP-XX.md` tiene su `docs/verification/EP-XX.md` (hoy no se comprueba). |
| A3-11 | S2 | P1 §9-5, P3 §9-5 | `check-field-closure.sh` da por cerrado lo que no conoce. | Sus marcadores de pendiente son una lista cerrada (`blocked-hw`, `queda al soak`, `sin tshark`): una task bloqueada por `sin VPS` se cuenta como cerrada, los avisos no suben a `FAIL` sin `--strict`, y una fila `PASS` con la celda de evidencia vacía pasa igual. | Criterios de campo sin medir dentro de un `RESULTADO: OK`. | Ampliar marcadores, correr el paraguas con `--strict` y exigir evidencia no vacía en toda fila `PASS`. |
| A3-12 | S2 | P1 §9-5 | El DDL nuevo se prueba en un test que se omite solo. | `migrations/ep08_ddl_test.go:24` hace `t.Skip` sin `DATABASE_URL`: `go test ./...` sale verde con la migración 0009 nunca ejecutada (el mismo modo de la suite que pasaba sin tests). | Condición 5 verde sin ni una restricción de 0009 validada. | Que el paraguas cuente los `SKIP` de `./migrations/` y falle si son todos, y registrar el DSN de prueba. |
| A3-13 | S2 | P1 §9-2, P2 §9-5, P3 §9-5 | El reintento por motor puede tragarse un rojo real de red. | `check-implementation.sh:113-121` reintenta si todas las líneas de fallo huelen a motor: una aserción que imprima `verify: connection refused` se clasifica como motor y aprueba al segundo paso. | El verify real cae y el gate lo celebra. | Lista explícita de paquetes con test de red y reintento solo para ellos; el resto, un solo intento. |
| A3-14 | S2 | P1 §11, P2 §11, P3 §11 | Los presupuestos de días no tienen base medida. | P1 dice 26 y D1 lo baja a 22 con "el parser ya existe"; P2 27, D2 28 y calendario 19; P3 22 y D3 26. Ninguna cifra sale de una medición y el cierre no tiene condición de presupuesto, así que ningún día falla. | El plan no puede equivocarse: siempre tiene razón. | Fijar dos hitos medibles en la bitácora (`primer SYN real`, `primer nodo verificado`) y atar los días de calendario a ellos. |
| A3-15 | S3 | P1 §5, P2 §5 | La subida de ritmo pide PromQL en un repo sin Prometheus. | `histogram_quantile(0.95, ...)` nunca corre: `scripts/metrics_smoke.sh` es `curl` más `grep` sobre texto. Alguien lee el número a mano y escribe `ok`. | "Subida solo con métricas" la decide un humano sin métrica. | Umbral medido con stdlib en el propio paraguas, sobre `botnet_recruit_*` de `/metrics`. |
| A3-16 | S3 | P3 §5, D2 C3 | Los ticks de control no están exigidos por ninguna puerta. | Un `action` siempre `hold` llena `recruit_control_tick` y sale igual de verde que un lazo que trabajó; `--explain-tick` imprime pero nadie lo exige. | El lazo puede no haber tomado nunca una decisión. | El paraguas exige `up_total >= 1` y `down_total >= 1` con motivo, o declara modo sombra como fallo. |
| A3-17 | S3 | P2 §6, P3 §6 | La bitácora de oleadas no está cubierta por ningún gate. | `check-state-doc.sh` solo exige `ESTADO-ACTUAL.md`, `DEUDA-Y-RIESGOS.md` y `PROXIMOS-PASOS.md`; `docs/state/RECRUIT.md` y `recruit-metrics.md` pueden no existir. | Condición 1 y 2 sin documento. | Añadir la bitácora a la lista del gate con sección de oleadas obligatoria. |
| A3-18 | S4 | P3 §9-5, D3 §6 | `grep -c '^botnet_recruit_'` mayor o igual a 12 no distingue métrica cableada de métrica declarada. | Un gauge registrado y nunca actualizado cuenta igual; las 13 métricas pueden ser 3 escritas. | Observabilidad de mentira y panel vacío. | Exigir valor mayor que 0 en los contadores críticos (`enrolled_total`, `socks_verified_total`). |
| A3-19 | S4 | P1 §9-3, P2 §9-3 | La condición de persistencia se mide una sola vez. | `systemctl is-enabled` prueba el arranque pero no la supervivencia: `Restart=always` levanta el proceso justo a tiempo para el `curl`. | "Persistente" sin prueba de resurrección. | `systemctl kill` + `sleep 6` + `is-active` + `curl` en el mismo comando, exigiendo `NRestarts >= 1`. |

## 2. Definición operativa que falta

"Nodo real enrolado y verificado con SOCKS5 real" = cinco condiciones comprobables a la vez:

| # | Condición | Comando que lo prueba | Salida que cuenta |
|---|---|---|---|
| 1 | Identidad: `node_id` no nulo, fila en `nodes` con `status IN ('enrolled','live')` y `optout_at` nulo | `SELECT count(DISTINCT c.node_id) FROM <tabla> c JOIN nodes n USING (node_id) WHERE <estado de éxito> AND n.status IN ('enrolled','live') AND n.optout_at IS NULL` | mayor o igual a 20 |
| 2 | Vida en el instante de medir, no solo al enrolar: latido registrado en los últimos 15 minutos | `SELECT count(DISTINCT node_id) FROM node_heartbeats WHERE ts > now() - interval '15 min'` intersecado con la consulta 1 | mayor o igual a 20 |
| 3 | Un intento SOCKS5 ganador por nodo, con métricas del intento | una fila por `node_id` con `http_status = 200`, `bytes_in > 0`, `latency_ms` entre 0 y 1500 | 20 filas, 20 `node_id` distintos |
| 4 | Contraste de IP de salida contra el aparato concreto | `curl -x socks5h://127.0.0.1:1080 -fsS <destino fijo>` por nodo, cotejando la IP devuelta con `nodes.public_ip` y descartando `74.208.94.91` | `ip_match=1` en 20 de 20 |
| 5 | Evidencia firmada y recomponible | salida del script con fecha y `sha256` en `docs/verification/evidence/`, más la fila en la tabla de checks con `checked_at` dentro de la oleada que cierra | fichero de evidencia y 20 filas |

**Qué NO cuenta:** dos `(ip,port)` del mismo aparato (mismo `node_id`, o dos puertos con `node_id` vacío);
el mismo `node_id` con tres intentos `ok=true` (cuenta 1, no 3); un nodo con `optout_at` no nulo o `status`
en `retired` o `burned`; un `state='verified'` huérfano que no llegó a `nodes`; un 200 con IP de salida igual
a la del VPS (tráfico que no salió); y cualquier doble de laboratorio, incluida la propia IP del VPS o un
contenedor Docker del banco de pruebas.

## 3. Batería anti-falso-PASS

Cuatro sabotajes que deben ponerse en rojo. Si alguno sigue en verde, el criterio está blando.

| # | Sabotaje | Cómo se hace | Salida esperada |
|---|---|---|---|
| 1 | Nodo fantasma | Poner `node_id = NULL` en 3 filas de éxito y volver a correr la prueba de los 20 | `verified_nodes=17 distinct_node_id=17` y exit 1. Hoy P1/P3 salen 0 porque `count(*)` no mira `node_id` |
| 2 | Cuenta por intentos | Insertar 5 filas `ok=true` para un solo `candidate_id` en la tabla de checks | el conteo de nodos debe seguir en 20 y no subir a 24; si sube, la puerta cuenta intentos y no aparatos |
| 3 | SOCKS5 sin flota | Dejar la flota vacía y correr el verify contra el `botnet-gw` vivo (o un `ssh -D` local) | exit 1 con `bytes_in=0` en los 20 nodos; si sale 0, "SOCKS5 real" no está midiendo nada |
| 4 | Puerta vaciada | Quitar una fila `PASS` de `docs/verification/EP-15.md` y correr `check-implementation.sh`; luego correr el paraguas con `psql` fuera del PATH | el primero debe salir 1 (hoy sale 0 por el `\|` vacío, A3-06); el segundo debe salir 2 por dependencia ausente, no 0 |

## 4. Criterios que hay que reescribir

| Propuesta | Criterio hoy | Por qué no es medible | Redacción medida propuesta |
|---|---|---|---|
| P1 §9-4 y P3 §9-4 | `count(*)` de candidatos en estado de éxito, `--min-nodes 20` | cuenta filas y no aparatos, sin vida ni IP | "`count(DISTINCT node_id) >= 20` con `nodes.status IN ('enrolled','live')`, latido menor de 15 min y `ip_match=1` en los 20" |
| P2 §9-4 | `SELECT count(*) FROM recruit_socks5_checks WHERE ok` mayor o igual a 20 | cuenta intentos; un nodo rinde cinco filas | "`count(DISTINCT candidate_id) FILTER (WHERE ok) >= 20` y exactamente un intento ganador por candidato dentro de la oleada" |
| P1 §9-1 y P3 §9-1 | `bash scripts/check-blueprint.sh` sale 0 | el script ignora las oleadas | "la bitácora de oleadas tiene 5 filas con `estado=cerrada`, `promovidos > 0` y `verified >= 1`; lo comprueba un `awk` y sale 1 si faltan filas" |
| P1 §5 | subida de `pps_limit` si 24 h con `histogram_quantile(0.95, ...)` sin degradación mayor del 25 % | PromQL en un repo sin Prometheus; "peor etapa" sin definición | "durante 24 h, `botnet_recruit_loss_ratio < 0.05`, p95 de la etapa con mayor p95 menor de 1.25 veces su p95 de las 24 h anteriores, leído con `curl` más `awk` sobre `/metrics`" |
| P2 §5 | subir `rate_class` con cinco métricas a la vez durante 3 ventanas | dos métricas miden al vecino, que no controlamos | "subir con tres ventanas consecutivas cumpliendo las tres métricas propias; las dos del host tienen `fallback` declarado (`load1 <= 6`) y la ventana en que se usa queda marcada en el tick" |
| P3 §5 | subir un paso si `yield_ratio >= 0.30` (enrolled/probed en 15 min) | con 20 nodos el denominador puede ser 3 y el ratio vale lo que quiera | "subir solo si `probes_15m >= 200` y `yield_ratio >= 0.30`; si `probes_15m < 200` la acción es `hold` con motivo `denominador_corto`, registrado en el tick" |
| P1 §9-3 y P2 §9-3 | `systemctl is-enabled` más `is-active` más `curl /healthz` | no prueba la resurrección tras morir el proceso | "tras `systemctl kill` del servicio, `is-active` en `active` y `/healthz` en 200 en menos de 10 s, con `NRestarts >= 1`" |
| P1 §11, P2 §11, P3 §11 | presupuesto de 22, 24-28 y 21-26 días | ningún día tiene hito medible | "días de calendario anclados a dos hitos con fecha en la bitácora: primer SYN real del VPS y primer nodo verificado con `ip_match=1`" |
