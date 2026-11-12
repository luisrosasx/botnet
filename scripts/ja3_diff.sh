#!/usr/bin/env bash
# ja3_diff.sh — T-0408 AC3: dos builds/perfiles de hello presentan
# JA3/JA4 distintos. El assert vivo es el test Go que captura los
# ClientHello reales (bytes = version + suites + extensiones + curves,
# las piezas del JA3); este script lo ejecuta y publica el veredicto.
# Salida: "diff=true" (exito) o "diff=false" (fallo, exit 1).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if go test ./internal/tunnel -run TestClientHelloProfilesDiffer -count=1 >/dev/null 2>&1; then
	echo "diff=true"
else
	echo "diff=false"
	exit 1
fi
