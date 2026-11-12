#!/usr/bin/env bash
# stickiness.sh — T-0904 (A2-08): sticky y rotativo son modos
# contractuales con umbrales medidos.
#   scripts/gw/stickiness.sh --mode=sticky --n=50 --ttl=300
#     → mismo node_id en >= 95 % de los n requests (TTL de la clave).
#   scripts/gw/stickiness.sh --mode=rotating --n=50
#     -> >= 80 % node_id distintos y reparto ponderado por bw_mbps.
# El umbral lo aserta go test (TestStickinessThresholds); el script solo
# traduce los flags a GW_STICKY_* y propaga la salida. Salida 0 = verde.
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

MODE=sticky
N=50
TTL=300
for arg in "$@"; do
  case "$arg" in
    --mode=*) MODE="${arg#*=}" ;;
    --n=*)    N="${arg#*=}" ;;
    --ttl=*)  TTL="${arg#*=}" ;;
    *) echo "stickiness: argumento desconocido: $arg" >&2; exit 2 ;;
  esac
done

case "$MODE" in
  sticky|rotating) ;;
  *) echo "stickiness: --mode debe ser sticky|rotating" >&2; exit 2 ;;
esac
[ "$N" -gt 0 ] 2>/dev/null || { echo "stickiness: --n entero > 0" >&2; exit 2; }
[ "$TTL" -gt 0 ] 2>/dev/null || { echo "stickiness: --ttl entero > 0" >&2; exit 2; }

export GW_STICKY_MODE="$MODE" GW_STICKY_N="$N" GW_STICKY_TTL="$TTL"
go test ./cmd/botnet-gw/ -run 'TestStickinessThresholds' -count=1 -v
