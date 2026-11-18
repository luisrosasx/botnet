#!/usr/bin/env bash
# socks5_probe_all.sh — T-2102: una peticion SOCKS5 REAL por nodo.
#
# Recorre la flota que levanto fleet_nodes.sh y, por cada nodo, hace UNA
# peticion de verdad con socks5check: CONNECT SOCKS5 contra el proxy embebido
# del agente, HTTP GET por dentro de ese tunel y lectura de la IP de origen
# que observo el destino. La fila de recruit_socks5_checks la escribe
# recruit.Store.Verificar (T-1804), asi que `ok` solo sale con evidencia dura:
# http_status=200, bytes_in>0, latencia entre 0 y 1500 ms e ip_match contra la
# IP publica del propio nodo. Nada va escrito a mano.
#
# Repite sin duplicate la victoria: el indice unico parcial deja un ganador
# por candidate_id, y los intentos siguientes quedan guardados como evidencia
# con attempt creciente.
#
# Uso:
#   bash scripts/field/socks5_probe_all.sh [--n 20] [--start 2] [--port 1080]
#     [--root /var/lib/botnet-nodes] [--bin /usr/local/bin/socks5check]
#     [--env /etc/botnet-recruit/env] [--dry] [--json] [--help]
#
# Sale 0 si los N nodos tienen su peticion con evidencia dura; 1 si alguno
# fallo o no esta dado de alta; 2 si los argumentos son invalidos; 3 si falta
# la herramienta, la base o el binario de chequeo.
set -uo pipefail

N=20
START=2
PORT=1080
ROOT_NODES=/var/lib/botnet-nodes
BIN=/usr/local/bin/socks5check
ENV_FILE=/etc/botnet-recruit/env
LISTEN=127.0.0.1:0   # :0 = puerto efimero del eco, libre de colisiones entre nodos
DRY=0
AS_JSON=0

usage() {
  cat <<'EOF'
socks5_probe_all.sh — verifica la flota con una peticion SOCKS5 real por nodo.

Uso:
  bash scripts/field/socks5_probe_all.sh [--n 20] [--start 2] [--port 1080]
    [--root /var/lib/botnet-nodes] [--bin /usr/local/bin/socks5check]
    [--listen 127.0.0.1:0] [--dry] [--json] [--help]
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --n) N="${2:-}"; shift 2 ;;
    --start) START="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --root) ROOT_NODES="${2:-}"; shift 2 ;;
    --bin) BIN="${2:-}"; shift 2 ;;
    --env) ENV_FILE="${2:-}"; shift 2 ;;
    --listen) LISTEN="${2:-}"; shift 2 ;;
    --dry) DRY=1; shift ;;
    --json) AS_JSON=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'socks5_probe_all: argumento invalido: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

for v in "$N" "$START" "$PORT"; do
  case "$v" in ''|*[!0-9]*) printf 'socks5_probe_all: esperaba numero, lei "%s"\n' "$v" >&2; exit 2 ;; esac
done

[ "$DRY" -eq 1 ] || {
  command -v psql >/dev/null 2>&1 || { printf 'socks5_probe_all: falta psql\n' >&2; exit 3; }
  [ -x "$BIN" ] || { printf 'socks5_probe_all: falta %s (go build -o %s ./scripts/field/socks5check)\n' "$BIN" "$BIN" >&2; exit 3; }
  [ -r "$ENV_FILE" ] || { printf 'socks5_probe_all: falta %s\n' "$ENV_FILE" >&2; exit 3; }
  # shellcheck disable=SC1090
  set -a; . "$ENV_FILE"; set +a
  DB="${DATABASE_URL:-$BOTNET_DSN}"
  [ -n "$DB" ] || { printf 'socks5_probe_all: %s no trae DATABASE_URL\n' "$ENV_FILE" >&2; exit 3; }
}

LAST=$((START + N - 1))
ok=0
fallos=0
omitidos=""

for i in $(seq "$START" "$LAST"); do
  ip="127.0.0.$i"
  if [ "$DRY" -eq 1 ]; then
    printf '  %s -> %s --node <uuid de %s> --port %s --state-dir %s/%s/state\n' \
      "$ip" "$BIN" "$ip" "$PORT" "$ROOT_NODES" "$i"
    continue
  fi
  node=$(psql "$DB" -X -q -At -c "
    SELECT node_id::text FROM nodes
     WHERE public_ip = '$ip'::inet AND deleted_at IS NULL
     ORDER BY enrolled_at DESC LIMIT 1" 2>/dev/null | tr -d '\r')
  if [ -z "$node" ]; then
    printf 'nodo=%s ip=%s SIN ALTA — se omite\n' "$i" "$ip"
    omitidos="$omitidos $i"
    fallos=$((fallos + 1))
    continue
  fi
  if "$BIN" --node "$node" --port "$PORT" --listen "$LISTEN" --state-dir "$ROOT_NODES/$i/state"; then
    ok=$((ok + 1))
  else
    fallos=$((fallos + 1))
  fi
done

[ "$DRY" -eq 1 ] && { printf 'sondeo_en_seco=si\n'; exit 0; }

# Lectura del estado de la base, no del eco: lo que cuenta es la fila ganadora
# con la IP de salida del propio nodo.
ganadoras=$(psql "$DB" -X -q -At -c "
  SELECT count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id)
   WHERE s.ok AND s.ip_match AND s.egress_ip = n.public_ip AND s.egress_ip <> '74.208.94.91'::inet" 2>/dev/null | tr -d '\r')
distintos=$(psql "$DB" -X -q -At -c "
  SELECT count(DISTINCT s.node_id) FROM recruit_socks5_checks s JOIN nodes n USING (node_id)
   WHERE s.ok AND s.ip_match AND s.egress_ip = n.public_ip AND s.egress_ip <> '74.208.94.91'::inet" 2>/dev/null | tr -d '\r')

printf 'verificados=%s/%s\n' "$ok" "$N"
printf 'filas_ganadoras=%s\n' "${ganadoras:-0}"
printf 'nodos_distintos_verificados=%s\n' "${distintos:-0}"
[ -n "$omitidos" ] && printf 'sin_alta=%s\n' "$omitidos"
if [ "$AS_JSON" -eq 1 ]; then
  printf '{"verificados":%s,"esperado":%s,"ganadoras":%s,"distintos":%s}\n' \
    "$ok" "$N" "${ganadoras:-0}" "${distintos:-0}"
fi

if [ "$fallos" -eq 0 ]; then
  exit 0
fi
printf 'socks5_probe_all: %s de %s nodos sin peticion SOCKS5 con evidencia dura\n' "$fallos" "$N" >&2
exit 1
