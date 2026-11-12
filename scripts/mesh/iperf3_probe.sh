#!/usr/bin/env bash
# EP-07 T-0703: sonda iperf3 por la malla que siembra
# nodes.bandwidth_cap_kbps en [medido x 0.8, medido] (factor 0.9, la
# misma formula que cmd/botnet-api/services/caps.go: CapProbeFactor).
#
# Produccion (malla arriba, iperf3 en bastion y nodo, IP de malla
# 100.64.x.y estable por randomize_client_port=false):
#   NODE_ID=<uuid> MESH_IP=100.64.x.y \
#     DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable \
#     bash scripts/mesh/iperf3_probe.sh
# El servidor iperf3 vive en el NODO (arranca y muere con la sonda):
# el router del dueno no gana ningun puerto escuchante publico; el
# bastion conecta por el tunel WG (T-0702).
#
# Host F4 sin iperf3: doble determinista, el aserto de banda vive aqui:
#   bash scripts/mesh/iperf3_probe.sh --self-test 4096
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

CAP_FACTOR=0.9
CAP_FLOOR=64
CAP_SMALLINT_MAX=32767

# cap_from_probe <kbps medidos> -> tope a escribir (la formula dura la
# fija Go: services.CapFromProbe; aqui la spec del script es la banda
# [0.8x, 1.0x], que es lo que el criterio del task exige).
cap_from_probe() {
  awk -v m="$1" -v f="$CAP_FACTOR" 'BEGIN{
    c = int(m*f);
    if (c < '"$CAP_FLOOR"') c = '"$CAP_FLOOR"';
    if (c > m) c = m;
    if (c > '"$CAP_SMALLINT_MAX"') c = '"$CAP_SMALLINT_MAX"';
    printf "%d", c
  }'
}

assert_band() { # medido cap -> aserta cap in [0.8-medido, medido]
  awk -v m="$1" -v c="$2" 'BEGIN{
    lo = int(m*0.8);
    if (c < lo || c > m) { printf "FUERA %d no in [%d,%d]\n", c, lo, m; exit 1 }
    printf "BANDA OK %d in [%d,%d]\n", c, lo, m
  }'
}

if [[ "${1:-}" == "--self-test" ]]; then
  # Doble sin iperf3: la fixture del task (link de 4 Mbps = 4096 kbps
  # -> tope 3276..4096) y un enlace fino (60 kbps: el piso no puede
  # pasar el medido).
  for m in 4096 2048 5000 60; do
    cap="$(cap_from_probe "$m")"
    assert_band "$m" "$cap"
  done
  echo "iperf3_probe self-test: OK (factor $CAP_FACTOR, banda [0.8x, 1.0x])"
  exit 0
fi

: "${NODE_ID:?NODE_ID (uuid) requerido}"
command -v iperf3 >/dev/null 2>&1 || {
  echo "iperf3_probe: iperf3 no instalado en el bastion; usa --self-test para la formula" >&2
  exit 2
}
MESH_IP="${MESH_IP:-}"
[[ -n "$MESH_IP" ]] || { echo "iperf3_probe: MESH_IP (100.64.x.y) requerido" >&2; exit 2; }
PSQL="${PSQL_BIN:-psql}"
: "${DATABASE_URL:?DATABASE_URL requerido para escribir el tope}"

# 10 s en modo receptor-hacia-el-bastion (el upload del nodo es el
# cuello); -R = receiver-side. bits/segundo -> kbps del receptor.
read -r KBPS < <(iperf3 -c "$MESH_IP" -t 10 -R --json \
  | awk 'match($0, /"bits_per_second"[^0-9]*([0-9.]+)/, m) { print int(m[1]/1000); exit }')
[[ -n "$KBPS" && "$KBPS" -gt 0 ]] || { echo "iperf3_probe: sonda no devolvio ancho" >&2; exit 1; }

CAP="$(cap_from_probe "$KBPS")"
assert_band "$KBPS" "$CAP" >/dev/null

# 20 sesiones de SOCKS pueden estar corriendo: la sonda jamas es una
# de ellas (AC3); el contador botnet_sessions_total no se toca aqui.
"$PSQL" "$DATABASE_URL" -c \
  "UPDATE nodes SET bandwidth_cap_kbps = $CAP::smallint WHERE node_id = '$NODE_ID'::uuid" \
  >/dev/null
echo "iperf3_probe: medido ${KBPS} kbps -> bandwidth_cap_kbps=$CAP (nodo $NODE_ID)"
