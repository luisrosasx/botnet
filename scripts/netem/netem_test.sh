#!/usr/bin/env bash
# netem_test.sh — prueba ejecutada de scripts/netem/apply.sh y clear.sh
# (EP-02 T-0202 AC3 y EP-09: perdida REAL con tc netem).
#
# En el host F4 (Windows) no hay `tc` ni `ip`: aqui se valida la GENERACION
# de la orden (--dry), el contrato de argumentos y el salida 3 honesta cuando
# la herramienta falta. La aplicacion real se prueba aparte en un nodo Linux
# con NETEM_TEST_TARGET=<ssh-host> (tc de verdad, con limpieza posterior).
#
#   bash scripts/netem/netem_test.sh
#   NETEM_TEST_TARGET=root@100.101.17.24 NETEM_TEST_IFACE=lo \
#     bash scripts/netem/netem_test.sh
#
# Salida 0 = todas las aserciones verdes; 1 = alguna falla.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
APPLY="$ROOT/scripts/netem/apply.sh"
CLEAR="$ROOT/scripts/netem/clear.sh"

FAILS=0
CASES=0

ok() { CASES=$((CASES + 1)); printf '  [OK]   %s\n' "$1"; }
ko() { CASES=$((CASES + 1)); FAILS=$((FAILS + 1)); printf '  [FALLO] %s\n' "$1"; }
assert_rc() {
  if [ "$2" = "$3" ]; then ok "$1 (rc=$3)"; else ko "$1: esperaba rc=$2, obtuve rc=$3"; fi
}
assert_has() { # descriptor patron texto
  if printf '%s' "$3" | LC_ALL=C grep -q "$2"; then ok "$1"; else ko "$1: falta /$2/"; fi
}

[ -f "$APPLY" ] || { printf 'netem_test: no existe %s\n' "$APPLY" >&2; exit 1; }
[ -f "$CLEAR" ] || { printf 'netem_test: no existe %s\n' "$CLEAR" >&2; exit 1; }

printf '\n== netem: ayuda ==\n'

OUT=$("$APPLY" --help 2>&1); assert_rc 'apply --help sale 0' 0 "$?"
assert_has 'apply --help imprime el uso' 'Uso' "$OUT"
OUT=$("$CLEAR" --help 2>&1); assert_rc 'clear --help sale 0' 0 "$?"
assert_has 'clear --help imprime el uso' 'Uso' "$OUT"

printf '\n== netem: --dry (genera la orden, no ejecuta) ==\n'

OUT=$("$APPLY" --iface eth0 --loss 1.5 --dry 2>&1); RC=$?
printf '  apply --dry: %s\n' "$OUT"
assert_rc 'apply --dry sale 0' 0 "$RC"
assert_has 'apply --dry contiene tc qdisc' 'tc qdisc' "$OUT"
assert_has 'apply --dry usa add y luego replace (no duplica)' 'replace' "$OUT"
assert_has 'apply --dry pone el loss pedido' 'loss 1.5%' "$OUT"
assert_has 'apply --dry ancla el qdisc en root' 'root netem' "$OUT"

OUT=$("$APPLY" --iface eth0 --loss 2 --delay-ms 20 --jitter-ms 5 --corrupt 0.1 --dry 2>&1); RC=$?
printf '  apply --dry completo: %s\n' "$OUT"
assert_rc 'apply --dry con delay/jitter/corrupt sale 0' 0 "$RC"
assert_has 'apply --dry lleva el delay' 'delay 20ms' "$OUT"
assert_has 'apply --dry lleva el jitter pegado al delay' 'delay 20ms 5ms' "$OUT"
assert_has 'apply --dry lleva la corrupcion' 'corrupt 0.1%' "$OUT"

OUT=$("$APPLY" --iface lo --loss 1 --dry --check 2>&1); RC=$?
printf '  apply --dry --check: %s\n' "$OUT"
assert_rc 'apply --dry --check sale 0' 0 "$RC"
assert_has 'apply --dry --check imprime tc qdisc show' 'tc qdisc show dev lo' "$OUT"

OUT=$("$APPLY" --iface eth0 --loss 1 --target nodo-fake --dry 2>&1); RC=$?
assert_rc 'apply --dry remoto sale 0' 0 "$RC"
assert_has 'apply --dry remoto anuncia el objetivo' 'nodo-fake' "$OUT"

OUT=$("$CLEAR" --iface eth0 --dry 2>&1); RC=$?
printf '  clear --dry: %s\n' "$OUT"
assert_rc 'clear --dry sale 0' 0 "$RC"
assert_has 'clear --dry contiene tc qdisc' 'tc qdisc' "$OUT"
assert_has 'clear --dry retira el root (|| true, idempotente)' 'qdisc del dev eth0 root' "$OUT"

printf '\n== netem: argumentos ==\n'

"$APPLY" --loss 1 --dry >/dev/null 2>&1; assert_rc 'apply sin --iface sale 2' 2 $?
"$CLEAR" --dry >/dev/null 2>&1; assert_rc 'clear sin --iface sale 2' 2 $?
"$APPLY" --iface eth0 --dry >/dev/null 2>&1; assert_rc 'apply sin --loss y sin --check sale 2' 2 $?
"$APPLY" --iface eth0 --loss 1 --jitter-ms 5 --dry >/dev/null 2>&1
assert_rc '--jitter-ms sin --delay-ms sale 2' 2 $?
"$APPLY" --iface eth0 --loss 120 --dry >/dev/null 2>&1; assert_rc '--loss > 100 sale 2' 2 $?
"$APPLY" --iface eth0 --loss-1 --dry >/dev/null 2>&1; assert_rc 'flag desconocido sale 2' 2 $?
"$APPLY" --iface 'eth 0' --loss 1 --dry >/dev/null 2>&1; assert_rc '--iface con espacios sale 2' 2 $?
OUT=$("$APPLY" --iface lo --check --dry 2>&1); assert_rc '--check sin --loss vale (solo verifica)' 0 "$?"
assert_has '--check sin --loss no aplica nada' 'solo se verifica' "$OUT"

printf '\n== netem: herramienta ausente sale 3 (host F4 sin tc) ==\n'

OUT=$(TC_BIN=/no/existe/tc "$APPLY" --iface eth0 --loss 1.5 2>&1); RC=$?
printf '  apply sin tc: %s\n' "$OUT"
assert_rc 'apply sin tc sale 3' 3 "$RC"
assert_has 'apply sin tc lo dice claro' 'tc no disponible' "$OUT"
OUT=$(TC_BIN=/no/existe/tc "$CLEAR" --iface eth0 2>&1); RC=$?
printf '  clear sin tc: %s\n' "$OUT"
assert_rc 'clear sin tc sale 3' 3 "$RC"
assert_has 'clear sin tc lo dice claro' 'tc no disponible' "$OUT"
SSH_BIN=/no/existe/ssh "$APPLY" --iface eth0 --loss 1 --target nodo-fake >/dev/null 2>&1
assert_rc 'ssh ausente con objetivo remoto sale 3' 3 $?

printf '\n== netem: tc doble (parseo de tc qdisc show) ==\n'

TMPD=$(mktemp -d)
trap '[ "${KEEP_NETEM_TMP:-}" = 1 ] || rm -rf "$TMPD"' EXIT
FAKE="$TMPD/fake"
mkdir -p "$FAKE"
# Doble de tc: guarda el qdisc root en un fichero; `add` falla si ya existe
# (igual que RTNETLINK «File exists»), `replace` manda, `show` imprime.
cat >"$FAKE/tc" <<'FAKE_EOF'
#!/usr/bin/env bash
set -u
STATE=${FAKE_TC_STATE:?estado del doble de tc}
[ "$1" = qdisc ] || exit 0
case "$2" in
  add)
    if [ -s "$STATE" ]; then
      echo 'RTNETLINK answers: File exists' >&2
      exit 2
    fi
    shift 5 # qdisc add dev <if> root
    printf 'qdisc netem 8001: root refcnt 2 %s\n' "${*#netem }" >"$STATE"
    ;;
  replace)
    shift 5
    printf 'qdisc netem 8001: root refcnt 2 %s\n' "${*#netem }" >"$STATE"
    ;;
  show)
    if [ -s "$STATE" ]; then cat "$STATE"; else echo 'qdisc noqueue 0: root refcnt 2'; fi
    ;;
  del) : >"$STATE" ;;
esac
exit 0
FAKE_EOF
chmod +x "$FAKE/tc"

if [ -n "${NETEM_TEST_TARGET:-}" ]; then
  printf '  (tc real en %s)\n' "$NETEM_TEST_TARGET"
  OUT=$("$APPLY" --iface "${NETEM_TEST_IFACE:-eth0}" --loss 1.5 --target "$NETEM_TEST_TARGET" --check --json 2>&1)
  RC=$?
  printf '  apply real: %s\n' "$OUT"
  assert_rc 'apply real + check en el nodo sale 0' 0 "$RC"
  OUT=$("$APPLY" --iface "${NETEM_TEST_IFACE:-eth0}" --loss 1.5 --target "$NETEM_TEST_TARGET" --check --json 2>&1)
  assert_rc '2a aplicacion real no duplica el qdisc' 0 "$?"
  OUT=$("$CLEAR" --iface "${NETEM_TEST_IFACE:-eth0}" --target "$NETEM_TEST_TARGET" --json 2>&1)
  RC=$?
  printf '  clear real: %s\n' "$OUT"
  assert_rc 'clear real deja el root sin netem' 0 "$RC"
else
  export FAKE_TC_STATE=$TMPD/qdisc
  : >"$FAKE_TC_STATE"
  OUT=$(PATH="$FAKE:$PATH" TC_BIN="$FAKE/tc" "$APPLY" --iface eth0 --loss 1.5 --delay-ms 20 --jitter-ms 5 --check --json 2>&1)
  RC=$?
  printf '  apply con doble: %s\n' "$OUT"
  assert_rc 'apply con doble de tc + check sale 0' 0 "$RC"
  printf '%s' "$OUT" | LC_ALL=C grep -q '"check_present":true' \
    && ok 'el check reconoce el netem recien aplicado' || ko 'el check no reconoce el netem aplicado'
  OUT=$(PATH="$FAKE:$PATH" TC_BIN="$FAKE/tc" "$APPLY" --iface eth0 --loss 1.5 --delay-ms 20 --jitter-ms 5 --check 2>&1)
  RC=$?
  assert_rc 'reaplicar sobre qdisc existente (add falla -> replace) sale 0' 0 "$RC"
  NQDISC=$(LC_ALL=C grep -c '^qdisc' "$FAKE_TC_STATE")
  [ "$NQDISC" = 1 ] && ok 'la reaplicacion NO duplica la regla (1 solo qdisc root)' || ko "la reaplicacion duplico la regla ($NQDISC lineas qdisc)"
  OUT=$(PATH="$FAKE:$PATH" TC_BIN="$FAKE/tc" "$CLEAR" --iface eth0 --json 2>&1)
  RC=$?
  printf '  clear con doble: %s\n' "$OUT"
  assert_rc 'clear con doble deja el root limpio' 0 "$RC"
  printf '%s' "$OUT" | LC_ALL=C grep -q '"verdict":"LIMPIO"' && ok 'clear reporta LIMPIO' || ko 'clear no reporta LIMPIO'
  # Con el root ya sin netem, el check no puede encontrar la regla => 1.
  OUT=$(PATH="$FAKE:$PATH" TC_BIN="$FAKE/tc" "$APPLY" --iface eth0 --check --json 2>&1)
  RC=$?
  printf '  check sin netem aplicado: %s\n' "$OUT"
  assert_rc 'check sin regla netem en el root sale 1' 1 "$RC"
  printf '%s' "$OUT" | LC_ALL=C grep -q '"check_present":false' \
    && ok 'el check distingue noqueue de netem' || ko 'el check no distingue noqueue de netem'
  PATH="$FAKE:$PATH" TC_BIN="$FAKE/tc" "$CLEAR" --iface eth0 >/dev/null 2>&1
  assert_rc 'clear dos veces es idempotente' 0 "$?"
fi

printf '\nnetem_test: %d aserciones, %d fallos\n' "$CASES" "$FAILS"
[ "$FAILS" -eq 0 ] || exit 1
printf 'netem_test: OK\n'
