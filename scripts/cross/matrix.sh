#!/usr/bin/env bash
# matrix.sh — las 6 archs del perfil matriz compiladas (T-0204 criterio 3).
# Puerta F1: 6 binarios estáticos en < 10 min; delega flags y presupuestos a
# deploy/sliver-builder/build.sh (una sola verdad de la matriz). Es lo que
# invoca `make cross` cuando el Makefile del suelo tenga el target; en esta
# máquina `make` no está instalado, se ejecuta este script directamente.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST="${DIST:-$ROOT/dist}"
NAME="${NAME:-botd}"
BUILD="$ROOT/deploy/sliver-builder/build.sh"
TIMEOUT_S="${TIMEOUT_S:-600}"

# Archs de la matriz (BLUEPRINT §4): mipsbe mipsle armv5 armv7 arm64 amd64.
ARCHS="mipsbe mipsle armv5 armv7 arm64 amd64"
profile_of() {
  case "$1" in
    amd64 | arm64) echo A ;;
    armv5 | armv7) echo B ;;
    mipsbe | mipsle) echo C ;;
  esac
}

start=$(date -u +%s)
built=0
for arch in $ARCHS; do
  DIST="$DIST" bash "$BUILD" --arch="$arch" --profile="$(profile_of "$arch")" --name="$NAME" --dist="$DIST"
  built=$(( built + 1 ))
done
elapsed=$(( $(date -u +%s) - start ))

if [ "$elapsed" -gt "$TIMEOUT_S" ]; then
  echo "cross FAIL: $built/6 archs en ${elapsed}s > presupuesto ${TIMEOUT_S}s" >&2
  exit 1
fi
echo "cross OK: $built/6 binarios $NAME en ${elapsed}s (presupuesto ${TIMEOUT_S}s) -> $DIST"
