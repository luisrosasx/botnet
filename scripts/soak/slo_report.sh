#!/usr/bin/env bash
# slo_report.sh — T-1207: las SLOs del blueprint son scripts que
# fallan, no parrafos que consuelan. Sobre el fixture del soak F2 (o
# el informe real del nightly) aserta la puerta F2 del BACKLOG §3:
#   streams_saturated < 10 %   y   p99_session_open_ms < 1500 a 250 nodos/gw.
# Publica slo_report.json (jq -e .streams_saturated sale 0) y, con
# --self-check, demuestra que con el fixture de ruptura el script
# SALE 1. Salida 0 = puerta verde; 1 = puerta en rojo (fallo real).
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

FIXTURE=${1:-scripts/soak/fixtures/slo_f2_fixture.json}
OUT=${2:-docs/verification/slo_report.json}

command -v jq >/dev/null 2>&1 || { echo "slo_report: jq no instalado" >&2; exit 2; }
[ -f "$FIXTURE" ] || { echo "slo_report: fixture ausente: $FIXTURE" >&2; exit 2; }

SAT_LIMIT=10      # streams_saturated < 10 % (puerta F2)
P99_LIMIT=1500    # P99 de apertura de sesion < 1,5 s a 250/gw
NODES_PER_GW=250

sat=$(jq -e '.streams_saturated_pct' "$FIXTURE")
p99=$(jq -e '.p99_session_open_ms' "$FIXTURE")
per_gw=$(jq -e '.nodes_per_gw' "$FIXTURE")

lt() { awk -v a="$1" -v b="$2" 'BEGIN { exit !(a < b) }'; }

ok_sat=0
lt "$sat" "$SAT_LIMIT" && ok_sat=1
ok_p99=0
if [ "$per_gw" -eq "$NODES_PER_GW" ]; then
  lt "$p99" "$P99_LIMIT" && ok_p99=1
else
  # La SLO se define a 250 nodos/gw; otra densidad hay que medirla
  # en su propio tramo — el script no aprueba a ciegas.
  echo "slo_report: nodes_per_gw=$per_gw != $NODES_PER_GW — P99 e2e fuera de su tramo" >&2
fi

mkdir -p "$(dirname "$OUT")"
jq -n \
  --slurpfile f "$FIXTURE" \
  --argjson ok_sat "$ok_sat" \
  --argjson ok_p99 "$ok_p99" \
  --arg sat_limit "$SAT_LIMIT" \
  --arg p99_limit "$P99_LIMIT" \
  '{
    source: $f[0].generated_by,
    nodes_per_gw: $f[0].nodes_per_gw,
    streams_saturated: $f[0].streams_saturated_pct,
    streams_saturated_limit_pct: ($sat_limit | tonumber),
    streams_saturated_ok: ($ok_sat == 1),
    p99_session_open_ms: $f[0].p99_session_open_ms,
    p99_session_open_limit_ms: ($p99_limit | tonumber),
    p99_session_open_ok: ($ok_p99 == 1),
    gate_f2: ($ok_sat == 1 and $ok_p99 == 1)
  }' > "$OUT"

jq -e '.streams_saturated' "$OUT" >/dev/null \
  || { echo "slo_report: slo_report.json sin .streams_saturated" >&2; exit 2; }

echo "slo_report: streams_saturated=${sat}% (<${SAT_LIMIT}) p99_open=${p99}ms (<${P99_LIMIT} a ${NODES_PER_GW}/gw) → $OUT"

if [ "${3:-}" = "--self-check" ]; then
  # El fixture de ruptura debe sacar 1: la puerta sabe fallar.
  if bash scripts/soak/slo_report.sh scripts/soak/fixtures/slo_breach_fixture.json "$OUT.breach" >/dev/null; then
    echo "slo_report: el fixture de ruptura NO debio pasar" >&2
    exit 3
  fi
  rm -f "$OUT.breach"
fi

[ "$ok_sat" -eq 1 ] && [ "$ok_p99" -eq 1 ]
