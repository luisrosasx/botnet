#!/usr/bin/env bash
# rss_watch_test.sh — prueba ejecutada de scripts/field/rss_watch.sh
# (EP-03 T-0301 AC3: RSS <= 3,1 MB en reposo 10 min; EP-06 T-0603 AC2:
# RSS real con 150 streams <= 6 MB).
#
# Muestrea un proceso REAL del host: el propio pid de esta prueba (en Windows
# se traduce MSYS->WINPID dentro del script) y, aparte, el nombre `bash`.
#
#   bash scripts/field/rss_watch_test.sh
#
# Salida 0 = todas las aserciones verdes; 1 = alguna falla.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/scripts/field/rss_watch.sh"

FAILS=0
CASES=0

ok() { CASES=$((CASES + 1)); printf '  [OK]   %s\n' "$1"; }
ko() { CASES=$((CASES + 1)); FAILS=$((FAILS + 1)); printf '  [FALLO] %s\n' "$1"; }
assert_rc() {
  if [ "$2" = "$3" ]; then ok "$1 (rc=$3)"; else ko "$1: esperaba rc=$2, obtuve rc=$3"; fi
}
num_json() { printf '%s' "$2" | LC_ALL=C sed -n "s/.*\"$1\":\([-0-9.]*\).*/\1/p"; }

IS_WIN=0
case "$(uname -s 2>/dev/null)" in
  MINGW* | MSYS* | CYGWIN*) IS_WIN=1 ;;
esac

[ -f "$SCRIPT" ] || { printf 'rss_watch_test: no existe %s\n' "$SCRIPT" >&2; exit 1; }

printf '\n== rss_watch: ayuda y argumentos ==\n'

OUT=$("$SCRIPT" --help 2>&1); RC=$?
assert_rc "--help sale 0" 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -qi 'Uso' && ok '--help imprime el uso' || ko '--help no imprime el uso'

"$SCRIPT" --pid 1 >/dev/null 2>&1; assert_rc 'sin --target sale 2' 2 $?
"$SCRIPT" --target local >/dev/null 2>&1; assert_rc 'sin --pid sale 2' 2 $?
"$SCRIPT" --target local --pid 1 --minutes rapido >/dev/null 2>&1; assert_rc '--minutes no numerico sale 2' 2 $?
"$SCRIPT" --target local --pid 1 --interval 0 >/dev/null 2>&1; assert_rc '--interval 0 sale 2' 2 $?
"$SCRIPT" --target local --pid 'con espacio' >/dev/null 2>&1; assert_rc '--pid con espacios sale 2' 2 $?

printf '\n== rss_watch: modo --dry (no ejecuta nada) ==\n'

OUT=$("$SCRIPT" --target local --pid 1 --minutes 0.2 --interval 5 --dry 2>&1); RC=$?
assert_rc '--dry local sale 0' 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q 'MUESTRA: ' && ok '--dry imprime el comando de muestra' || ko '--dry no imprime el comando de muestra'
printf '%s' "$OUT" | LC_ALL=C grep -q 'VEREDICTO: pico' && ok '--dry imprime el criterio del veredicto' || ko '--dry no imprime el criterio'

OUT=$("$SCRIPT" --target nodo-fake --pid botd --dry 2>&1); RC=$?
assert_rc '--dry remoto sale 0' 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q 'ps -o rss=' && ok '--dry remoto usa ps -o rss= en el nodo' || ko '--dry remoto no usa ps -o rss='
printf '%s' "$OUT" | LC_ALL=C grep -q 'metrica=rss_kb' && ok '--dry remoto declara la metrica rss_kb' || ko '--dry remoto no declara la metrica'

printf '\n== rss_watch: herramienta ausente sale 3 ==\n'

if [ "$IS_WIN" = 1 ]; then
  POWERSHELL_BIN=/no/existe/powershell "$SCRIPT" --target local --pid $$ --minutes 0.02 --interval 1 >/dev/null 2>&1
  assert_rc 'powershell ausente sale 3' 3 $?
else
  PS_BIN=/no/existe/ps "$SCRIPT" --target local --pid $$ --minutes 0.02 --interval 1 >/dev/null 2>&1
  assert_rc 'ps ausente sale 3' 3 $?
fi
SSH_BIN=/no/existe/ssh "$SCRIPT" --target nodo-fake --pid botd --minutes 0.02 --interval 1 >/dev/null 2>&1
assert_rc 'ssh ausente con objetivo remoto sale 3' 3 $?

printf '\n== rss_watch: muestreo real (pid de esta prueba) ==\n'

OUT=$("$SCRIPT" --target local --pid $$ --minutes 0.2 --interval 5 --threshold-mb 100000 --json 2>/dev/null); RC=$?
printf '  JSON real (pid %s): %s\n' "$$" "$OUT"
assert_rc 'muestreo real con umbral alto sale 0' 0 "$RC"
SAMPLES=$(num_json samples "$OUT")
FIRST=$(num_json first_mb "$OUT")
LAST=$(num_json last_mb "$OUT")
PEAK=$(num_json peak_mb "$OUT")
MEAN=$(num_json mean_mb "$OUT")
if [ "${SAMPLES:-0}" -gt 1 ] 2>/dev/null; then
  ok "muestras tomadas: $SAMPLES en 0.2 min cada 5 s"
else
  ko "solo $SAMPLES muestra(s) — se esperaban al menos 2"
fi
LC_ALL=C awk -v v="${LAST:-0}" 'BEGIN{ exit (v > 0) ? 0 : 1 }' \
  && ok "ultima lectura > 0 MB ($LAST MB)" || ko "ultima lectura no es > 0 MB ($LAST MB)"
LC_ALL=C awk -v p="${PEAK:-0}" -v m="${MEAN:-0}" -v l="${LAST:-0}" \
  'BEGIN{ exit (p >= m && p >= l && p > 0) ? 0 : 1 }' \
  && ok "pico coherente y > 0 MB (pico=$PEAK media=$MEAN ultima=$LAST)" \
  || ko "pico incoherente (pico=$PEAK media=$MEAN ultima=$LAST)"
printf '%s' "$OUT" | LC_ALL=C grep -q '"verdict":"PASS"' && ok 'veredicto PASS con umbral alto' || ko 'veredicto no PASS con umbral alto'
printf '%s' "$OUT" | LC_ALL=C grep -Eq '"metric":"(rss_kb|working_set_bytes)"' \
  && ok 'la salida declara que magnitud se midio (metric)' || ko 'falta el campo metric'

# Umbral 0 MB: ninguna lectura puede quedar por debajo => exit 1.
"$SCRIPT" --target local --pid $$ --minutes 0.02 --interval 1 --threshold-mb 0 >/dev/null 2>&1
assert_rc 'umbral 0 MB sale 1 (pico por encima)' 1 $?

printf '\n== rss_watch: --pid por nombre y proceso ausente ==\n'

OUT=$("$SCRIPT" --target local --pid bash --minutes 0.05 --interval 1 --threshold-mb 100000 --json 2>/dev/null); RC=$?
printf '  JSON por nombre (bash): %s\n' "$OUT"
assert_rc '--pid por nombre sale 0 con umbral alto' 0 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q '"pid":"bash"' && ok '--pid por nombre aceptado' || ko '--pid por nombre no aceptado'

OUT=$("$SCRIPT" --target local --pid 999999 --minutes 0.02 --interval 1 --json 2>/dev/null); RC=$?
printf '  proceso inexistente: %s\n' "$OUT"
assert_rc 'proceso inexistente sale 1' 1 "$RC"
printf '%s' "$OUT" | LC_ALL=C grep -q '"verdict":"NOPROCESO"' && ok 'veredicto NOPROCESO sin lecturas' || ko 'falta el veredicto NOPROCESO'

printf '\nrss_watch_test: %d aserciones, %d fallos\n' "$CASES" "$FAILS"
[ "$FAILS" -eq 0 ] || exit 1
printf 'rss_watch_test: OK\n'
