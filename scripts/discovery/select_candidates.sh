#!/usr/bin/env bash
# T-0106 — consulta de seleccion de candidatos que respeta opt-outs:
# excluye IPs cuyo node tiene optout_at < 12 meses o un leave con
# reprobe_blocked_until futuro. LIMIT por entorno o --limit.
#
#   LIMIT=100 DATABASE_URL=postgres://... scripts/discovery/select_candidates.sh
# Salida: CSV ip,port,service,asn,country (sin cabecera, para pipes).
# Sin nodes/leaves en la BD (EP-08 no ha corrido), la seleccion corre
# el equivalente sin anti-join (mismo indice parcial).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

limit="${LIMIT:-100}"
for a in "$@"; do
  case "$a" in
    LIMIT=*)   limit="${a#LIMIT=}" ;;
    --limit=*) limit="${a#--limit=}" ;;
    *) echo "select_candidates: opcion desconocida $a" >&2; exit 2 ;;
  esac
done

[[ -n "${DATABASE_URL:-}" ]] || { echo "select_candidates: DATABASE_URL requerido" >&2; exit 2; }
go run ./cmd/botnet-discover select --limit="$limit" --db="$DATABASE_URL"
