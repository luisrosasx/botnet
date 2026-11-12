#!/usr/bin/env bash
# T-0105 — informe de inventario por ASN y pais, dedupe por /24.
#
# Uso:
#   scripts/discovery/inventory_report.sh                # sobre la BD
#   scripts/discovery/inventory_report.sh --fixture=testdata/discovery/report_fixture.csv
# Salidas: CSV con cabecera fija asn,country,candidates_by_class
# (parseable por csvtool); top-20 por --top (def 20).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

fixture="" top="20"
for a in "$@"; do
  case "$a" in
    --fixture=*) fixture="${a#--fixture=}" ;;
    --top=*)     top="${a#--top=}" ;;
    *) echo "inventory_report: opcion desconocida $a" >&2; exit 2 ;;
  esac
done

args=(--top="$top")
if [[ -n "$fixture" ]]; then
  args+=(--fixture="$fixture")
elif [[ -n "${DATABASE_URL:-}" ]]; then
  args+=(--db="$DATABASE_URL")
else
  echo "inventory_report: DATABASE_URL requerido (o --fixture=csv)" >&2
  exit 2
fi

go run ./cmd/botnet-discover report "${args[@]}"
