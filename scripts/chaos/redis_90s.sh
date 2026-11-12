#!/usr/bin/env bash
# EP-08 T-0806: caos de Redis 90 s con sesiones vivas (A3-X3/A3-09).
# El script es el cronometro del contrato: corre TestRedis90sChaos con
# la ventana completa de 90 s (Redis parado y vuelto), sobre el
# redis-server portable del host. CHAOS_WINDOW_S=<n> acelera la ventana
# para iterar (no usar para cerrar la task).
#
# Requisitos: DATABASE_URL (Postgres portable EP-01) y redis-server
# portable (REDIS_SERVER_BIN o ~/tools/redis/...).
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null
[[ -n "${DATABASE_URL:-}" ]] || { echo "redis_90s.sh: DATABASE_URL requerido" >&2; exit 2; }
export CHAOS_WINDOW_S="${CHAOS_WINDOW_S:-90}"
echo "redis_90s.sh: ventana de caos ${CHAOS_WINDOW_S} s con sesiones vivas"
go test ./internal/backend/ -run TestRedis90sChaos -count=1 -v -timeout 6m
