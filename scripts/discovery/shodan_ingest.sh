#!/usr/bin/env bash
# T-0102 — ingesta por lotes de la API Shodan con upsert a discovered.
#
# Modos:
#   FIXTURE=testdata/shodan_1k.json  doble determinista (sin red)
#   SHODAN_KEY=... [SHODAN_API_BASE=...] [LIMIT=100]  API real
#   DRY=1  parsea y cuenta, no escribe en la BD
#   DATABASE_URL=postgres://...  destino del upsert (obligatorio sin DRY)
#
# `make shodan-ingest` delega aqui (el Makefile lo mantiene el
# orquestador); cron: 0 3 * * * con FIXTURE o SHODAN_KEY.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

args=()
if [[ -n "${FIXTURE:-}" ]]; then
  args+=(--fixture="$FIXTURE")
elif [[ -n "${SHODAN_KEY:-}" ]]; then
  args+=(--key="$SHODAN_KEY" --api-base="${SHODAN_API_BASE:-https://api.shodan.io}"
    --limit="${LIMIT:-100}" --query="${SHODAN_QUERY:-port:23,80,8080,554,32210}")
else
  echo "shodan_ingest: FIXTURE o SHODAN_KEY requerido" >&2
  exit 2
fi
if [[ "${DRY:-0}" == "1" || -z "${DATABASE_URL:-}" ]]; then
  args+=(--dry)
else
  args+=(--db="$DATABASE_URL")
fi

go run ./cmd/botnet-discover ingest-shodan "${args[@]}"
