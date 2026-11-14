#!/usr/bin/env bash
# all.sh — compilacion completa de BOT.NET en un solo comando.
#
# Encadena todo lo que produce artefactos o los valida, en orden y
# fallando al primer rojo. No reinventa pasos: delega en los scripts que
# ya existen (gate de implementacion, matriz cross, firma, humo, gates
# documentales) y guarda la evidencia en
# docs/verification/evidence/build-<fecha>.log.
#
# Uso: bash scripts/build/all.sh [--fast]
#   --fast   omite la matriz cross, la firma y el humo (solo compila,
#            vet, test y gates). Para iterar sin esperar 20 minutos.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
source scripts/env.sh || exit 1

FAST=0
[ "${1:-}" = "--fast" ] && FAST=1

STAMP="$(date +%Y%m%d-%H%M%S)"
LOG_DIR="$ROOT/docs/verification/evidence"
mkdir -p "$LOG_DIR"
LOG="$LOG_DIR/build-$STAMP.log"
: >"$LOG"

FAILED=0
say()  { printf '%s\n' "$*" | tee -a "$LOG"; }
head_of() { say ""; say "=== $1 ==="; }
ok()   { say "ok   $1"; }
bad()  { say "FAIL $1"; FAILED=$((FAILED+1)); }

# run <nombre> <comando...>: ejecuta, vuelca la salida al log y anota.
run() {
  local name="$1"; shift
  head_of "$name"
  if "$@" >>"$LOG" 2>&1; then
    ok "$name"
  else
    bad "$name"
    tail -15 "$LOG" | sed 's/^/       /'
  fi
}

say "BOT.NET — compilacion completa ($STAMP)"
say "host=$(uname -s) go=$(go version 2>/dev/null | awk '{print $3}')"
say "GOTMPDIR=${GOTMPDIR:-}  GOFLAGS=${GOFLAGS:-}"

# 1. Suelo: si esto falla, el resto no aporta nada.
head_of "entorno"
if go build -p 1 ./... >>"$LOG" 2>&1; then
  ok "go build (suelo)"
else
  bad "go build (suelo)"
  tail -15 "$LOG" | sed 's/^/       /'
  say ""
  say "RESULTADO: FAIL ($FAILED comprobaciones) — detalle en ${LOG#$ROOT/}"
  exit 1
fi

# 2. Gate de implementacion: estructura, tasks DONE, verificacion 1:1 por
#    criterio, build, vet, suite por tandas y build del panel.
run "check-implementation" bash scripts/check-implementation.sh

if [ "$FAST" -eq 0 ]; then
  # 3. Matriz cross de 6 arquitecturas (deja dist/ y dist/raw/).
  run "matriz cross 6 archs" bash scripts/cross/build_matrix.sh
  # 4. Firma del manifiesto y verificacion contra la raiz fijada.
  run "firma del release" bash deploy/signing/sign_release.sh dist/ deploy/signing/keys/root_ed25519.pub
  run "verificacion del release" bash deploy/signing/verify_release.sh dist/manifest.json deploy/signing/keys/root_ed25519.pub dist/
  # 5. Humo de arranque (doble nativo sin qemu-user en el host).
  run "humo de arranque" bash scripts/qemu/smoke.sh
fi

# 6. Gates documentales.
run "check-blueprint" bash scripts/check-blueprint.sh
run "check-state-doc" bash scripts/check-state-doc.sh

say ""
if [ "$FAILED" -eq 0 ]; then
  say "RESULTADO: PASS — evidencia en ${LOG#$ROOT/}"
  exit 0
fi
say "RESULTADO: FAIL ($FAILED comprobaciones) — detalle en ${LOG#$ROOT/}"
exit 1
