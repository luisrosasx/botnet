#!/usr/bin/env bash
# Puerta del servicio de reclutamiento. Cada comprobacion muerde: si el
# servicio no ha hecho el trabajo, sale rojo.
#
# Uso: check-recruit.sh [--only <seccion>]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
source scripts/env.sh >/dev/null 2>&1 || true
: "${DATABASE_URL:?DATABASE_URL requerido}"

ONLY=""
[ "${1:-}" = "--only" ] && ONLY="${2:-}"
FAIL=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }
quiere() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }

q() { psql -tAc "$1" "$DATABASE_URL" 2>/dev/null | tr -d '\r'; }

if quiere oleadas; then
  cerradas=$(q "SELECT count(*) FROM (SELECT 1 FROM docs_waves) x" 2>/dev/null)
  [ "$(ls docs/recruit/w1-proposals/*.md 2>/dev/null | wc -l)" -ge 3 ] && ok "oleada 1: 3 propuestas" || bad "oleada 1"
  [ "$(ls docs/recruit/w2-defense/*.md 2>/dev/null | wc -l)" -ge 3 ] && ok "oleada 2: 3 defensas" || bad "oleada 2"
  [ "$(ls docs/recruit/w3-attack/*.md 2>/dev/null | wc -l)" -ge 3 ] && ok "oleada 3: 3 ataques" || bad "oleada 3"
  [ "$(ls docs/recruit/w4-verdicts/*.md 2>/dev/null | wc -l)" -ge 3 ] && ok "oleada 4: 3 veredictos" || bad "oleada 4"
  injertos=$(grep -o 'injerto: V[123]' docs/recruit/BLUEPRINT.md 2>/dev/null | wc -l)
  [ "$injertos" -ge 12 ] && ok "injertos aplicados=$injertos" || bad "injertos=$injertos (minimo 12)"
fi

if quiere tareas; then
  total=$(grep -rh '^### T-' docs/recruit/TASKS/ 2>/dev/null | wc -l)
  hechas=$(grep -rh '^- \*\*Estado:\*\* DONE' docs/recruit/TASKS/ 2>/dev/null | wc -l)
  [ "$hechas" -eq "$total" ] && ok "tasks DONE=$hechas/$total" || bad "tasks DONE=$hechas/$total"
fi

if quiere socks5; then
  # 20 nodos verificados con evidencia dura y salida distinta del gateway.
  n=$(q "SELECT count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id)
          WHERE s.ok AND s.ip_match AND s.egress_ip = n.public_ip
            AND s.egress_ip <> '74.208.94.91'")
  [ "${n:-0}" -ge 20 ] && ok "socks5: $n nodos con IP de salida propia" || bad "socks5: $n nodos con IP propia (minimo 20)"
  mal=$(q "SELECT count(*) FROM recruit_socks5_checks WHERE ok AND (bytes_in<=0 OR latency_ms>1500 OR http_status<>200)")
  [ "${mal:-1}" -eq 0 ] && ok "socks5: cero ganadores sin evidencia dura" || bad "socks5: $mal ganadores sin evidencia"
fi

if quiere servicio; then
  code=$(curl -s -o /dev/null -w '%{http_code}' 127.0.0.1:9110/healthz 2>/dev/null)
  [ "$code" = "200" ] && ok "healthcheck del orquestador" || bad "healthcheck=$code"
fi

echo ""
if [ "$FAIL" -eq 0 ]; then echo "RESULTADO: PASS"; exit 0; fi
echo "RESULTADO: FAIL ($FAIL comprobaciones)"; exit 1
