#!/usr/bin/env bash
# latency_pings_test.sh — prueba ejecutada de scripts/field/latency_pings.sh
# (EP-06 T-0604 AC2: p95 de los 100 pings con el bucket saturado).
#
# Se corre contra objetivos locales REALES: `ping` del host (Windows o
# Linux) sobre 127.0.0.1. La saturacion iperf3 se prueba con el doble de
# ruta inexistente (solo se comprueba que se exige la herramienta).
#
#   bash scripts/field/latency_pings_test.sh
#
# Salida 0 = todas las aserciones verdes; 1 = alguna falla.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/scripts/field/latency_pings.sh"

FAILS=0
CASES=0

ok() {
  CASES=$((CASES + 1))
  printf '  [OK]   %s\n' "$1"
}

ko() {
  CASES=$((CASES + 1))
  FAILS=$((FAILS + 1))
  printf '  [FALLO] %s\n' "$1"
}

assert_rc() { # descriptor rc esperado rc obtenido
  if [ "$2" = "$3" ]; then ok "$1 (rc=$3)"; else ko "$1: esperaba rc=$2, obtuve rc=$3"; fi
}

# num_json <campo> <json> -> valor numerico del campo
num_json() { printf '%s' "$2" | LC_ALL=C sed -n "s/.*\"$1\":\([-0-9.]*\).*/\1/p"; }

[ -f "$SCRIPT" ] || { printf 'latency_pings_test: no existe %s\n' "$SCRIPT" >&2; exit 1; }

printf '\n== latency_pings: ayuda y argumentos ==\n'

OUT=$("$SCRIPT" --help 2>&1); RC=$?
assert_rc "--help sale 0" 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -qi 'Uso' && ok '--help imprime el uso' || ko '--help no imprime el uso'

"$SCRIPT" --count 10 >/dev/null 2>&1; assert_rc 'sin --target sale 2' 2 $?
"$SCRIPT" --target local --count abc >/dev/null 2>&1; assert_rc '--count no numerico sale 2' 2 $?
"$SCRIPT" --target local --threshold-p95-ms nano >/dev/null 2>&1; assert_rc '--threshold-p95-ms no numerico sale 2' 2 $?
"$SCRIPT" --target local --sabor fresa >/dev/null 2>&1; assert_rc 'argumento desconocido sale 2' 2 $?

printf '\n== latency_pings: modo --dry (no ejecuta nada) ==\n'

OUT=$("$SCRIPT" --target local --count 10 --dry 2>&1); RC=$?
assert_rc '--dry local sale 0' 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q 'PING: ' && ok '--dry imprime el comando ping' || ko '--dry no imprime el comando ping'
printf '%s' "$OUT" | LC_ALL=C grep -Eq '\-n 10|\-c 10' && ok '--dry lleva el --count pedido' || ko '--dry no refleja el --count'

OUT=$("$SCRIPT" --target nodo-fake --count 100 --saturate-mbps 5 --dry 2>&1); RC=$?
assert_rc '--dry remoto sale 0' 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q 'iperf3' && ok '--dry remoto muestra la carga iperf3' || ko '--dry remoto no muestra la carga iperf3'
printf '%s' "$OUT" | LC_ALL=C grep -q 'os=linux' && ok '--dry remoto asume Linux en el nodo' || ko '--dry remoto no asume Linux'

printf '\n== latency_pings: herramienta ausente sale 3 ==\n'

PING_BIN=/no/existe/ping "$SCRIPT" --target local --count 3 >/dev/null 2>&1
assert_rc 'ping ausente sale 3' 3 $?
IPERF3_BIN=/no/existe/iperf3 "$SCRIPT" --target local --count 3 --saturate-mbps 5 >/dev/null 2>&1
assert_rc 'iperf3 ausente con --saturate-mbps sale 3' 3 $?
# Sin --saturate-mbps, la ausencia de iperf3 NO puede fastidiar la medicion.
IPERF3_BIN=/no/existe/iperf3 "$SCRIPT" --target local --count 2 --json >/dev/null 2>&1
RC=$?
[ "$RC" = 3 ] && assert_rc 'iperf3 ausente SIN --saturate-mbps sale 3' 0 "$RC" || ok "iperf3 ausente SIN --saturate-mbps no sale 3 (rc=$RC)"

printf '\n== latency_pings: medicion real (objetivo local) ==\n'

OUT=$("$SCRIPT" --target local --count 10 --threshold-p95-ms 5000 --json 2>/dev/null); RC=$?
printf '  JSON real: %s\n' "$OUT"
assert_rc '--target local --count 10 --json sale 0 con umbral alto' 0 "$RC"
RECV=$(num_json recv "$OUT")
P50=$(num_json p50_ms "$OUT")
P95=$(num_json p95_ms "$OUT")
P99=$(num_json p99_ms "$OUT")
MAXV=$(num_json max_ms "$OUT")
if [ "${RECV:-0}" -gt 0 ] 2>/dev/null; then
  ok "respuestas leidas: $RECV/10"
else
  ko "no se leyeron respuestas (recv=$RECV) — el ping local no devolvio RTT"
fi
LC_ALL=C awk -v a="$P50" -v b="$P95" -v c="$P99" -v d="$MAXV" \
  'BEGIN{ exit (a != "" && b != "" && c != "" && d != "") ? 0 : 1 }' \
  && ok "p50=$P50 p95=$P95 p99=$P99 max=$MAXV ms presentes" \
  || ko "faltan percentiles (p50=$P50 p95=$P95 p99=$P99 max=$MAXV)"
LC_ALL=C awk -v a="$P50" -v b="$P95" -v c="$P99" -v d="$MAXV" \
  'BEGIN{ exit (a <= b && b <= c && c <= d) ? 0 : 1 }' \
  && ok 'orden de percentiles coherente (p50 <= p95 <= p99 <= max)' \
  || ko 'orden de percentiles incoherente'
printf '%s' "$OUT" | LC_ALL=C grep -q '"verdict":"PASS"' && ok 'veredicto PASS con umbral alto' || ko 'veredicto no PASS con umbral alto'

# El mismo objetivo con umbral 0 no puede pasar: es la puerta del exit 1.
"$SCRIPT" --target local --count 5 --threshold-p95-ms 0 >/dev/null 2>&1
assert_rc 'umbral 0 ms sale 1 (p95 por encima)' 1 $?

# 100 % de perdida: 0 respuestas => veredicto FAIL, exit 1 (no 3).
OUT=$("$SCRIPT" --target local --count 2 --host 240.0.0.1 --json 2>/dev/null); RC=$?
printf '  perdida: %s\n' "$OUT"
assert_rc 'destino sin respuesta sale 1' 1 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q '"recv":0' && ok 'sin respuestas: recv=0' || ko 'sin respuestas: recv!=0'

printf '\nlatency_pings_test: %d aserciones, %d fallos\n' "$CASES" "$FAILS"
[ "$FAILS" -eq 0 ] || exit 1
printf 'latency_pings_test: OK\n'
