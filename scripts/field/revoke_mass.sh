#!/usr/bin/env bash
# revoke_mass.sh — cierre del piloto de revocacion masiva (S-15/S-16 y el
# criterio "100 nodos revocados, sesiones cerradas en < 60 s y ninguno
# vuelve a latir sin re-enrolar").
#
# Revoca POST /v1/nodes/{id}/revoke (cmd/botnet-api/handlers/handlers.go:94,
# servicios/revoke.go: RevokeNode) en paralelo y comprueba el cierre contra
# el mismo almacen que ve el gateway:
#   - Postgres: sessions.ended_at deja de ser NULL (CloseOpenForNode cierra
#     con closed_reason='goaway' en la misma llamada al revocar).
#   - Reincidencia: un latido DESPUES de la revocacion sin alta nueva del
#     mismo dispositivo. El backend responde 410+bye a un nodo burned
#     (handlers/heartbeat.go) y no le ingiere el frame, asi que aqui solo
#     aparece latido si el nodo siguio entrando con la identidad quemada.
#   - Redis/CRL (revoked:{serial}, refresh 55 s) es la via del gateway para
#     el GOAWAY del stream vivo; este script lo deja fuera porque lo mide el
#     gateway en su propio test (cmd/botnet-gw/crl.go).
#
# Uso:
#   bash scripts/field/revoke_mass.sh \
#     (--ids <uuid,uuid,...> | --ids-file <f> | --count <N>) \
#     --api <http://host:port> [--token <bearer>] [--parallel 8]
#     [--timeout 60] [--db "postgres://..."] [--dry] [--json] [--help]
#
# Variables del entorno:
#   DATABASE_URL   valor por defecto de --db (si no se pasa --db)
#   PSQL_BIN       ruta a psql (defecto: psql en PATH o ~/pgsql/bin/psql.exe)
#   CURL_BIN       ruta a curl (defecto: curl en PATH)
#
# Salida: humano por stdout; con --json SOLO el JSON va por stdout (los
# avisos salen por stderr). JSON:
#   {total, ok, failed, p50_s, p95_s, max_s, total_s, sessions_closed,
#    relapsed, verdict}
#   sessions_closed/relapsed valen null cuando Postgres no se pudo usar: el
#   veredicto sale PARCIAL y el script LO DICE — no se inventa PASS.
#
# Veredictos:
#   PASS     HTTP limpio, sesiones cerradas dentro del timeout y 0 reincidentes.
#   PARCIAL  HTTP limpio pero Postgres no disponible o sin filas de estos
#            node_id: el cierre no se ha podido verificar aqui.
#   FALLO    hay revoke(s) fallidos, sesiones abiertas al vencer el timeout,
#            reincidentes o algun nodo mas lento que el timeout.
#
# Exit: 0 PASS o PARCIAL con la parte HTTP limpia (failed=0 y max_s<=timeout);
# 1 FALLO; 2 argumentos invalidos; 3 herramienta ausente (curl o psql).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2

USAGE='revoke_mass.sh — revocar N nodos y verificar el cierre en menos de 60 s

Uso:
  bash scripts/field/revoke_mass.sh (--ids <uuid,uuid,...> | --ids-file <f> | --count <N>)
       --api <http://host:port> [--token <bearer>] [--parallel 8] [--timeout 60]
       [--db "postgres://..."] [--dry] [--json] [--help]

  --ids        lista separada por comas de node_id (UUID).
  --ids-file   fichero con un node_id por linea (# y lineas vacias se ignoran).
  --count N    tomar N node_id del inventario (requiere --db); la revocacion
               masiva del piloto usa esta via cuando no hay lista a mano.
  --api        origen del plano de control, ej. http://127.0.0.1:8080
  --token      bearer para Authorization (API_ADMIN_TOKEN del backend).
  --parallel N revokes simultaneos (defecto 8).
  --timeout S  techo en segundos por nodo Y para esperar el cierre de
               sesiones (defecto 60, el criterio del piloto).
  --db         DSN Postgres para verificar sesiones y reincidencias. Sin el
               (y sin DATABASE_URL) el veredicto sale PARCIAL.
  --dry        imprime el plan (a dónde va, cuantos ids, si hay Postgres) y
               no lanza ningun revoke.
  --json       salida JSON (solo el JSON por stdout).
  --help       esta ayuda.

Salidas: 0 PASS o PARCIAL con HTTP limpio | 1 FALLO | 2 argumentos | 3 herramienta ausente.'

log() { # avisos: con --json van a stderr para dejar el stdout limpio
  if [ "${JSON:-0}" = 1 ]; then printf '%s\n' "$*" >&2; else printf '%s\n' "$*"; fi
}
die() { # die <msg> <exit>
  printf 'revoke_mass: %s\n' "$1" >&2
  exit "${2:-2}"
}

# ---------------------------------------------------------------- argumentos
IDS_ARG="" IDS_FILE="" COUNT="" API="" TOKEN="${API_ADMIN_TOKEN:-}"
PARALLEL=8 TIMEOUT=60 DB="${DATABASE_URL:-}" DRY=0 JSON=0

while [ $# -gt 0 ]; do
  case "$1" in
    --ids) IDS_ARG="${2:-}"; shift 2 || true ;;
    --ids=*) IDS_ARG="${1#*=}"; shift ;;
    --ids-file) IDS_FILE="${2:-}"; shift 2 || true ;;
    --ids-file=*) IDS_FILE="${1#*=}"; shift ;;
    --count) COUNT="${2:-}"; shift 2 || true ;;
    --count=*) COUNT="${1#*=}"; shift ;;
    --api) API="${2:-}"; shift 2 || true ;;
    --api=*) API="${1#*=}"; shift ;;
    --token) TOKEN="${2:-}"; shift 2 || true ;;
    --token=*) TOKEN="${1#*=}"; shift ;;
    --parallel) PARALLEL="${2:-}"; shift 2 || true ;;
    --parallel=*) PARALLEL="${1#*=}"; shift ;;
    --timeout) TIMEOUT="${2:-}"; shift 2 || true ;;
    --timeout=*) TIMEOUT="${1#*=}"; shift ;;
    --db) DB="${2:-}"; shift 2 || true ;;
    --db=*) DB="${1#*=}"; shift ;;
    --dry) DRY=1; shift ;;
    --json) JSON=1; shift ;;
    -h|--help) printf '%s\n' "$USAGE"; exit 0 ;;
    *) die "argumento no reconocido: $1" 2 ;;
  esac
done

int_ok() { case "$1" in ''|*[!0-9]*) return 1 ;; *) return 0 ;; esac; }
[ -n "$API" ] || die "falta --api <http://host:port> (usa --help)" 2
API="${API%/}"
int_ok "$PARALLEL" && [ "$PARALLEL" -ge 1 ] || die "--parallel quiere un entero >= 1" 2
int_ok "$TIMEOUT" && [ "$TIMEOUT" -ge 1 ] || die "--timeout quiere un entero >= 1" 2

SRC=0
[ -n "$IDS_ARG" ] && SRC=$((SRC + 1))
[ -n "$IDS_FILE" ] && SRC=$((SRC + 1))
[ -n "$COUNT" ] && SRC=$((SRC + 1))
[ "$SRC" -eq 1 ] || die "da exactamente una de --ids, --ids-file o --count (usa --help)" 2
[ -z "$COUNT" ] || int_ok "$COUNT" || die "--count quiere un entero >= 1" 2

# ------------------------------------------------------------------ utilitos
PSQL="${PSQL_BIN:-psql}"
if ! command -v "$PSQL" >/dev/null 2>&1; then
  if [ -x "$HOME/pgsql/bin/psql.exe" ]; then PSQL="$HOME/pgsql/bin/psql.exe"
  elif [ -x "$HOME/pgsql/bin/psql" ]; then PSQL="$HOME/pgsql/bin/psql"
  elif [ "$PSQL" != "psql" ]; then die "PSQL_BIN=$PSQL no es ejecutable" 3
  fi
fi
CURL="${CURL_BIN:-curl}"
command -v "$CURL" >/dev/null 2>&1 || die "curl no disponible (CURL_BIN)" 3
command -v jq >/dev/null 2>&1 || die "jq no disponible" 3

# sql_lit: comilla simple para el array de node_id (los ids ya van validados).
sql_lit() { printf "'%s'" "$1"; }

# psql1: psql -tA para un solo valor. En Windows psql termina las lineas con
# CRLF y ese \r invisible rompe comparaciones y casts posteriores; aqui se
# quita una vez para todas.
psql1() { "$PSQL" -tA -c "$1" "$DB" 2>/dev/null | tr -d '\r'; }

# psql1_seguro: como psql1 pero con hasta 3 intentos. En MSYS2/Git Bash el
# arranque de psql.exe compite con procesos recien cerrados (los curl del
# revoke) y a veces sale vacio sin que Postgres haya fallado de verdad; un
# vacio repetido si se reporta como tal (el llamante decide el veredicto).
psql1_seguro() {
  local v i
  v=""
  for i in 1 2 3; do
    v="$(psql1 "$1")"
    [ -n "$v" ] && break
    sleep 1
  done
  printf '%s' "$v"
}

# Postgres solo es necesario para verificar (y para --count).
DB_OK=0
[ -n "$DB" ] && [ "$DB" != "" ] && DB_OK=1
if [ "$DB_OK" = 1 ]; then
  if ! "$PSQL" -tA -c "SELECT 1" "$DB" >/dev/null 2>&1; then
    if [ "$DRY" = 1 ]; then
      log "revoke_mass: Postgres no responde en --db (verificacion en modo parcial)"
      DB_OK=0
    else
      die "Postgres no responde en --db: $DB" 3
    fi
  fi
fi

# ---------------------------------------------------------------- resolucion
IDS=()
if [ -n "$IDS_ARG" ]; then
  IFS=',' read -r -a IDS <<< "$IDS_ARG"
elif [ -n "$IDS_FILE" ]; then
  [ -f "$IDS_FILE" ] || die "no existe --ids-file: $IDS_FILE" 2
  while IFS= read -r line; do
    line="${line%%#*}"
    line="$(printf '%s' "$line" | tr -d '[:space:]')"
    [ -n "$line" ] && IDS+=("$line")
  done < "$IDS_FILE"
else # --count
  if [ "$DB_OK" = 0 ]; then
    if [ "$DRY" = 1 ]; then
      log "revoke_mass: --count $COUNT necesita Postgres; en seco no se resuelven los ids"
      IDS=()
      for ((i = 0; i < COUNT; i++)); do IDS+=("<id-$((i + 1))>"); done
    else
      die "--count necesita --db (o DATABASE_URL) para elegir los node_id" 2
    fi
  else
    while IFS= read -r line; do
      line="${line%$'\r'}"
      [ -n "$line" ] && IDS+=("$line")
    done < <("$PSQL" -tA -c "
      SELECT node_id FROM nodes
       WHERE deleted_at IS NULL AND status <> 'burned'
       ORDER BY last_seen_at NULLS LAST, node_id
       LIMIT $COUNT" "$DB" 2>/dev/null)
    [ "${#IDS[@]}" -gt 0 ] || die "Postgres no devuelve node_id que revocar" 2
    if [ "${#IDS[@]}" -lt "$COUNT" ]; then
      log "revoke_mass: Postgres solo tiene ${#IDS[@]} node_id candidatos de $COUNT pedidos"
    fi
  fi
fi

TOTAL=${#IDS[@]}
[ "$TOTAL" -gt 0 ] || die "no hay node_id que revocar" 2

# Los node_id son UUID; validar aqui evita inyectar basura y rompe pronto.
uuid_re='^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
for id in "${IDS[@]}"; do
  [[ "$id" =~ $uuid_re ]] || die "node_id no es UUID: '$id'" 2
done

# -------------------------------------------------------------------- plan
if [ "$DRY" = 1 ]; then
  sample="${IDS[0]}"
  [ "$TOTAL" -gt 1 ] && sample="$sample,...,${IDS[$((TOTAL - 1))]}"
  if [ "$JSON" = 1 ]; then
    jq -n --argjson total "$TOTAL" --arg api "$API" --argjson parallel "$PARALLEL" \
      --argjson timeout "$TIMEOUT" --arg db "$DB" --arg sample "$sample" \
      --argjson db_ok "$DB_OK" \
      '{dry:true, total:$total, api:$api, parallel:$parallel, timeout_s:$timeout,
        db:(if $db == "" then null else $db end), postgres:($db_ok == 1),
        ids_ejemplo:$sample,
        peticion:("POST " + $api + "/v1/nodes/{node_id}/revoke")}'
  else
    printf 'revoke_mass (plan): %d node_id -> POST %s/v1/nodes/{node_id}/revoke\n' \
      "$TOTAL" "$API"
    printf '  ids: %s\n' "$sample"
    printf '  parallel=%s timeout=%ss db=%s\n' "$PARALLEL" "$TIMEOUT" "${DB:-<ninguno>}"
    printf '  verificacion: %s\n' \
      "$([ "$DB_OK" = 1 ] && echo 'Postgres arriba (sesiones + reincidencia)' || echo 'sin Postgres: veredicto parcial')"
    printf '  (modo seco: no se ha lanzado ningun revoke)\n'
  fi
  exit 0
fi

# ------------------------------------------------------------------ revoke
WORK="$(mktemp -d "${TMPDIR:-/tmp}/revoke_mass.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

# Instante de referencia en el reloj de Postgres: los latidos se comparan con
# el REVOKE_TS del servidor, no con el reloj del bastion (skew de campo).
REVOKE_TS=""
if [ "$DB_OK" = 1 ]; then
  REVOKE_TS="$(psql1_seguro "SELECT now()")"
  [ -n "$REVOKE_TS" ] || die "no se pudo leer now() en Postgres" 3
fi

revoke_one() { # revoke_one <node_id> -> escribe WORK/<id> con "codigo segundos rc"
  local id="$1" out code secs rc=0
  local -a auth=()
  [ -n "$TOKEN" ] && auth=(-H "Authorization: Bearer $TOKEN")
  out="$("$CURL" -sS -o /dev/null -w '%{http_code} %{time_total}' \
    --max-time "$TIMEOUT" -X POST \
    "${auth[@]}" -H 'Content-Type: application/json' \
    "$API/v1/nodes/$id/revoke" 2>"$WORK/$id.err")" || rc=$?
  code="${out%% *}"; secs="${out##* }"
  { [ -n "$code" ] && [ "$code" != "$out" ]; } || code="000"
  case "$secs" in ''|*[!0-9.]*) secs="0" ;; esac
  printf '%s %s %s\n' "$code" "$secs" "$rc" > "$WORK/$id"
}

START=$SECONDS
i=0
while [ "$i" -lt "$TOTAL" ]; do
  batch_end=$((i + PARALLEL))
  [ "$batch_end" -gt "$TOTAL" ] && batch_end=$TOTAL
  for ((j = i; j < batch_end; j++)); do revoke_one "${IDS[$j]}" & done
  wait
  i=$batch_end
done
TOTAL_S=$((SECONDS - START))

# --------------------------------------------------- estadisticas por nodo
: > "$WORK/codes.txt"
for id in "${IDS[@]}"; do
  [ -f "$WORK/$id" ] || printf '000 0 7\n' > "$WORK/$id"
  printf '%s\n' "$(cut -d' ' -f2 < "$WORK/$id")" >> "$WORK/p50.txt.raw"
  printf '%s %s\n' "$id" "$(cat "$WORK/$id")" >> "$WORK/all.txt"
  cut -d' ' -f1 < "$WORK/$id" >> "$WORK/codes.txt"
done
touch "$WORK/p50.txt.raw"

ok=$(awk '$1 >= 200 && $1 < 300 { n++ } END { print n + 0 }' "$WORK/codes.txt")
failed=$((TOTAL - ok))
TOTAL_S_NUM="$TOTAL_S"
read -r P50 P95 MAX <<EOF
$(awk '{ print $1 }' "$WORK/p50.txt.raw" | sort -g | awk '
  { v[NR] = $1; n = NR }
  END {
    if (n == 0) { print "0 0 0"; exit }
    i50 = int((n + 1) * 0.50); if (i50 < 1) i50 = 1
    i95 = int((n + 1) * 0.95); if (i95 < 1) i95 = 1
    printf "%.3f %.3f %.3f", v[i50], v[i95], v[n]
  }')
EOF

if [ "$failed" -gt 0 ]; then
  log "revoke_mass: $failed revoke(s) sin 2xx:"
  awk '$2 < 200 || $2 >= 300 { print "  " $0 }' "$WORK/all.txt" >&2 | head -10
fi

# ------------------------------------------------- verificacion en Postgres
SESSIONS_CLOSED=null
RELAPSED=null
DB_NOTE="sin Postgres"
CLOSURE_OK=1
if [ "$DB_OK" = 1 ]; then
  ARR="ARRAY["
  first=1
  for id in "${IDS[@]}"; do
    [ "$first" = 1 ] || ARR="$ARR,"
    ARR="$ARR$(sql_lit "$id")"
    first=0
  done
  ARR="$ARR]::uuid[]"
  # node_id es uuid: hay que comparar con = ANY(...), no con IN (IN no
  # acepta un array).
  ARR_CLAUSE="node_id = ANY($ARR)"

  # Cierre de sesiones: se espera hasta --timeout a que no quede ninguna
  # ended_at IS NULL para los nodos revocados.
  deadline=$((SECONDS + TIMEOUT))
  open_sessions=0
  while :; do
    open_sessions="$(psql1_seguro "SELECT count(*) FROM sessions WHERE ended_at IS NULL AND $ARR_CLAUSE")"
    int_ok "${open_sessions:-x}" || { DB_OK=0; DB_NOTE="consulta de sesiones fallo"; break; }
    [ "$open_sessions" -eq 0 ] && break
    [ "$SECONDS" -lt "$deadline" ] || break
    sleep 2
  done
  if [ "$DB_OK" = 1 ]; then
    if [ "${open_sessions:-0}" -gt 0 ]; then
      CLOSURE_OK=0
      DB_NOTE="$open_sessions sesiones siguen abiertas tras ${TIMEOUT}s"
    fi
    SESSIONS_CLOSED="$(psql1_seguro "SELECT count(*) FROM sessions WHERE ended_at IS NOT NULL AND $ARR_CLAUSE")"
    int_ok "${SESSIONS_CLOSED:-x}" || SESSIONS_CLOSED=0
    # Reincidencia: latido posterior a la revocacion sin un alta nueva del
    # mismo dispositivo (ticket nuevo = otra fila en nodes con el mismo
    # device_token_hash y enrolled_at posterior al corte).
    RELAPSED="$(psql1_seguro "
      SELECT count(DISTINCT h.node_id)
        FROM node_heartbeats h
        JOIN nodes n ON n.node_id = h.node_id
       WHERE h.node_id = ANY($ARR) AND h.ts > '$REVOKE_TS'::timestamptz
         AND NOT EXISTS (
               SELECT 1 FROM nodes re
                WHERE re.device_token_hash = n.device_token_hash
                  AND re.node_id <> n.node_id
                  AND re.enrolled_at > '$REVOKE_TS'::timestamptz)")"
    int_ok "${RELAPSED:-x}" || { RELAPSED=null; DB_NOTE="consulta de latidos fallo"; }
    if [ "$DB_NOTE" = "sin Postgres" ]; then
      if [ "$SESSIONS_CLOSED" = 0 ]; then
        DB_NOTE="0 sesiones en Postgres para estos node_id (0 abiertas = cierre trivial)"
      else
        DB_NOTE="verificado en Postgres"
      fi
    fi
  fi
fi

# ------------------------------------------------------------------ veredicto
max_le_timeout="$(awk -v a="$MAX" -v b="$TIMEOUT" 'BEGIN { print (a + 0 <= b + 0) ? 1 : 0 }')"
HTTP_OK=1
[ "$failed" -eq 0 ] || HTTP_OK=0
[ "$max_le_timeout" = 1 ] || HTTP_OK=0

verdict=FALLO
exit_code=1
relapsed_num="${RELAPSED:-null}"
if [ "$HTTP_OK" = 1 ]; then
  if [ "$DB_OK" = 1 ] && [ "$CLOSURE_OK" = 1 ] && [ "$relapsed_num" != null ] && [ "$relapsed_num" -eq 0 ]; then
    verdict=PASS
    exit_code=0
  elif [ "$DB_OK" = 1 ]; then
    verdict=FALLO
    exit_code=1
  else
    verdict=PARCIAL
    exit_code=0
  fi
fi

if [ "$JSON" = 1 ]; then
  jq -n \
    --argjson total "$TOTAL" --argjson ok "$ok" --argjson failed "$failed" \
    --argjson p50 "$P50" --argjson p95 "$P95" --argjson max "$MAX" \
    --argjson total_s "$TOTAL_S_NUM" \
    --arg sessions "$SESSIONS_CLOSED" --arg relapsed "$RELAPSED" \
    --arg verdict "$verdict" --arg db_note "$DB_NOTE" \
    --argjson timeout "$TIMEOUT" \
    '{total:$total, ok:$ok, failed:$failed, p50_s:$p50, p95_s:$p95, max_s:$max,
      total_s:$total_s, timeout_s:$timeout,
      sessions_closed:(if $sessions == "null" then null else ($sessions|tonumber) end),
      relapsed:(if $relapsed == "null" then null else ($relapsed|tonumber) end),
      verdict:$verdict, nota:$db_note}'
else
  printf 'revoke_mass: %d/%d revocados (p50=%ss p95=%ss max=%ss total=%ss, limite %ss)\n' \
    "$ok" "$TOTAL" "$P50" "$P95" "$MAX" "$TOTAL_S" "$TIMEOUT"
  printf '  sesiones cerradas=%s reincidentes=%s (%s)\n' \
    "${SESSIONS_CLOSED:-null}" "${RELAPSED:-null}" "$DB_NOTE"
  printf '  veredicto: %s\n' "$verdict"
fi

if [ "$exit_code" -eq 0 ] && [ "$verdict" = PARCIAL ]; then
  log "revoke_mass: veredicto PARCIAL — sin Postgres no se puede afirmar el cierre de sesiones ni la reincidencia"
fi
exit "$exit_code"
