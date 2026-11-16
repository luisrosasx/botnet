#!/usr/bin/env bash
# run_all_tests.sh — suite de las herramientas de campo.
#
# Corre las pruebas de cada herramienta de scripts/field y scripts/netem y
# comprueba el validador de cierre de criterios. Las que no tienen prueba
# propia (revoke_mass, load_250_1000) se verifican aqui con su contrato
# minimo: --help, rechazo de argumentos y --dry. Nada se da por bueno por
# compilar.
#
# Uso: bash scripts/field/run_all_tests.sh [--fast]
#   --fast   omite las pruebas que levantan motores o procesos Go
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
source scripts/env.sh || exit 1

FAST=0
[ "${1:-}" = "--fast" ] && FAST=1

FAIL=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }

run_test() {
  local name="$1" cmd="$2"
  if bash $cmd >/tmp/field_test.log 2>&1; then
    ok "$name"
  else
    bad "$name"
    tail -12 /tmp/field_test.log | sed 's/^/       /'
  fi
}

# Contrato minimo de las que no tienen prueba propia.
contract() {
  local name="$1"; shift
  local out rc
  out=$("$@" --help 2>&1); rc=$?
  if [ "$rc" -eq 0 ]; then ok "$name --help"; else bad "$name --help (rc=$rc)"; fi

  out=$("$@" --flag-que-no-existe 2>&1); rc=$?
  if [ "$rc" -eq 2 ]; then ok "$name rechaza flag invalido"; else bad "$name rechaza flag invalido (rc=$rc)"; fi
}

echo "== herramientas de campo =="
run_test "bootstrap"  scripts/field/bootstrap_test.sh
run_test "collect"    scripts/field/collect_test.sh
run_test "latency"    scripts/field/latency_pings_test.sh
run_test "rss"        scripts/field/rss_watch_test.sh
run_test "netem"      scripts/netem/netem_test.sh

contract "revoke_mass" bash scripts/field/revoke_mass.sh
contract "load_250_1000" bash scripts/field/load_250_1000.sh

if [ "$FAST" -eq 0 ]; then
  out=$(bash scripts/field/revoke_mass.sh --dry --ids 11111111-1111-1111-1111-111111111111 \
        --api http://127.0.0.1:1 --json 2>/dev/null)
  if [ -n "$out" ] && printf '%s' "$out" | jq -e . >/dev/null 2>&1; then
    ok "revoke_mass --dry emite JSON"
  else
    bad "revoke_mass --dry no emite JSON valido"
  fi
fi

echo "== validador de cierre de campo =="
run_test "check-field-closure (test)" scripts/check-field-closure_test.sh
if bash scripts/check-field-closure.sh --strict >/tmp/field_closure.log 2>&1; then
  ok "check-field-closure --strict"
else
  bad "check-field-closure --strict"
  grep -E 'doc=|fila pendiente' /tmp/field_closure.log | head -6 | sed 's/^/       /'
fi

echo ""
if [ "$FAIL" -eq 0 ]; then
  echo "RESULTADO: PASS"
  exit 0
fi
echo "RESULTADO: FAIL ($FAIL comprobaciones)"
exit 1
