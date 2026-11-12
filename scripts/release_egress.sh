#!/usr/bin/env bash
# T-1406: presupuesto de egress de una release a la flota (BLUEPRINT §2,
# A1-13). Un solo binario por arch: el egress maximo por release es
# N x bytes_binario (perfil A tope 4,5 MB/nodo). El script estima
# `--nodes=N` y comprueba: egress total <= 500 GB y bytes por nodo <= 4,5 MB.
# La marca real por release (medida en dist/) manda sobre el worst case.
# Uso: release_egress.sh --nodes=100000 [--binary=dist/botd-amd64]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
[ -f scripts/env.sh ] && source scripts/env.sh

NODES=100000
BINARY="${BINARY:-dist/botd-amd64}"
MAX_TOTAL_GB=500
MAX_PER_NODE_MB=4.5

for arg in "$@"; do
  case "$arg" in
    --nodes=*) NODES="${arg#--nodes=}" ;;
    --binary=*) BINARY="${arg#--binary=}" ;;
    *) echo "uso: release_egress.sh --nodes=N [--binary=path]" >&2; exit 2 ;;
  esac
done

if [ -f "$BINARY" ]; then
  per_node_bytes=$(wc -c <"$BINARY" | tr -d ' ')
  source_note="medido en $BINARY"
else
  # worst case del presupuesto (perfil A): 4,5 MB por nodo.
  per_node_bytes=$(awk 'BEGIN{printf "%d", 4.5*1000000}')
  source_note="worst case 4,5 MB ($BINARY no compilado: corre make cross)"
fi

awk -v n="$NODES" -v b="$per_node_bytes" -v note="$source_note" \
    -v maxtot="$MAX_TOTAL_GB" -v maxnode="$MAX_PER_NODE_MB" '
BEGIN {
  gb = n * b / 1e9            # GB decimales (1 GB = 1e9 B)
  node_mb = b / 1e6
  printf "release_egress: nodes=%d por_nodo=%.2f MB total=%.1f GB (%s)\n", n, node_mb, gb, note
  ok = 1
  if (node_mb > maxnode + 1e-9) { printf "FAIL: %.2f MB/nodo > %.1f MB (presupuesto por nodo)\n", node_mb, maxnode; ok = 0 }
  if (gb > maxtot + 1e-6) { printf "FAIL: %.1f GB > %d GB (presupuesto total de release)\n", gb, maxtot; ok = 0 }
  exit (ok ? 0 : 1)
}'
echo "release_egress: OK (1 binario por arch; la pesadilla P2 de ~150 GB por modulo no aplica)"
