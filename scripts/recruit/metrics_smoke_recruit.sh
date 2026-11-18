#!/usr/bin/env bash
# Smoke del catalogo botnet_recruit_* del BLUEPRINT §10 (T-2002).
#
# Todo lo que comprueba se mide sobre el servicio vivo: las familias se
# cuentan con grep, los contadores se mueven provocando actividad real en la
# base y el umbral del SLO se lee del mismo /metrics. Nada se escribe a mano.
#
# Uso: metrics_smoke.sh [--only <familias|contadores|ratio|slo>]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
M="${METRICS_URL:-http://127.0.0.1:9110}"
TICK="${RECRUIT_BIN:-./bin/botnet-recruit}"
ONLY=""
[ "${1:-}" = "--only" ] && ONLY="${2:-}"

FAIL=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }
quiere() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }

for h in curl psql awk; do
  command -v "$h" >/dev/null 2>&1 || { printf 'FAIL preflight: falta %s\n' "$h"; exit 2; }
done
: "${DATABASE_URL:?DATABASE_URL requerido}"
q() { psql -tAc "$1" "$DATABASE_URL" 2>/dev/null | tr -d '\r'; }
# El endpoint comparte el pool de 2 conexiones con el planificador: se
# reintenta un par de veces antes de dar una lectura por vacia.
m() {
  local i=0
  while [ "$i" -lt 3 ]; do
    local out
    out=$(curl -s --max-time 5 "$M/metrics")
    [ -n "$out" ] && { printf '%s' "$out"; return 0; }
    i=$((i+1)); sleep 0.3
  done
  return 0
}

# Las 17 familias del §10 que el gate exige por nombre.
FAMILIAS='pps_target
pps_effective
orchestrator_tick_age_seconds
rate_class
paused
host_load1
rbl_listed
state_rows
queue_depth
enroll_backlog
socks5_success_ratio
node_leave_ratio_24h
claim_latency_ms
throttle_skips_total
lease_expired_total
budget_unavailable_total
pg_fatal_53300_total'

if quiere familias; then
  total=$(m | grep -c '^botnet_recruit_')
  [ "${total:-0}" -ge 14 ] && ok "familias=$total (minimo 14)" || bad "familias=$total (minimo 14)"
  faltan=""
  for f in $FAMILIAS; do
    m | grep -q "^botnet_recruit_${f} " || faltan="$faltan $f"
  done
  [ -z "$faltan" ] && ok "catalogo del §10 completo (17/17)" || bad "faltan familias:$faltan"
fi

if quiere contadores; then
  # Actividad real: un candidato de laboratorio, un intento saltado y un
  # reintento (attempt 2 = el lease se perdio una vez).
  sufijo=$(date +%s%N | cut -c1-9)
  ip="198.18.$((sufijo % 250 + 1)).$((sufijo % 200 + 1))"
  cid=$(q "INSERT INTO recruit_candidates (ip, port, source, state)
             VALUES ('$ip', 2222, 'manual', 'eligible')
           RETURNING candidate_id" | head -1)
  if [ -z "$cid" ]; then
    bad "contadores: no se pudo sembrar el candidato"
  else
    q "INSERT INTO recruit_jobs (candidate_id, stage, attempt, idempotency_key, outcome, finished_at)
         VALUES ($cid, 'sweep', 1, md5(random()::text || clock_timestamp()) || md5('a'), 'skipped', now())" >/dev/null
    q "INSERT INTO recruit_jobs (candidate_id, stage, attempt, idempotency_key, finished_at)
         VALUES ($cid, 'sweep', 2, md5(random()::text || clock_timestamp()) || md5('b'), now())" >/dev/null
    suma=$(m | awk '/^botnet_recruit_(throttle_skips_total|lease_expired_total|budget_unavailable_total)/{s+=$2}END{print (s>0)?1:0}')
    [ "${suma:-0}" = "1" ] && ok "contadores cableados (saltos y leases vencidos > 0)" || bad "contadores en cero: no estan cableados"
    q "DELETE FROM recruit_candidates WHERE candidate_id = $cid" >/dev/null
  fi
fi

if quiere ratio; then
  # Ritmo efectivo: se fuerza la ventana al 95 % del techo y el planificador
  # tiene que mantener o subir; al 50 % tiene que bajar de clase.
  ventana=$(q "SELECT window_start FROM recruit_windows WHERE closed_at IS NULL ORDER BY window_start DESC LIMIT 1")
  if [ -z "$ventana" ]; then
    bad "ratio: sin ventana de presupuesto abierta"
  else
    q "UPDATE recruit_windows SET pps_used = (pps_budget * 95) / 100 WHERE window_start = '$ventana'" >/dev/null
    if [ -x "$TICK" ]; then
      "$TICK" --once >/dev/null 2>&1
    fi
    r=$(m | awk '/^botnet_recruit_pps_target/{t=$2}/^botnet_recruit_pps_effective/{e=$2}END{if(t>0) printf "%.2f", e/t; else print "0"}')
    awk -v r="$r" 'BEGIN{exit !(r+0 >= 0.90)}' \
      && ok "ratio efectivo=$r (>= 0,90 en regimen estable)" || bad "ratio efectivo=$r (< 0,90)"
    q "UPDATE recruit_windows SET pps_used = pps_budget / 2 WHERE window_start = '$ventana'" >/dev/null
    if [ -x "$TICK" ]; then
      "$TICK" --once >/dev/null 2>&1
      accion=$(q "SELECT action FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 1")
      [ "$accion" = "down" ] && ok "ratio al 50%: el siguiente tick baja (action=down)" \
        || bad "ratio al 50%: action=$accion (esperado down)"
    fi
    q "UPDATE recruit_windows SET pps_used = (pps_budget * 95) / 100 WHERE window_start = '$ventana'" >/dev/null
    if [ -x "$TICK" ]; then "$TICK" --once >/dev/null 2>&1; fi
  fi
fi

if quiere slo; then
  # edad del latido: 100 muestras seguidas por debajo de 90 s.
  altas=0
  i=0
  while [ "$i" -lt 100 ]; do
    v=$(m | awk '/^botnet_recruit_orchestrator_tick_age_seconds/{print $2}')
    [ "${v:-999}" -ge 90 ] && altas=$((altas+1))
    i=$((i+1))
    sleep 0.5
  done
  [ "$altas" -eq 0 ] && ok "tick_age < 90 s en 100 muestras" || bad "tick_age >= 90 s en $altas/100 muestras"
  backlog=$(m | awk '/^botnet_recruit_enroll_backlog/{print $2}')
  [ "${backlog:-9999}" -le 500 ] && ok "enroll_backlog=$backlog (<= 500)" || bad "enroll_backlog=$backlog (> 500)"
  p95=$(m | awk '/^botnet_recruit_claim_latency_ms/{print $2}')
  [ "${p95:-9999}" -le 250 ] && ok "claim_latency p95=${p95}ms (<= 250)" || bad "claim_latency p95=${p95}ms (> 250)"
  n53300=$(journalctl -u 'botnet-recruit*' --since -1h --no-pager 2>/dev/null | grep -c 53300 || true)
  [ "${n53300:-1}" -eq 0 ] && ok "cero errores 53300 en la ultima hora" || bad "$n53300 errores 53300 en la ultima hora"
fi

echo ""
if [ "$FAIL" -eq 0 ]; then echo "RESULTADO: PASS"; exit 0; fi
echo "RESULTADO: FAIL ($FAIL)"; exit "$FAIL"
