#!/usr/bin/env bash
# T-1403: firma la release de dist/ con la raiz ed25519 y escribe
# dist/manifest.json con {version, sha256, url, sig} por arch. La clave
# privada vive en el keydir del builder (deploy/signing/keys) y jamas viaja
# al nodo; si la raiz no existe, se genera una vez (genroot no pisa una
# existente: perder la raiz invalida releases pasadas).
# Uso: sign_release.sh [DIST_DIR] [ROOT_PUB]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
[ -f scripts/env.sh ] && source scripts/env.sh

DIST_DIR="${1:-dist}"
ROOT_PUB="${2:-deploy/signing/keys/root_ed25519.pub}"
KEYDIR="${KEYDIR:-$(dirname "$ROOT_PUB")}"
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo 0.1.0-dev)}"

if [ ! -f "$ROOT_PUB" ]; then
  go run ./cmd/botnet-build genroot -keydir "$KEYDIR"
fi
[ -f "$KEYDIR/root_ed25519.key" ] || {
  echo "sign_release: falta la clave privada en $KEYDIR (genroot solo crea pares nuevos)" >&2
  exit 1
}

go run ./cmd/botnet-build sign -dist "$DIST_DIR" -keydir "$KEYDIR" \
  -out "$DIST_DIR/manifest.json" -version "$VERSION"
test -s "$DIST_DIR/manifest.json"
echo "sign_release: OK $DIST_DIR/manifest.json version=$VERSION"
