#!/usr/bin/env bash
# Matriz de build F1 (BLUEPRINT parrafo 15): 5 archs con binario estatico <= 4 MB.
# CGO_ENABLED=0 y -ldflags "-s -w" siempre; UPX opcional con UPX=1 y binario
# `upx` en PATH (ausente => se omite, no es fallo). Sale 1 si algun binario
# pasa del tope: es la puerta de F1, no un aviso.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
[ -f scripts/env.sh ] && source scripts/env.sh

BINARY="${1:-botd}"
VERSION="$(git describe --tags --always 2>/dev/null || echo 0.1.0-dev)"
LDFLAGS="-s -w -X main.version=${VERSION}"
# T-1403 AC2: la raiz ed25519 de update se fija en build (botd
# --print-root). El .pub ya es base64 de los 32 B crudos: se inyecta
# byte a byte (RootFromBase64 lo decodea). Solo al agente; sin clave
# publica, var vacia = binario sin raiz (rechazo antes de ejecutar).
if [ "$BINARY" = "botd" ] && [ -f deploy/signing/keys/root_ed25519.pub ]; then
  ROOT_XLFLAG="-X github.com/botnet/botnet/internal/agent.RootPubKeyBase64=$(tr -d '\r\n' <deploy/signing/keys/root_ed25519.pub)"
  LDFLAGS="$LDFLAGS $ROOT_XLFLAG"
fi
MAX_BYTES=$((4 * 1024 * 1024))
USE_UPX="${UPX:-1}"
DIST="$ROOT/dist"
mkdir -p "$DIST"

# GOARCH: mips (be), mipsle, arm (GOARM=7 => armv7), arm64, amd64.
ARCHS="mips mipsle arm arm64 amd64"
fail=0
built=0
for arch in $ARCHS; do
  out="$DIST/$BINARY-linux-$arch"
  envargs=(GOOS=linux "GOARCH=$arch" CGO_ENABLED=0)
  [ "$arch" = "arm" ] && envargs+=(GOARM=7)
  env "${envargs[@]}" go build -trimpath -ldflags "$LDFLAGS" -o "$out" "./cmd/$BINARY"
  if [ "$USE_UPX" = "1" ] && command -v upx >/dev/null 2>&1; then
    upx -9 -q "$out" >/dev/null
  fi
  size=$(wc -c <"$out")
  mb=$(awk -v s="$size" 'BEGIN{printf "%.2f", s/1048576}')
  if [ "$size" -gt "$MAX_BYTES" ]; then
    printf 'FAIL %-6s %10d B (%s MB) > tope 4 MB\n' "$arch" "$size" "$mb"
    fail=1
  else
    printf 'ok   %-6s %10d B (%s MB)\n' "$arch" "$size" "$mb"
    built=$((built + 1))
  fi
done
if [ "$fail" -ne 0 ]; then
  echo "matriz FAIL: algun $BINARY supera 4 MB (puerta F1)"
  exit 1
fi
echo "matriz OK: $built/5 binarios $BINARY bajo el tope -> dist/"
