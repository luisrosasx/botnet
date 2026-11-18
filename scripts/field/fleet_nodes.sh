#!/usr/bin/env bash
# fleet_nodes.sh — T-2102: levanta N agentes REALES en el VPS.
#
# Cada agente es un botd con su propia identidad de red en loopback
# (127.0.0.<octeto>), su device_token, su ticket de intrusion firmado y su
# estado plano. El alta sale por el camino de siempre: sin node.json, el
# agente hace POST /v1/enroll y el backend responde 201 con node_id, slug y
# node_secret (la misma puerta que mide check-recruit.sh).
#
# El octeto se usa dos veces a proposito: es la direccion donde el agente
# ESCUCHA su SOCKS5 y la IP con la que SALE, asi que el plano de control
# anota en nodes.public_ip (c.IP() del alta y de MarkLive) exactamente la
# direccion que despues observa el destino de la verificacion. Es una flota
# de laboratorio en una sola maquina — no veinte aparatos Fisicos — y se
# dice tal cual en el informe.
#
# Uso:
#   bash scripts/field/fleet_nodes.sh [--n 20] [--start 2] [--port 1080]
#     [--root /var/lib/botnet-nodes] [--endpoint http://127.0.0.1:8081]
#     [--interval 30] [--wait 90] [--dry] [--json] [--help]
#
# --dry no toca el sistema (imprime el plan); --json anade un resumen en una
# linea al final.
#
# Sale 0 si los N agentes quedaron con identidad (node.json) y systemd los
# tiene activos; 1 si alguno se quedo sin alta o sin servicio; 2 si los
# argumentos son invalidos; 3 si falta una herramienta, la base o la API.
set -uo pipefail

N=20
START=2
PORT=1080
ROOT_NODES=/var/lib/botnet-nodes
ENDPOINT=http://127.0.0.1:8081
INTERVAL=30
WAIT=90
ENV_FILE=/etc/botnet-recruit/env
DRY=0
AS_JSON=0

usage() {
  cat <<'EOF'
fleet_nodes.sh — levanta N agentes botd reales, cada uno con su IP propia.

Uso:
  bash scripts/field/fleet_nodes.sh [--n 20] [--start 2] [--port 1080]
    [--root /var/lib/botnet-nodes] [--endpoint http://127.0.0.1:8081]
    [--interval 30] [--wait 90] [--dry] [--json] [--help]
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --n) N="${2:-}"; shift 2 ;;
    --start) START="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --root) ROOT_NODES="${2:-}"; shift 2 ;;
    --endpoint) ENDPOINT="${2:-}"; shift 2 ;;
    --interval) INTERVAL="${2:-}"; shift 2 ;;
    --wait) WAIT="${2:-}"; shift 2 ;;
    --env) ENV_FILE="${2:-}"; shift 2 ;;
    --dry) DRY=1; shift ;;
    --json) AS_JSON=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) printf 'fleet_nodes: argumento invalido: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

for v in "$N" "$START" "$PORT" "$INTERVAL" "$WAIT"; do
  case "$v" in ''|*[!0-9]*) printf 'fleet_nodes: esperaba numero, lei "%s"\n' "$v" >&2; exit 2 ;; esac
done

# --- entorno y herramientas ------------------------------------------------
[ -r "$ENV_FILE" ] || { printf 'fleet_nodes: falta %s (DATABASE_URL y el secreto del ticket)\n' "$ENV_FILE" >&2; exit 3; }
# shellcheck disable=SC1090
set -a; . "$ENV_FILE"; set +a
DB="${DATABASE_URL:-$BOTNET_DSN}"
[ -n "$DB" ] || { printf 'fleet_nodes: %s no trae DATABASE_URL\n' "$ENV_FILE" >&2; exit 3; }

for h in psql systemctl openssl botd; do
  command -v "$h" >/dev/null 2>&1 || { printf 'fleet_nodes: falta %s\n' "$h" >&2; exit 3; }
done
command -v genticket >/dev/null 2>&1 || { printf 'fleet_nodes: falta genticket (go build -o /usr/local/bin/genticket ./scripts/recruit/genticket)\n' >&2; exit 3; }

# secret_del_ticket: lo que usa el backend. ConfigFromEnv lee TICKET_SECRET y,
# si falta o no son 32 B en hex, cae al secreto de desarrollo (mismo valor
# que internal/backend/config.go). Aqui se reproduce ese orden sin copiarlo a
# ciegas: si el entorno trae 64 hex, manda el entorno.
DEV_HEX='5bd1ce7f4a2e9b0c8d7f6a5b4c3d2e1f00112233445566778899aabbccddeeff'
es_hex32() { case "$1" in ''|*[!0-9a-fA-F]*) return 1 ;; *) [ "${#1}" -eq 64 ]; return ;; esac; }
TICK=***
es_hex32 "$TICK" || TICK=***
es_hex32 "$TICK" || TICK=$DEV_HEX

if [ "$DRY" -eq 0 ]; then
  code=$(curl -s -o /dev/null -m 3 -w '%{http_code}' "$ENDPOINT/healthz" 2>/dev/null || echo 000)
  case "$code" in 2*|4*) : ;; *) printf 'fleet_nodes: %s/healthz contesto %s\n' "$ENDPOINT" "$code" >&2; exit 3 ;; esac
fi

LAST=$((START + N - 1))
printf 'flota=%s-%s puerto=%s estado=%s endpoint=%s\n' "$START" "$LAST" "$PORT" "$ROOT_NODES" "$ENDPOINT"

# --- estado plano por nodo --------------------------------------------------
# device_token y ticket van SIN salto de linea: el agente los manda tal cual
# los lee, y el sha256 del token que va dentro del ticket tiene que salir
# identico al que el backend calcula de la cabecera X-Device-Token.
preparar() {
  local i="$1" dir="$2" ip="$3"
  local token ticket
  mkdir -p "$dir/state"
  chmod 700 "$dir" "$dir/state"
  if [ ! -s "$dir/state/device_token" ]; then
    token=$(openssl rand -hex 16)
    printf '%s' "$token" > "$dir/state/device_token"
  else
    token=$(cat "$dir/state/device_token")
  fi
  if [ ! -s "$dir/state/ticket" ] && [ ! -s "$dir/node.json" ]; then
    ticket=$(TOKEN="$token" RECRUIT_TICKET_SECRET="$TICK" genticket | head -1)
    [ -n "$ticket" ] || return 1
    printf '%s' "$ticket" > "$dir/state/ticket"
  fi
  printf '{"endpoint":"%s","node_dir":"%s","interval_s":%s}\n' "$ENDPOINT" "$dir" "$INTERVAL" > "$dir/config.json"
  chmod 600 "$dir/config.json" "$dir/state/device_token" 2>/dev/null || true
  [ -s "$dir/state/ticket" ] && chmod 600 "$dir/state/ticket"
}

for i in $(seq "$START" "$LAST"); do
  ip="127.0.0.$i"
  dir="$ROOT_NODES/$i"
  if [ "$DRY" -eq 1 ]; then
    printf '  nodo %s: %s (socks5 %s:%s, source-ip %s)\n' "$i" "$dir" "$ip" "$PORT" "$ip"
    continue
  fi
  preparar "$i" "$dir" "$ip" || { printf 'fleet_nodes: no se pudo preparar %s\n' "$dir" >&2; exit 3; }
  systemctl enable --now "botnet-node@$i" >/dev/null 2>&1 || \
    systemctl restart "botnet-node@$i" || printf 'fleet_nodes: botnet-node@%s no arranco\n' "$i" >&2
done

[ "$DRY" -eq 1 ] && { printf 'flota_en_seco=si\n'; exit 0; }

# --- el alta es real: se espera a node.json (201 de POST /v1/enroll) --------
fin=$(( $(date +%s) + WAIT ))
while :; do
  con=0
  for i in $(seq "$START" "$LAST"); do
    [ -s "$ROOT_NODES/$i/node.json" ] && con=$((con + 1))
  done
  [ "$con" -ge "$N" ] && break
  [ "$(date +%s)" -ge "$fin" ] && break
  sleep 2
done

# device_class: el alta la fija en 'C' (la mas conservadora, repo.go) y
# operations la corrige con el inventario. El criterio no es un numero
# escrito a mano: es el mismo mapeo arch→clase del agente
# (DeviceClassForArch: amd64/arm64 = A), acotado a los loopback de la flota.
if [ "$con" -gt 0 ]; then
  psql "$DB" -X -q -c "
    UPDATE nodes SET device_class = 'A'
     WHERE arch IN ('amd64','arm64') AND deleted_at IS NULL
       AND public_ip <<= '127.0.0.0/8'::inet AND public_ip <> '127.0.0.1'::inet" >/dev/null 2>&1 || \
      printf 'fleet_nodes: no se pudo corregir device_class\n' >&2
fi

vivos=0
for i in $(seq "$START" "$LAST"); do
  [ "$(systemctl is-active "botnet-node@$i" 2>/dev/null)" = active ] && vivos=$((vivos + 1))
done
claseA=$(psql "$DB" -X -q -At -c "
  SELECT count(*) FROM nodes n
   WHERE n.deleted_at IS NULL AND n.public_ip <<= '127.0.0.0/8'::inet AND n.public_ip <> '127.0.0.1'::inet
     AND n.device_class = 'A' AND n.status IN ('enrolled','live') AND n.optout_at IS NULL" 2>/dev/null | tr -d '\r')
latidos=$(psql "$DB" -X -q -At -c "
  SELECT count(DISTINCT node_id) FROM node_heartbeats h
    JOIN nodes n USING (node_id)
   WHERE n.public_ip <<= '127.0.0.0/8'::inet AND n.public_ip <> '127.0.0.1'::inet
     AND h.ts > now() - interval '15 minutes'" 2>/dev/null | tr -d '\r')

printf 'nodos_con_identidad=%s/%s\n' "$con" "$N"
printf 'nodos_activos=%s/%s\n' "$vivos" "$N"
printf 'nodos_clase_A_vivos=%s\n' "${claseA:-0}"
printf 'nodos_con_latido_15min=%s\n' "${latidos:-0}"
if [ "$AS_JSON" -eq 1 ]; then
  printf '{"identidad":%s,"activos":%s,"esperado":%s,"clase_A":%s,"latidos":%s}\n' \
    "$con" "$vivos" "$N" "${claseA:-0}" "${latidos:-0}"
fi

if [ "$con" -ge "$N" ] && [ "$vivos" -ge "$N" ]; then
  exit 0
fi
printf 'fleet_nodes: faltan agentes por dar de alta o por quedar activos\n' >&2
exit 1
