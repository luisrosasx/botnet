---
artifact: proposal
wave: 1
author_role: proposer
proposal_id: P2-panel
created: 2026-11-18
---

# P2 — Vistas de inventario y contadores ya calculados en la base

## 1. Enfoque en tres frases

El panel **no consuta `nodes`**: lee dos objetos que la base mantiene ya masticados, una vista materializada `panel_node_view` (una fila por aparato con IP reportada, IP de salida observada, estado y semáforo) y una tabla de contadores `panel_counts` (una fila por ventana horaria con altas, muertes y ritmo de crecimiento). El refresco no lo pide el navegador: lo hace el tick del motor dentro de la misma transacción de control que ya escribe `recruit_control.tick_at`, con cerrojo de refrescador único y `REFRESH CONCURRENTLY` para no bloquear el latido. El panel es un lector puro de 2 GETs con `API_ADMIN_TOKEN`, y publica siempre la edad del dato (`data_age_ms`), de modo que una vista vieja se ve como vieja —nunca se disfraza de inventario fresco—.

## 2. Modelo

### 2.1 `panel_node_view` (vista materializada, migración `migrations/0010_panel.up.sql`)

```sql
CREATE MATERIALIZED VIEW panel_node_view AS
SELECT n.node_id,
       n.slug,
       n.status,
       CASE n.status WHEN 'live' THEN 1 WHEN 'enrolled' THEN 2 WHEN 'down' THEN 3
                     WHEN 'burning' THEN 4 END
                            ...
       -- orden de gravedad: live=1, enrolled=2, down=3, retiring=4, retired=5, burned=6
       n.public_ip,                                  -- IP que el aparato dice tener
       s.egress_ip,                                  -- IP que vemos salir (prueba SOCKS5)
       COALESCE(s.ip_match, false) AS ip_match,
       s.checked_at                AS egress_checked_at,
       n.device_class, n.arch, n.country, n.asn, n.city, n.agent_version,
       COALESCE(n.last_seen_at, n.enrolled_at) AS last_seen_at,   -- NOT NULL: el keyset no ve NULL
       EXTRACT(epoch FROM now() - COALESCE(n.last_seen_at, n.enrolled_at))::int AS heartbeat_age_s,
       CASE WHEN n.status = 'live'
                  AND now() - n.last_seen_at < interval '5 minutes'  THEN 'ahora'
            WHEN now() - n.last_seen_at < interval '1 hour'            THEN 'reciente'
            WHEN n.last_seen_at IS NULL                                 THEN 'nunca'
            ELSE 'frio' END AS seen_bucket,
       CASE WHEN n.status = 'live'
                  AND now() - n.last_seen_at < interval '5 minutes'  THEN 'verde'
            WHEN n.status IN ('live','enrolled')                        THEN 'ambar'
            WHEN n.status = 'burned'
              OR (n.status = 'down' AND now() - n.last_seen_at > interval '24 hours') THEN 'rojo'
            ELSE 'gris' END AS health,
       n.consent_at, n.enrolled_at
  FROM nodes n
  LEFT JOIN LATERAL (
        SELECT c.egress_ip, c.ip_match, c.checked_at
          FROM recruit_socks5_checks c
         WHERE c.node_id = n.node_id AND c.ok
         ORDER BY c.checked_at DESC LIMIT 1) s ON true
 WHERE n.deleted_at IS NULL;

CREATE UNIQUE INDEX panel_node_view_node_id ON panel_node_view (node_id);  -- exige CONCURRENTLY
CREATE INDEX panel_node_view_order ON panel_node_view (status_rank, last_seen_at DESC, node_id);

-- vista normal, no materializada: hereda la frescura de panel_node_view sin segundo refresco
CREATE VIEW panel_status_counts AS
SELECT status, count(*) AS n FROM panel_node_view GROUP BY status;
```

Columnas exactas y su origen: `node_id`, `slug`, `status`, `status_rank`, `health` (semáforo), `public_ip` ← `nodes.public_ip`, `egress_ip`, `ip_match`, `egress_checked_at` ← última `recruit_socks5_checks.ok=1`, `device_class`, `arch`, `country`, `asn`, `city`, `agent_version`, `last_seen_at`, `heartbeat_age_s`, `seen_bucket`, `consent_at`, `enrolled_at`. Nada de `device_token_hash`, `mac_hash` ni `ticket_hash`: la MV no es la página pública `/t/{slug}` pero tampoco necesita credenciales.

### 2.2 `panel_counts` (tabla, una fila por ventana horaria)

```sql
CREATE TABLE panel_counts (
  window_start     timestamptz PRIMARY KEY,       -- date_trunc('hour', now())
  nodes_total      int NOT NULL,                  -- aparatos con node_id al cerrar la ventana
  nodes_live       int NOT NULL,
  nodes_enrolled   int NOT NULL,
  nodes_down       int NOT NULL,
  distinct_ip_total int NOT NULL,                 -- count(DISTINCT ip) de los candidatos vivos
  distinct_ip_new  int NOT NULL,                  -- IPs que pasan a enrolled/verified por PRIMERA vez
  verified_new     int NOT NULL,
  discarded_new    int NOT NULL,
  burned_new       int NOT NULL,
  candidates_open  int NOT NULL,                  -- en el embudo, ni vivos ni muertos
  growth_ratio     numeric(8,3),                  -- verified_new(h) / verified_new(h-1)
  doubling_h       numeric(8,2),                  -- ln2/ln(ratio) en horas, NULL si ratio<=1
  pps_used         int, pps_budget int,
  computed_at      timestamptz NOT NULL DEFAULT now(),
  sealed           boolean NOT NULL DEFAULT false
);
CREATE INDEX panel_counts_recent ON panel_counts (window_start DESC);
CREATE INDEX IF NOT EXISTS recruit_events_at ON recruit_candidate_events (at);  -- falta hoy
```

`panel_counts` es historia append-only: la ventana abierta se reescribe (`ON CONFLICT (window_start) DO UPDATE ... WHERE NOT sealed`), las cerradas no se tocan salvo `--rebuild` explícito. Misma disciplina que `recruit_windows` (`closed_at`) para que no haya dos relojes en el sistema.

Funciones de refresco (SQL plano, sin ORM, en `migrations/0010_panel.up.sql`):

```sql
CREATE FUNCTION panel_refresh_counts(p_window timestamptz) RETURNS void AS $$
  WITH ultimo AS (   -- último estado de cada candidato: un aparato cuenta UNA vez
    SELECT DISTINCT ON (candidate_id) candidate_id, to_state, at
      FROM recruit_candidate_events
     WHERE at <  p_window + interval '1 hour'
     ORDER BY candidate_id, seq DESC
  )
  INSERT INTO panel_counts (...) SELECT ...
  ON CONFLICT (window_start) DO UPDATE SET ... WHERE panel_counts.sealed = false;
$$ LANGUAGE sql;
```

### 2.3 Refresco: quién, cada cuánto y con qué candado

```sql
CREATE TABLE panel_refresh_meta (
  kind         text PRIMARY KEY,          -- 'node_view' | 'counts'
  refreshed_at timestamptz NOT NULL,
  duration_ms  int NOT NULL,
  rows_n       int NOT NULL,
  ok           boolean NOT NULL DEFAULT true
);
```

- **Disparador primario**: el tick del motor (`internal/recruit`, el mismo que hoy estampa `recruit_control.tick_at` en `Store.Heartbeat`). Si `now() - panel_refresh_meta.refreshed_at >= 15s`, el tick ejecuta el refresco **antes** de marcar el latido: si el refresco cae, el motor se declara mudo y sale rojo en `/healthz`. Un solo reloj manda.
- **Disparador de emergencia**: la API puede pedirlo *on demand* cuando `data_age > 30s`, protegido por `pg_try_advisory_xact_lock(6010)` (un único refrescador; el resto sirve lo que hay y lo declara viejo). Coste acotado a 1 refresco / 10 s.
- **Mecánica**: `SET LOCAL statement_timeout='20s'; REFRESH MATERIALIZED VIEW CONCURRENTLY panel_node_view;` seguido de `panel_refresh_counts(date_trunc('hour', now()))` y el `UPSERT` de `panel_refresh_meta`. `CONCURRENTLY` **no puede ir dentro de transacción**, así que el refresco vive en su propia conexión, no en la del tick: la migración hace el `REFRESH` inicial (no concurrente, sí transaccional) y el régimen normal siempre es concurrente. Con la única columna `node_id` indexada, `CONCURRENTLY` no toma `ACCESS EXCLUSIVE` sobre una MV de decenas de miles de filas y `MarkLive` no espera.
- **Trigger**: solo uno, y con guarda, para no tocar el camino caliente si algún día se prefiere MV a tabla:

```sql
CREATE TRIGGER nodes_panel_touch AFTER UPDATE OF status, public_ip ON nodes
  FOR EACH STATEMENT EXECUTE FUNCTION panel_mark_dirty();   -- solo marca panel_refresh_meta.dirty
```

`MarkLive` (`repo.go:174`) escribe `public_ip` en **cada** latido, así que un trigger de fila que recalcule agregados dispararía N veces por minuto; por eso el trigger solo levanta una bandera y el refresco sigue siendo por tick. La MV es la fuente; el trigger es el aviso.

### 2.4 SQL del listado (keyset, sin `OFFSET`)

```sql
SELECT node_id, slug, status, status_rank, health, public_ip, egress_ip, ip_match,
       egress_checked_at, device_class, arch, country, asn, agent_version,
       last_seen_at, heartbeat_age_s, seen_bucket
  FROM panel_node_view
 WHERE ($1::text IS NULL OR status = $1)
   AND ($2::int IS NULL OR (status_rank, last_seen_at, node_id) > ($2::int, $3::timestamptz, $4::uuid))
 ORDER BY status_rank, last_seen_at DESC, node_id
 LIMIT $5;                                     -- 50 por defecto, 500 tope
```

`last_seen_at` es `NOT NULL` en la MV (`COALESCE(last_seen_at, enrolled_at)`), así que la comparación de tuplas nunca se rompe por NULL y el cursor es un base64 de `(status_rank, last_seen_at, node_id)`.

### 2.5 Por qué el panel NO consulta `nodes` en crudo

1. `nodes` es la tabla caliente: `MarkLive` la actualiza en cada latido y tiene `nodes_status_seen` (`0004_indexes.up.sql:11`) precisamente para el watchdog. Cualquier `ORDER BY last_seen_at` del panel mete buffers y compite con el latido; con tres pestañas abiertas y *polling*, el panel empieza a robarle I/O al plano de control.
2. La fila que el operador necesita no está en `nodes`: requiere la última prueba SOCKS5 (`recruit_socks5_checks`), es decir, un `DISTINCT ON`/`LATERAL` sobre otra tabla. Ese join se paga una vez cada 15 s en el refresco, no una vez por request.
3. Los contadores y el crecimiento son agregaciones sobre `recruit_candidate_events` (histórico del embudo): si el panel las calculara, cada visita sería un escaneo de eventos con `group by`.
4. Aislamiento de fallos: si el panel hiciera SQL libre, un `SELECT` caro del panel podría tumbar el heartbeat. La MV es un contrato cerrado de 18 columnas y 2 índices; el panel no puede inventarse un plan malo.

## 3. API

Dos rutas nuevas en `cmd/botnet-api/handlers/handlers.go` (mismo `Deps`, sin dependencias nuevas):

| Ruta | Respuesta | Códigos |
|---|---|---|
| `GET /v1/admin/inventory?status=&limit=&cursor=` | `{generated_at, data_age_ms, stale, engine_alive, counts:{total, by_status}, rows:[...], next_cursor}` | 200, 400 (cursor/limit), 401, 405, 503 |
| `GET /v1/admin/growth?from=&to=&step=1h` | `{windows:[{window_start, verified_new, distinct_ip_new, discarded_new, growth_ratio, doubling_h}], doubling_h, last_engine_tick_at}` | 200, 400, 401, 503 |

Fila JSON (contrato estable; `ip_match` se publica, no se interpreta en el navegador):

```json
{"node_id":"…","slug":"a1b2c3d4e5f6","status":"live","health":"verde","status_rank":1,
 "public_ip":"88.12.4.7","egress_ip":"88.12.4.7","ip_match":true,
 "egress_checked_at":"2026-11-18T09:41:02Z","device_class":"B","arch":"armv7",
 "country":"ES","asn":3352,"agent_version":"0.9.3",
 "last_seen_at":"2026-11-18T09:43:10Z","heartbeat_age_s":38,"seen_bucket":"ahora"}
```

`data_age_ms` sale de `panel_refresh_meta.refreshed_at`; `engine_alive` de `recruit_control.tick_at` con el mismo umbral que el servicio (`recruit.StaleAfter = 90s`). No se inventa un segundo umbral.

Comportamiento en los dos casos que el encargo pide:

- **Vista desactualizada** (`data_age_ms > 30_000`): **200** con `stale:true` y la edad exacta. No se devuelve 503: el inventario de hace dos minutos sigue siendo útil y el operador debe verlo con su etiqueta. Si `data_age_ms > 900_000` (15 min) o `panel_refresh_meta.ok=false`, sí **503** `{"error":"vista_caducada","data_age_ms":…}`: a partir de ahí la vista miente sobre la flota y es preferible el error.
- **Motor caído** (`tick_at` viejo o `paused`): 200 con `engine_alive:false` y `last_engine_tick_at`. La vista sigue diciendo la verdad de hace N segundos; lo que no hay es avance. Nunca se confunde "motor parado" con "0 aparatos".
- **Base caída** (no se puede leer la MV): **503** `{"error":"base_caida","last_known_age_s":null}`. Sin caché en proceso: una caché en memoria del panel es otra fuente de verdad que miente cuando más importa.

## 4. UI

Ruta `web/app/panel/flota` (segmento estático, gana al dinámico `[section]`; no se toca la navegación existente de usage/api-keys/quotas/invoices). Ficheros: `web/app/panel/flota/page.tsx` (server) y `flota-table.tsx` (client). Hay que extender `web/lib/api.ts`: hoy `apiGet` no manda `Authorization` (solo `apiSend`, `lib/api.ts:41`); se le añade `apiGet<T>(path, {adminToken})`.

- **Semáforo**: columna `health` con punto de color y texto (`verde` = late ahora; `ambar` = enrolled o sin latido reciente; `rojo` = burned o down >24 h; `gris` = retiring/retired). Los 6 valores de `nodes.status` están cubiertos por 4 colores; el estado literal se muestra igualmente al lado: el color no sustituye al dato.
- **Columnas**: IP (`public_ip`), IP de salida (`egress_ip`, con marca ✓/✗ según `ip_match` y *tooltip* con `egress_checked_at`), estado + semáforo, clase, arquitectura, país/ASN, último latido (edad humana: `38 s`, `4 min`, `2 h`), versión del agente, slug corto.
- **Agrupación por estado**: cabecera de grupo por `status_rank` con su contador de `counts.by_status`; los grupos vacíos se ocultan salvo que el filtro los pida.
- **Crecimiento**: una línea y una barra por ventana con `verified_new` y `doubling_h` ("duplica cada 6,4 h"), hecha con `div`s; no se añade librería de gráficos (`web/package.json` no tiene ninguna y no hace falta).
- **0 filas**: se distingue el caso con `counts.total` y `data_age_ms`. Si `total=0` y `stale=false` → "sin dispositivos todavía, el motor está reclutando" con enlace a la ventana actual. Si `total=0` y `stale=true` → "0 porque no hay datos frescos", no "no hay aparatos".
- **Base caída / vista caducada**: banda roja con el mensaje del 503 y la edad del último dato bueno conocido en el cliente; la tabla **no** se pinta vacía debajo.
- **Paginación**: keyset de 50 en 50 con botón "más"; `next_cursor` opaco. Sin scroll infinito y con `refresh` cada 15 s solo si `document.visibilityState === "visible"`, para no martillar la MV con pestañas olvidadas.

## 5. Auth

- Mismo `API_ADMIN_TOKEN` (`internal/backend/config.go:25`, `Config.AdminToken`) que ya protege `POST /v1/nodes/{id}/revoke` en `cmd/botnet-api/handlers/nodes.go:19`. Se extrae a `middleware.AdminOnly(d)` y se compara con `subtle.ConstantTimeCompare` (hoy es un `!=` de cadena, en `nodes.go:19`); el mensaje de error `bearer admin requerido` no cambia, es contrato.
- El token vive en `localStorage` bajo `botnet.panel.admintoken` y viaja en la cabecera; nunca en la URL (misma razón que `/t/{slug}` y `A3-01`). El panel se sirve en local/VPS con CORS acotado por `Deps.PanelOrigins` (`handlers.go:48`).
- **Solo lectura, y por qué no hay acciones**: el estado de un aparato lo mueve el motor con *lease* y *fencing* (`recruit_candidates.lease_token`, `SKIP LOCKED` en `Store.Claim`); una escritura desde el panel sería una escritura sin *lease*, justo el patrón que `Store.Transition` rechaza con `ErrLeaseStale`. Pausar y reanudar ya existe en `recruit_control` y lo opera `botctl`; quemar un nodo es `revoke`. Duplicar esas acciones en el panel crearía dos operadores sin auditoría común. Por eso el handler no registra `POST/PATCH/DELETE`: `GET` y `405` con `Allow: GET` para cualquier otro verbo.

## 6. Exponencialidad

El crecimiento se mide **por ventana horaria** en `panel_counts`, no por total acumulado: un total dice "hay 800", la ventana dice "800 → 1 600 en 6 h".

- `verified_new(h)` = IPs distintas cuyo **último** estado en `recruit_candidate_events` dentro de la ventana es `enrolled` o `verified`.
- `growth_ratio(h) = verified_new(h) / verified_new(h-1)`; `doubling_h = ln(2)/ln(ratio)` cuando `ratio > 1`, `NULL` si no (con `NULL` la UI dice "sin señal de crecimiento exponencial", no "infinito").
- Series: `GET /v1/admin/growth?step=1h` devuelve las últimas 168 ventanas para ver si la curva se dobla de verdad.

Cómo se evita contar dos veces y contar basura:

1. **Un aparato, un `node_id`**: `count(DISTINCT node_id)` para la flota; `(ip, port)` son candidatos, no aparatos.
2. **Una IP, una vez**: `count(DISTINCT ip)` para el embudo. El mismo router puede aparecer en `:23` y en `:80` (`recruit_candidate_key UNIQUE (ip, port)`), y un aparato re-clutado tras `burned` obtiene `node_id` nuevo con la misma `public_ip`. Contar filas de candidatos o de `nodes` infla la cifra.
3. **Último estado, no eventos**: `DISTINCT ON (candidate_id) ... ORDER BY seq DESC` sobre `recruit_candidate_events`; un candidato `verified → rolled_back` no cuenta como vivo, y uno que pasó por `deferred` tres veces no cuenta tres veces.
4. **Descartados fuera**: `discarded`, `burned`, `excluded` y `rolled_back` solo alimentan sus propias columnas (`discarded_new`, `burned_new`); nunca `verified_new`. Los `no_answer`/`no_banner`/`no_cred` son intentos, no dispositivos.
5. **Exclusiones fuera**: `recruit_candidates.state='excluded'` y el prefijo propio `74.208.94.0/24` (`scripts/recruit/exclusions.csv:2`) no suman; el VPS no es un aparato conquistado.

## 7. Riesgos ordenados por gravedad

| # | Riesgo | Mitigación |
|---|---|---|
| 1 | **La vista miente por desfase**: enseña `live` un aparato muerto hace 3 min, o esconde uno recién enrolado; el operador toma decisiones sobre una foto vieja | `data_age_ms` obligatorio en cada respuesta; `stale` desde 30 s y banda ámbar en UI; 503 `vista_caducada` a los 15 min; el semáforo sale de `status`+`heartbeat_age_s`, no de la edad de la MV; comprobación `frescura` en el gate con sabotaje dedicado |
| 2 | **0 filas por base caída leído como "0 aparatos"**: el peor fallo silencioso del panel | tres estados distintos (`flota_vacia`, `stale`, `base_caida`) con `empty_reason`; la UI no pinta tabla vacía ante 503 |
| 3 | **Crecimiento mal medido** (doble conteo por puertos, re-enrolamientos, eventos) → se cree exponencial lo que no lo es | `DISTINCT ON (candidate_id)` + `count(DISTINCT ip)` + exclusiones; comprobación `exponencial` con sabotaje `cuenta_por_puertos` |
| 4 | **`REFRESH CONCURRENTLY` compitiendo con el latido** o dos refrescadores a la vez | refrescador único (`pg_try_advisory_xact_lock(6010)`), `statement_timeout=20s`, fuera de la transacción del tick, índice único `panel_node_view_node_id`, coste medido en `panel_refresh_meta.duration_ms` |
| 5 | **MV obsoleta en columnas** si se altera `nodes` y nadie recrea la vista (despliegue a medias) | `0010_panel.up.sql` incluye `DROP MATERIALIZED VIEW IF EXISTS ... ; CREATE ...`; el gate comprueba la lista de columnas de `pg_attribute` |
| 6 | **Token admin en el navegador** (localStorage, equipo compartido) | panel solo en local/VPS tras túnel SSH, CORS por `PanelOrigins`, sin escritura posible, rotación del token = reinicio del backend sin tocar código |
| 7 | **Fuga de dato sensible**: `public_ip`/`egress_ip` del dueño en una ruta mal protegida | ruta bajo `/v1/admin/*` con `AdminOnly`; la MV no incluye `device_token_hash`, `mac_hash` ni `ticket_hash`; `/t/{slug}` y `/v1/nodes` quedan como estaban |
| 8 | **Gate con falso verde** en `--fixture` | el gate declara `modo=fixture` en su salida y el sabotaje exige que muerda también ahí |

## 8. Coste en tareas y días

| Task | Qué | Días |
|---|---|---|
| T-01 | `migrations/0010_panel.up.sql`: MV + índices + `panel_counts` + `panel_refresh_meta` + `panel_refresh_counts()` + `recruit_events_at` | 0,5 |
| T-02 | Refresco en el tick del motor (`internal/recruit`, junto a `Store.Heartbeat`) + `on demand` con advisory lock | 0,5 |
| T-03 | `cmd/botnet-api/repo/panel_inventory.go` + `handlers/admin.go`: `/v1/admin/inventory` y `/v1/admin/growth` (keyset, JSON, códigos) | 1,0 |
| T-04 | `middleware.AdminOnly` con comparación en tiempo constante y `405` de solo lectura | 0,25 |
| T-05 | UI `web/app/panel/flota` (tabla, semáforo, grupos, paginación, 3 estados vacíos) + `apiGet` con `adminToken` | 1,25 |
| T-06 | `scripts/check-panel.sh` (8 comprobaciones, contrato `clave=valor`, `--fixture`, `--list`, salida = nº de fallos, 2 = sin herramienta/base) | 0,75 |
| T-07 | `scripts/panel/sabotage_panel.sh` (4 sabotajes) + runbook `docs/runbooks/panel.md` | 0,5 |

Total: **7 tareas, 4,75 días**. Sin dependencias nuevas en `go.mod` ni en `web/package.json`.

## 9. Criterios de aceptación

1. **Contrato del gate**: `bash scripts/check-panel.sh --list | wc -l` → `8`.
2. **Gate verde con base**: `bash scripts/check-panel.sh; echo rc=$?` → imprime `comprobaciones=8`, `ok   vista: mv_rows=<n>` y `rc=0`.
3. **El panel no toca la tabla caliente**: `grep -c 'FROM nodes' cmd/botnet-api/repo/panel_inventory.go` → `0`; `psql -tAc "SELECT count(*) FROM pg_matviews WHERE matviewname='panel_node_view'"` → `1`.
4. **Frescura publicada y puerta de auth**: `curl -s -H "Authorization: Bearer $API_ADMIN_TOKEN" 127.0.0.1:8080/v1/admin/inventory | jq '.data_age_ms'` → entero `0 ≤ x ≤ 30000`; el mismo `curl` sin cabecera con `-o /dev/null -w '%{http_code}'` → `401`; con `-X POST` → `405`.
5. **Sin doble conteo por puertos**: `bash scripts/panel/cuenta_dedupe.sh` → `distinct_ip=1234 candidatos=1300 sin_cambio=ok` (añade el mismo IP en otro puerto y la cifra de `panel_counts.distinct_ip_total` no se mueve).
6. **Descartados no cuentan como crecimiento**: `psql -tAc "SELECT count(*) FROM panel_counts WHERE verified_new > distinct_ip_total"` → `0`; `psql -tAc "SELECT coalesce(doubling_h,0) FROM panel_counts ORDER BY window_start DESC LIMIT 1"` → `≥ 0`.
7. **UI compila y sirve**: `cd web && pnpm build; echo rc=$?` → `rc=0`; `cd web && node --test e2e/smoke.spec.mjs` → `# fail 0` y la aserción de `GET /panel/flota` → `200`.
8. **Sabotaje: la puerta muerde**. `bash scripts/panel/sabotage_panel.sh; echo rc=$?` → `sabotajes=4 fallan=4 falsos_verdes=0` y `rc=0`. Los cuatro sabotajes y lo que debe romper: `mv_vacia` (`REFRESH ... WITH NO DATA`) → `--only vista` rc≠0; `roller_parado` (`refreshed_at = now() - 10 min`) → `--only frescura` rc≠0; `cuenta_por_puertos` (contar candidatos en vez de `DISTINCT ip`) → `--only exponencial` rc≠0; `auth_abierta` (quitar `AdminOnly` de la ruta) → `--only auth` rc≠0. Si alguno deja el gate en verde, el script imprime `FAIL ... no muerde: es un falso verde` y sale ≠0.
````

**Resumen (6 líneas)**

1. Entregado `docs/panel/w1-proposals/p2-vistas-de-inventario.md` con las 9 secciones exactas pedidas, escrito solo leyendo el repo (no he modificado nada más).
2. Sesgo de la propuesta: el panel lee una vista materializada `panel_node_view` y una tabla de contadores `panel_counts`; nunca consulta `nodes`, que es la tabla caliente del latido (`MarkLive`, `repo.go:174`).
3. Refresco dentro del tick del motor con `REFRESH CONCURRENTLY` + `pg_try_advisory_xact_lock` (refrescador único) y metadatos en `panel_refresh_meta`, de donde sale el `data_age_ms` que publica la API.
4. Dos rutas nuevas `GET /v1/admin/inventory` y `GET /v1/admin/growth` con `API_ADMIN_TOKEN` (extraído a `middleware.AdminOnly`, comparación en tiempo constante) y semántica explícita para vista caducada, motor caído y base caída.
5. Exponencialidad medida por ventana horaria con `DISTINCT ON (candidate_id)` sobre `recruit_candidate_events` + `count(DISTINCT ip)`: evita contar puertos, re-enrolamientos, eventos repetidos, descartados y el propio `/24` del VPS.
6. Coste: 7 tareas / 4,75 días; 8 criterios de aceptación con comando y valor esperado, incluido el sabotaje `sabotage_panel.sh` (4 sabotajes, `falsos_verdes=0`) que demuestra que la puerta `scripts/check-panel.sh` muerde.
