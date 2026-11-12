#!/usr/bin/env bash
# EP-07 T-0704: sonda de geo del exit. Se sale a un echo de geo ("que
# ve el mundo") a traves del SOCKS del gateway del nodo, se leen
# pais/ASN observados y se suben al backend (POST /v1/nodes/{id}/geo,
# bearer admin): ahi cae el veredicto geo_verified + country_probed
# (cmd/botnet-api/services/geo.go). Cadencia semanal: esto corre una
# vez por nodo y semana (7 pasadas por 7 cadencias; ≤ 5 sondas/mes,
# ≤ 50 KB/nodo/mes con ~700 B por pasada).
#
# Produccion (malla o red de operaciones arriba):
#   NODE_ID=<uuid> GATEWAY_SOCKS=gw.internal:1080 API_BASE=http://api:8080 \
#   API_ADMIN_TOKEN=... [GEO_ECHO=https://ipinfo.io/json] \
#     bash scripts/mesh/geo_probe.sh
#
# Host F4 sin salida: doble determinista que aserta el parseo:
#   bash scripts/mesh/geo_probe.sh --self-test
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

GEO_ECHO="${GEO_ECHO:-https://ipinfo.io/json}"
BYTES_ESTIMATE=700 # presupuesto por pasada (services.GeoProbeBytesTX)

# parseo del echo: country de "country":"ES" y ASN del "asn":"AS12345"
# o "ASNS"... se extrae el entero tras AS.
parse_country() { awk 'match($0, /"country"[ ]*:[ ]*"[A-Z][A-Z]"/){ s=substr($0, RSTART, RLENGTH); gsub(/[^A-Z]/, "", s); print substr(s, length(s) - 1); exit }' "$1"; }
parse_asn()     { awk 'match($0, /AS[0-9]+/){ s=substr($0, RSTART+2, RLENGTH-2); print s+0; exit }' "$1"; }

if [[ "${1:-}" == "--self-test" ]]; then
  # Doble: fixture ES residencial (la del task) parseada igual que el
  # echo real. El veredicto duro (verified, pool) lo aserta el test Go
  # services.TestGeoVerdictResidencialYDC; aqui se prueba el transporte
  # de la lectura: 2 letras + ASN entero, y el presupuesto por pasada.
  fx="$(mktemp)"
  printf '%s' '{"org":"Telefonica ES","asn":"AS12345","country":"ES","city":"Madrid"}' >"$fx"
  c="$(parse_country "$fx")"; a="$(parse_asn "$fx")"
  rm -f "$fx"
  [[ "$c" == "ES" ]] || { echo "geo_probe self-test: country=$c, want ES" >&2; exit 1; }
  [[ "$a" == "12345" ]] || { echo "geo_probe self-test: asn=$a, want 12345" >&2; exit 1; }
  echo "geo_probe self-test: OK (ES/12345 parseados; ~${BYTES_ESTIMATE} B/pasada, 5/peor-mes => ≤ $((5 * BYTES_ESTIMATE)) B/mes ≤ 51200)"
  exit 0
fi

: "${NODE_ID:?NODE_ID (uuid) requerido}"
: "${API_BASE:?API_BASE requerido (http://api:8080)}"
: "${API_ADMIN_TOKEN:?API_ADMIN_TOKEN requerido}"
command -v curl >/dev/null 2>&1 || { echo "geo_probe: curl requerido" >&2; exit 2; }

SOCKS_ARGS=()
if [[ -n "${GATEWAY_SOCKS:-}" ]]; then SOCKS_ARGS=(--socks5-hostname "$GATEWAY_SOCKS"); fi

# 1) salir por el exit y capturar el echo (los bytes de la pasada se
# estiman por el tamano de la respuesta + overhead TLS fijo).
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
BYTES=$(curl -sS "${SOCKS_ARGS[@]}" -o "$tmp" -w '%{size_download}' "$GEO_ECHO" || true)
[[ -s "$tmp" ]] || { echo "geo_probe: echo de geo vacio (¿gateway caido?)" >&2; exit 1; }
COUNTRY="$(parse_country "$tmp")"
ASN="$(parse_asn "$tmp")"
[[ "${#COUNTRY}" == 2 && -n "$ASN" ]] || { echo "geo_probe: lectura invalida: country=$COUNTRY asn=$ASN" >&2; exit 1; }
CITY="$(awk 'match($0,/"city"[^"]*"[^"]*"/){s=substr($0,RSTART,RLENGTH); sub(/.*"city"[^"]*"/,"",s); sub(/".*/,"",s); print s; exit}' "$tmp")"
BYTES=$(( ${BYTES:-0} + 300 )) # overhead de handshakes/headers por pasada

# 2) subir el veredicto crudo al backend: el service decide y escribe.
BODY="{\"country\":\"$COUNTRY\",\"asn\":$ASN,\"city\":\"$CITY\",\"bytes_tx\":$BYTES}"
curl -sS -X POST "$API_BASE/v1/nodes/$NODE_ID/geo" \
  -H "Authorization: Bearer $API_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d "$BODY"
echo
echo "geo_probe: observando $COUNTRY/AS$ASN por el exit de $NODE_ID (${BYTES} B tx)"
