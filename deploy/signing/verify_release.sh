#!/usr/bin/env bash
# T-1403: verifica el manifest.json firmado contra la raiz fijada
# (deploy/signing/keys/root_ed25519.pub). Salida esperada: "verify: OK ...".
# Alterar 1 byte del sig del manifest => exit != 0 (el MITM no sirve el
# binario que el mismo hasheo). Con DIST_DIR ademas coteja sha256 por arch.
# Uso: verify_release.sh [MANIFEST] [ROOT_PUB] [DIST_DIR]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
[ -f scripts/env.sh ] && source scripts/env.sh

MANIFEST="${1:-dist/manifest.json}"
ROOT_PUB="${2:-deploy/signing/keys/root_ed25519.pub}"
DIST_DIR="${3:-}"

[ -f "$MANIFEST" ] || { echo "verify_release: no existe $MANIFEST (corre sign primero)" >&2; exit 1; }
[ -f "$ROOT_PUB" ] || { echo "verify_release: no existe la raiz $ROOT_PUB" >&2; exit 1; }

go run ./cmd/botnet-build verify -manifest "$MANIFEST" -root "$ROOT_PUB" -dist "$DIST_DIR"
echo "verify_release: OK"
