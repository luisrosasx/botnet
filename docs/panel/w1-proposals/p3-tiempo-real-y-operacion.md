---
artifact: P3-tiempo-real-y-operacion
wave: 1
author_role: proposer
proposal_id: P3
created: 2026-11-18
---

# P3 — El panel como consola de operación: se actualiza sola y no se cae

## 1. Enfoque en 3 frases

El panel es un proceso Go nuevo (`cmd/botnet-panel`) que sirve una foto HTTP paginada y, en paralelo, un
stream SSE con **solo deltas de estado**, alimentado por un único rastreador de eventos dentro del proceso:
el resto de espectadores no cuesta una consulta más. La verdad sigue siendo Postgres (`nodes`,
`recruit_candidates`, `recruit_candidate_events`) y el motor en `9110`, y el panel solo formatea y contrasta:
un `count(*)` periódico delata cualquier desfase entre lo que la pantalla cree y lo que la base dice, y dispara
un `resync` en vez de mentir. La UI es una tabla virtualizada de altura fija que parchea celdas por `node_id`
—nunca reordena— y que distingue tres cosas que hoy se confunden: **cero filas de verdad**, **base caída** y
**motor atascado**.

## 2. Transporte de datos: SSE desde `cmd/botnet-panel`

### 2.1 Endpoint y contrato del stream

`GET /api/stream` en `127.0.0.1:9120` (el puerto que ya fija `scripts/check-panel.sh:43`).

```http
HTTP/1.1 200 OK
Content-Type: text/event-stream; charset=utf-8
Cache-Control: no-cache, no-transform
Connection: keep-alive
X-Accel-Buffering: no
```

| frame | `event:` | `id:` | Cuándo | Carga |
|---|---|---|---|---|
| saludo | `hello` | — | al abrir | `{proto:1, now, hb_s:15, cand_cursor, node_cursor, poll_ms:1000}` |
| candidato | `cand` | `<event_id>` | transición nueva en `recruit_candidate_events` | `{candidate_id, ip, port, from, to, reason, at}` (batch de hasta 200) |
| nodo | `node` | `n<seq>` | cambio de `status`/`last_seen_at` de un nodo | `{node_id, status, last_seen_s}` (coalescido 1 Hz) |
| contraste | `count` | — | cada 10 s si el recuento cambió | `{total, by_status, drift}` |
| atasco | `stuck` | — | cada 10 s si cambia el veredicto | `{stuck, cause, tick_age_s, rate_class, pps_effective, backlog}` |
| ráfaga | `burst` | — | > 5000 deltas `node` en un flush | `{from_seq, n}` → el cliente recarga su página |
| orden de recarga | `resync` | — | desfase, cursor purgado, `burst` | `{reason}` |
| latido | comentario `: hb <ts>` | — | cada 15 s | — |

Reglas duras:

- **`id` monotónico y con dos espacios de nombres.** Los `cand` usan `recruit_candidate_events.event_id`
  (bigserial, sin huecos, ya existe en `migrations/0009_recruit.up.sql:70`) y son **reproducibles**; los
  `node` usan un contador del proceso `n<seq>` y **no** se reproducen, porque el estado del nodo es absoluto
  (`nodes.status`), no un incremento. El `hello` declara qué cursor es reproducible: el cliente no tiene que
  adivinarlo.
- **`Last-Event-ID`**: si llega en la petición y es un `cand` con `event_id >= min(event_id)` de la tabla, el
  panel **reproduce desde ahí** (consulta keyset `WHERE event_id > $1 ORDER BY event_id LIMIT 5000`) y sigue en
  vivo. Si el cursor es anterior al mínimo (retención de `recruit_candidate_events`), emite `resync` con motivo
  `cursor_purgado` en vez de fingir continuidad. Si es un `n<seq>` o no llega, arranca en "ahora" y el cliente
  refresca la página por HTTP: la lista nunca se reconstruye por el stream.
- **Reconexión**: `retry: 3000` + jitter **en el cliente** (1 s → 30 s exponencial, tope 5 intentos antes de
  pasar a indicador *caído*). El servidor no mantiene estado de sesión: reconectar es abrir otro `GET`.
- **Un solo rastreador por proceso.** No hay bus de eventos hoy (`grep -rn "NOTIFY\|LISTEN" internal/` = 0
  resultados), así que el panel lo crea: una goroutine que cada 1000 ms hace (a) `event_id > cursor` sobre
  `recruit_candidate_events` y (b) `SELECT node_id,status,last_seen_at FROM nodes WHERE last_seen_at > $w`
  por el índice `nodes_status_seen` (`migrations/0004_indexes.up.sql:11`), más un barrido cíclico completo en
  páginas de 5000 cada 30 s para capturar los cambios de `status` que no tocan `last_seen_at`
  (`revoke` → `retired`/`burned`). Los suscriptores SSE solo leen de un canal por cliente.

### 2.2 Por qué SSE y no WebSocket ni sondeo

| Criterio | SSE | WebSocket | Sondeo |
|---|---|---|---|
| Dependencias Go | `net/http` puro, como `internal/recruit/httpapi.go` | `gorilla/websocket` nueva (hoy no está en `go.mod`) | ninguna |
| Dirección | un sentido (el panel es solo lectura) | bidireccional, no se usa | request/response |
| Reconexión | del protocolo: `retry` + `Last-Event-ID` | la escribes tú (ping/pong, backoff, cola) | la escribes tú con temporizador |
| Coste con 10 pestañas | 1 rastreador + 10 sockets | 1 rastreador + 10 sockets + handshake y framing propios | 10 × `count(*)`/5 s = 7200 consultas/hora |
| Atraviesa túneles/proxies | sí (HTTP/1.1, `no-transform`) | necesita upgrade explícito | sí |
| Complejidad de auth | cabecera `Authorization` en un `GET` normal | token en query o primer frame firmado | cabecera en cada vuelta |

Sondeo descartado por aritmética: con 60 s de refresco el operador ve el embudo con un minuto de retraso justo
cuando importa (ola de reclutamiento en marcha); con 5 s se multiplican por 12 las consultas sin ganar nada
sobre SSE, y cada pestaña paga su propia consulta. WebSocket es la respuesta a un problema que este panel no
tiene: no envía nada al servidor.

**Aviso técnico, no cosmético:** `EventSource` **no permite cabeceras**. Para cumplir "token en cabecera" el
cliente no usa `EventSource`, usa `fetch()` + `ReadableStream` + un parser SSE de ~40 líneas (frame `id:`/`event:`/
`data:` separados por línea en blanco, comentarios `:` ignorados). Ventaja extra: el error HTTP deja de ser
opaco (`EventSource` da `onerror` sin código) y se puede distinguir 401 de 503.

## 3. Modelo de consulta

### 3.1 Listado paginado (la foto)

`GET /api/nodes?status=&limit=&after=` → **array JSON plano** (el contrato que ya grepea la puerta:
`check-panel.sh:130` y `:158` cuentan `"node_id"` y `"status"` sueltos, así que nada de envoltorio
`{items:[…]}` en esta ruta).

```sql
SELECT node_id::text, slug, host(public_ip) AS public_ip, status, device_class, arch,
       COALESCE(country,'') AS country, COALESCE(model,'') AS model,
       enrolled_at, last_seen_at
  FROM nodes
 WHERE deleted_at IS NULL
   AND ($1 = '' OR status = $1)
   AND ($2::uuid IS NULL OR node_id > $2::uuid)
 ORDER BY node_id
 LIMIT $3
```

- **Keyset, no `OFFSET`**: `after=<último node_id>` con `ORDER BY node_id`. Estable ante inserciones y sin el
  coste O(n) del offset con 200k filas.
- `limit` por defecto 500 (lo que pide el navegador), techo **200000** porque la puerta llama
  `?limit=200000` y exige igualdad exacta con `SELECT node_id::text FROM nodes`; si el techo se quedara en
  1000, el criterio 2 de la puerta sería imposible de pasar sin trampa. El navegador nunca pide más de 500.
- La respuesta **no** incluye `device_token_hash` ni `mac_hash`: no hacen falta y son secretos de flota.
- `fresh` **no** se sirve como `status`: `nodes.status` viaja **verbatim** (la puerta compara contra
  `SELECT DISTINCT status FROM nodes` y un estado inventado la pone roja). La frescura va en un campo aparte,
  `last_seen_s` (segundos desde `last_seen_at`, `null` si nunca), y la UI pinta "sin latido hace 12 min".

### 3.2 Ventana de estados y contraste

`GET /api/summary`:

```json
{"total":20143,
 "by_status":{"live":19800,"enrolled":200,"down":143},
 "growth":{"windows":[{"hour":"2026-11-18T09:00:00Z","candidates":1240,"nodes":310,"partial":false}, …],
           "ratio":1.62,"stuck":null},
 "engine":{"reachable":true,"tick_age_s":12,"rate_class":2,"paused":false,
           "pps_target":2500,"pps_effective":2380,"queue_depth":37,"enroll_backlog":112},
 "counted_at":"2026-11-18T10:14:02Z"}
```

Dos consultas de contraste, con techo de coste explícito:

1. `SELECT count(*) FROM nodes WHERE deleted_at IS NULL` + `GROUP BY status` → verdad del listado.
2. Serie de crecimiento, **idéntica** a la de la puerta (`check-panel.sh:174`):
   `SELECT count(*) FROM recruit_candidates GROUP BY date_trunc('hour',created_at) ORDER BY 1 DESC LIMIT 4`.
   La hora en curso se marca `partial:true` y **no entra** en el `ratio` (una ventana abierta siempre parece
   una caída).

`drift = |total_summary − Σ by_status|` (debe ser 0) y `total` frente al último `total` enviado por el stream:
si cambian y no se han emitido deltas suficientes, `resync`. El contraste corre cada **10 s** y se degrada a
**60 s** si la consulta tarda más de 200 ms (medido por el propio panel, expuesto como
`botnet_panel_summary_ms`).

### 3.3 Qué viaja por el stream (y qué no)

Por el stream viaja **el delta más pequeño que permite parchear una celda**: `node_id` + campos cambiados.
Nunca filas completas, nunca el listado, nunca la tabla. Presupuesto de bytes: 120 B por delta `node`, y con
coalescido a 1 Hz una oleada de 2000 altas/minuto son ~4 KB/s por espectador, no 20k filas cada 5 s.

Deltas idempotentes: `status` es el valor **absoluto**, no `+1`. Perder un frame solo cuesta una celda
desactualizada hasta el siguiente `count`/`resync`; nunca corrompe un contador.

`POST`/`PATCH`/`DELETE` no existen en este panel: solo `GET`. El `OPTIONS` de CORS se responde pero no se
documenta como ruta propia.

## 4. UI

### 4.1 Tabla virtualizada

`web/app/panel/devices/page.tsx`, componente cliente. **Sin dependencias nuevas**: `web/package.json` no tiene
`react-window` ni `@tanstack/react-virtual` y no se añade ninguna. Virtualización a mano:

- Altura de fila fija 28 px, contenedor con `overflow:auto`, se renderiza `ceil(alto/28)+40` filas (overscan
  de 20 arriba y 20 abajo) posicionadas de forma absoluta por `top = índice*28`.
- Filas en un `Map<node_id, Row>`; el `key` de React es `node_id`, no el índice: un delta no remonta la tabla.
- Carga por keyset ("Cargar más" y scroll al 80 %), tope de 10 páginas en memoria (5000 filas); más allá, el
  operador usa el filtro por estado.
- Columna de frescura con semáforo calculado en cliente desde `last_seen_s` y `now` (no requiere tráfico): verde
  < 90 s (el `StaleAfter` del motor, `internal/recruit/store.go:79`), ámbar < 10 min, gris sin latido.
- Orden: `node_id` salvo que el operador pulse una cabecera; con orden manual los deltas **actualizan celdas y
  no reordenan**. El reordenamiento es explícito (botón), nunca implícito.

### 4.2 Indicador de conexión (tres estados, no dos)

| Estado | Cuándo | Qué se ve |
|---|---|---|
| **vivo** | stream abierto y último frame (incluido `: hb`) hace < 3×`hb_s` (45 s) | punto verde + "en vivo · actualizado hace 2 s" |
| **reconectando** | `fetch` cerrado o watchdog vencido, reintento programado | punto ámbar + "reconectando (intento 3, en 8 s)"; la tabla sigue visible y se marca como foto antigua con hora |
| **caído** | 401/403, o 5 reintentos agotados, o `/api/summary` 503 | banda roja con la causa y "última foto: 10:12:31"; el botón "Reintentar ahora" no recarga la página |

El watchdog del cliente es obligatorio: una conexión TCP medio muerta (NAT, túnel SSH cortado) no dispara
`onerror` en minutos. Si no llega frame en 45 s, se cierra el `AbortController` y se reconecta.

### 4.3 Cero filas vs base caída vs motor parado

Tres pantallas distintas, porque son tres problemas distintos:

- **0 filas de verdad** (`/api/summary` responde 200 con `total=0`): "Todavía no hay dispositivos. El motor de
  descubrimiento está en clase 2 y ha sondeado 1240 candidatos en la última hora" — el vacío explica por qué.
- **Base caída** (`/api/nodes` devuelve **503** con `{"error":"db_down"}`, nunca `[]`): se conserva la última
  tabla, atenuada, con "base de datos sin responder · los datos son de las 10:12". La puerta lo deja escrito:
  *"Base caida: 503, nunca lista vacia"* (`check-panel.sh:145`). Un `[]` con la base caída es una mentira que el
  operador no distingue de una flota vacía.
- **Motor parado** (`9110` no responde o `tick_age_s > 90`): la tabla está bien y el banner de motor está rojo;
  no se mezclan.

### 4.4 No perder la posición al actualizar

1. Los deltas parchean celdas; **nunca** se reemplaza el array ni se reordena.
2. En `resync`, se recarga la página **anclada al primer `node_id` visible** (`after=<ancla>`), y se fusiona por
   `node_id` conservando `scrollTop`. Si el ancla desapareció (nodo retirado), se mantiene el índice y se avisa
   con "2 filas de esta página ya no existen".
3. Nunca se hace `scrollIntoView` automático, ni "salta a la fila nueva": con 2000 altas por minuto el operador
   no podría leer. Las novedades se cuentan en una píldora ("+37 nuevas") que se pulsa a voluntad.
4. El filtro y el ancla viven en la URL (`?status=live&after=…`), así que F5 o compartir el enlace devuelve
   exactamente la misma vista.

## 5. Auth: token de admin en cabecera, solo lectura

- **Un único token**: `PANEL_ADMIN_TOKEN`, con `envOr` sobre `API_ADMIN_TOKEN` para no inventar otra
  credencial en el VPS (`internal/backend/config.go:48`, default `dev-admin-token`, que es el que usa la puerta
  en `check-panel.sh:44`). Se lee una vez al arrancar; rotar = `systemctl restart botnet-panel`.
- Cabecera `Authorization: Bearer <token>` en **todas** las rutas: `/api/nodes`, `/api/summary`,
  `/api/stream`, `/healthz` (200 sin token, solo `{ok,db,uptime_s}` sin datos de flota).
- Comparación en tiempo constante: `subtle.ConstantTimeCompare([]byte(got), []byte(want))` tras comprobar
  longitud, como pide el blueprint §6. Sin cookies, sin sesiones, sin `Set-Cookie`: no hay CSRF que defender.
- **El token no viaja en la URL.** Nada de `?token=`: acaba en el `access_log`, en el historial y en el
  `Referer`. Por eso el cliente usa `fetch`-stream y no `EventSource` (§2.2).
- CORS: `Allow-Origin` desde config (por defecto `http://127.0.0.1:3100`), `Allow-Methods: GET,OPTIONS`,
  `Allow-Headers: Authorization, Last-Event-ID, Content-Type`.
- Solo lectura, garantizada dos veces: (a) `internal/panel/*.go` no contiene ni una sentencia de escritura
  (la puerta lo grepea en `check-panel.sh:216`; **ojo: el grep también cuenta comentarios**, así que ningún
  comentario de ese paquete puede contener la cadena `UPDATE nodes`), y (b) rol Postgres `panel_ro` con
  `GRANT SELECT ON nodes, recruit_candidates, recruit_candidate_events, recruit_planner_ticks TO panel_ro` y
  nada más. Si el código se equivoca, el motor de la base lo para.
- 401 con `WWW-Authenticate: Bearer` y cuerpo `{"error":"unauthorized"}`; 403 si el token es válido pero la
  ruta no está permitida (no hay ninguna hoy: todo es lectura).

## 6. Exponencialidad visible sin ser fuente de verdad

El panel **no calcula el crecimiento**: lo **reproduce** con la misma consulta que la puerta y lo enseña. Si
discrepan, la puerta tiene razón.

- **Contadores por ventana**: candidatos descubiertos, nodos enrolados y nodos verificados por hora, leídos de
  `recruit_candidates.created_at`, `nodes.enrolled_at` y `recruit_socks5_checks.checked_at`, con
  `date_trunc('hour', …)`. Anti-doble-conteo: `count(DISTINCT candidate_id)` / `count(DISTINCT node_id)`, y la
  ventana en curso marcada `partial`.
- **Ritmo**: `ratio = c_actual / c_anterior` por ventana y el criterio del blueprint (x1,5 tres ventanas
  seguidas) mostrado como tres píldoras verdes/rojas, más "proyección a 24 h" (`c_actual * ratio^24`) que se
  etiqueta explícitamente como **proyección**, no como hecho.
- **Nada de esto se escribe en la base**: la fuente de verdad son las filas, y el panel solo las agrega. Si el
  panel se cae, el crecimiento sigue ocurriendo y la puerta lo sigue midiendo con `psql`.

**Detección de motor atascado** (una señal, cinco causas, cero ambigüedad). El panel consulta
`127.0.0.1:9110/healthz` y `/metrics` cada 10 s y declara `stuck` con la primera que muerda:

| Causa | Señal | Umbral | Fuente |
|---|---|---|---|
| latido congelado | `botnet_recruit_orchestrator_tick_age_seconds` | > 90 | `internal/recruit/store.go:79` |
| ritmo cero sin pausa | `rate_class == 0 && !paused` | exacto | `internal/recruit/httpapi.go:60` |
| presupuesto sin comprar | `queue_depth` crece y `pps_effective == 0` | 3 ventanas | `internal/recruit/metrics.go:165` |
| embudo atascado | `enroll_backlog` | > 500 (`EmbudoMaximo`) | `internal/recruit/planner.go:31` |
| motor mudo | `/healthz` no responde o 503 | 3 fallos seguidos | `httpapi.go:40` |

El banner muestra la causa y la última decisión del planificador con su motivo
(`SELECT action, reason FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 1`), enlazando al runbook
`docs/runbooks/recruit.md` §"Qué mirar cuando algo va mal". El panel **no** pausa, **no** reanuda, **no**
escribe en `recruit_control`: avisa y el operador decide.

## 7. Riesgos ordenados por gravedad

| # | Riesgo | Por qué duele | Mitigación |
|---|---|---|---|
| 1 | **Fuga de conexiones SSE** (goroutines + descriptores) | cada pestaña cerrada a lo bruto deja goroutine y socket; con 256 `TasksMax` del `recruit.slice` se ahoga el motor | `ctx.Done()` en el handler; `ping` cada 15 s con `SetWriteDeadline(5s)` para detectar el par muerto; canal por cliente de 64 con política *lag → `resync` y cierre*; tope duro `MaxClients=64` (429 + `Retry-After`) y 4 por IP; `http.Server{ReadHeaderTimeout:5s, IdleTimeout:120s, WriteTimeout:0}` (0 obligatorio: un `WriteTimeout` mata el stream a los N s); unidad propia fuera de `recruit.slice` con `TasksMax=64`; métrica `botnet_panel_stream_conns` y alarma si no vuelve a 0 tras cerrar 200 clientes (AC6) |
| 2 | **Tormenta de reconexión** | al reiniciar el panel, las 20 pestañas vuelven a la vez: `TasksMax` y `max_connections` de Postgres en rojo | `retry: 3000` + jitter exponencial **en el cliente** (1 s→30 s, tope 5); un solo stream por pestaña (singleton de módulo, se cierra el anterior antes de abrir otro); 429 con `Retry-After: 5` por encima de 4 por IP; el rastreador es **uno** para todos los clientes, así que N reconexiones no son N consultas |
| 3 | **El panel se come la CPU/BD del motor** | comparte VPS con `CPUQuota=300%`/`MemoryMax=1536M` para reclutamiento | `botnet-panel.service` en slice propio (`CPUQuota=50%`, `MemoryMax=256M`, `TasksMax=64`), `--db-pool=2`, `count(*)` a 10 s (60 s si tarda > 200 ms), keyset sin `OFFSET`, barrido cíclico en páginas de 5000 |
| 4 | **Falso "todo bien" con el stream colgado** | TCP medio muerto: la tabla parece viva y lleva 40 min congelada | watchdog de cliente a 3×`hb_s`; `count` de contraste cada 10 s; el indicador tiene estado *reconectando* y *caído* con hora de última foto (§4.2) |
| 5 | **Deltas perdidos o cursor purgado** | retención de `recruit_candidate_events` deja un `Last-Event-ID` huérfano | `resync` con motivo, y deltas **absolutos** (idempotentes); el contraste `count(*)` cierra cualquier deriva |
| 6 | **Ráfaga de altas satura el stream** | 50k altas en 60 s: 50k eventos × N pestañas | coalescido 1 Hz por nodo, batch de 200, y por encima de 5000 deltas en un flush se manda `burst` + `resync` (recarga de página) |
| 7 | **Base caída se pinta como flota vacía** | el operador no distingue "no hay nadie" de "no puedo mirar" | 503 estructurado + última foto atenuada con hora; `[]` prohibido con base caída (AC7) |
| 8 | **Token filtrado / XSS en el panel** | es un token de admin, aunque de solo lectura | solo lectura por rol `panel_ro` y por ausencia de escrituras; sin token en URL; `constant-time`; rotación por reinicio; el panel no renderiza HTML del nodo (`model`/`banner` como texto, nunca `dangerouslySetInnerHTML`) |
| 9 | **El panel escribe en `nodes`** | rompe la decisión "solo lectura" y el criterio 8 de la puerta | `internal/panel/*.go` sin escrituras (ojo a los comentarios), `panel_ro`, y el test de aislamiento de `check-panel.sh:210` |

## 8. Coste en tareas y días

| Tarea | Entregable | Días | Depende |
|---|---|---|---|
| T-P3-01 Esqueleto y auth | `cmd/botnet-panel/main.go`, `internal/panel/{server,auth}.go`, CORS, 401/503, `/healthz`, `--db-pool=2`, puerto 9120 | 1,5 | — |
| T-P3-02 Consulta y contraste | `GET /api/nodes` keyset, `GET /api/summary`, `count(*)` con degradación, `panel_ro` | 1,5 | T-P3-01 |
| T-P3-03 Rastreador de eventos | tailer de `recruit_candidate_events` + watermark de `nodes` + barrido cíclico + coalescido | 1,5 | T-P3-02 |
| T-P3-04 Stream SSE | `/api/stream`, `hello`/`cand`/`node`/`count`/`stuck`/`burst`/`resync`, `Last-Event-ID`, `hb`, límites y 429 | 1,5 | T-P3-03 |
| T-P3-05 UI | tabla virtualizada, indicador de 3 estados, vacío/caído/atascado, ancla de scroll, parser SSE con `fetch` | 2 | T-P3-04 |
| T-P3-06 Operación | `deploy/systemd/botnet-panel.service`, ampliar `scripts/check-panel.sh` (`stream`, `stream_fugas`, `base_caida`), runbook `docs/runbooks/panel.md` | 1 | T-P3-05 |

**Total: 9 días efectivos**, ~2 semanas de calendario. Ruta crítica 01→02→03→04→05; T-P3-06 arranca el día 6
en paralelo (la unidad systemd y las tres comprobaciones nuevas se pueden escribir contra el contrato antes de
que la UI exista).

Nota de alcance: las secciones 4, 7 y 9 de la puerta (`check-panel.sh`) piden **exactamente 8 secciones**; las
tres comprobaciones nuevas que propongo suben el contrato a 11, así que la propia puerta necesita su task de
ajuste (`SECCIONES` y `comprobaciones=`) o se declaran como criterios de esta propuesta sin tocar la puerta
existente. Lo primero es preferible: una puerta que no mide lo que promete no muerde.

## 9. Ocho criterios de aceptación medibles

Preflight: `source scripts/env.sh`; `PANEL=http://127.0.0.1:9120`; `TOKEN=${PANEL_TOKEN:-dev-admin-token}`;
`psql "$DATABASE_URL"` operativo.

| # | Criterio | Comando | Valor esperado |
|---|---|---|---|
| AC1 | Auth por cabecera | `curl -s -o /dev/null -w '%{http_code}' $PANEL/api/nodes?limit=1` y `… -H "Authorization: Bearer $TOKEN"` | `401` sin cabecera, `200` con ella |
| AC2 | El panel sirve **todas** las filas, ni una más ni una menos | `curl -s -H "Authorization: Bearer $TOKEN" "$PANEL/api/nodes?limit=200000" \| grep -o '"node_id":"[^"]*"' \| sort > /tmp/p; psql -tAc "SELECT node_id::text FROM nodes WHERE deleted_at IS NULL" "$DATABASE_URL" \| sort > /tmp/d; diff -q /tmp/p /tmp/d` | sin salida, `exit 0` (y `wc -l` igual en ambos) |
| AC3 | Estados verbatim | `comm -23 <(curl -s -H "Authorization: Bearer $TOKEN" "$PANEL/api/nodes?limit=200000" \| grep -o '"status":"[^"]*"' \| cut -d'"' -f4 \| sort -u) <(psql -tAc "SELECT DISTINCT status FROM nodes" "$DATABASE_URL" \| sort -u)` | vacío |
| AC4 | El stream existe y late | `curl -sN -H "Authorization: Bearer $TOKEN" --max-time 3 $PANEL/api/stream \| grep -cE '^(event: hello\|: hb)'` | `≥ 2` (`hello` + al menos un `hb`) |
| AC5 | `Last-Event-ID` se honra | `C=$(psql -tAc "SELECT max(event_id) FROM recruit_candidate_events" "$DATABASE_URL"); C=$((C-1)); curl -sN -H "Authorization: Bearer $TOKEN" -H "Last-Event-ID: $C" --max-time 2 $PANEL/api/stream \| grep -c "^id: $((C+1))$"` | `≥ 1` para un `event_id` vivo; y con `Last-Event-ID: 1` sobre tabla purgada, `event: resync` con `reason:"cursor_purgado"` |
| AC6 | Sin fugas de conexiones | `for i in $(seq 200); do curl -sN -H "Authorization: Bearer $TOKEN" $PANEL/api/stream >/dev/null & done; sleep 5; pkill -f 'api/stream'; sleep 5; curl -s $PANEL/metrics \| awk '/^botnet_panel_stream_conns/{print $2}'` | `0` a los 5 s de matar los 200 clientes (y `< 64` en ningún momento se sirve más de 64: el resto recibe 429) |
| AC7 | Base caída = 503, nunca lista vacía | `systemctl stop postgresql` (o `curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" "$PANEL/api/nodes?dsn_probe=1"` en CI sin tocar el servicio) | `503` y cuerpo que contiene `"error":"db_down"`; nunca `[]` ni `200` |
| AC8 | Crecimiento y atasco medidos, no inventados | `A=$(curl -s -H "Authorization: Bearer $TOKEN" $PANEL/api/summary \| grep -o '"total":[0-9]*' \| cut -d: -f2); B=$(psql -tAc "SELECT count(*) FROM nodes WHERE deleted_at IS NULL" "$DATABASE_URL"); [ "$A" = "$B" ] && echo igual` y `psql -tAc "UPDATE recruit_control SET tick_at=now()-interval '5 min'" "$DATABASE_URL"; curl -s -H "Authorization: Bearer $TOKEN" $PANEL/api/summary \| grep -o '"stuck":"[a-z_]*"'` | `igual`; y `"stuck":"latido_congelado"` con `tick_age_s > 90` (revertir con `UPDATE recruit_control SET tick_at=now()`) |

### Sabotajes que demuestran que la puerta muerde

Cada sabotaje se aplica, se mide el rojo, y se revierte. Si un sabotaje no muerde, la comprobación mira al
sitio equivocado.

1. **Estado inventado (AC3).** En `internal/panel/nodes.go`, cambiar el `SELECT` para servir
   `status || ' '` → `comm -23` devuelve `live ` y la sección `estados` de `check-panel.sh` imprime
   `FAIL estados inventados:live` con `exit ≥ 1`. Revertir y volver a verde.
2. **Lista vacía con la base caída (AC7).** En el handler, sustituir el 503 por `c.JSON([])` cuando
   `QueryContext` falla: con `dsn_probe=1` la comprobación `panel: /api/nodes responde 200` pasa, pero AC7
   exige `503` y falla con `error:""` ausente. Es el sabotaje que hoy **no** existe y debería: la puerta
   actual deja el caso a medias (`check-panel.sh:145` solo lo menciona en un comentario), así que este
   criterio es el que cierra el agujero.
3. **`Last-Event-ID` ignorado (AC5).** Borrar la lectura de la cabecera en el handler del stream: AC5 pasa de
   `≥ 1` a `0` y el cliente duplica o pierde deltas. Muerde en la comprobación, no en la UI.
4. **Fuga de conexiones (AC6).** Quitar el `defer` que cierra el canal y el `unregister` del hub: tras matar
   los 200 clientes, `botnet_panel_stream_conns` se queda en `200` y AC6 falla. Es el sabotaje más importante:
   una fuga se ve a la tercera hora de guardia, no en el primer `curl`.
5. **Motor atascado invisible (AC8).** Desconectar la lectura de `9110/metrics` (usar solo la base): con
   `tick_at` congelado, `/api/summary` devuelve `"stuck":null` y AC8 falla; la tabla sigue verde mientras el
   motor está mudo, que es exactamente el fallo que esta propuesta quiere hacer imposible.
