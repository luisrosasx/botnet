#!/usr/bin/env bash
# cgnat_purge_100k — puerta T-1203: 100k conexiones simuladas, purge TCP
# cada 60 s, 24 h simuladas. El simulador es determinista y barato
# (segundos), asi que la puerta se corre entera en CI; el modo largo
# (QEMU + trafico real) reusa el mismo informe. Flags extra: "$@".
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh
go run scripts/soak/cgnat_purge_100k.go -nodes=100000 -hours=24 -report=scripts/soak/cgnat_report.md "$@"
