#!/usr/bin/env bash
# loss_soak.sh — T-0906 (A2-02): soak de mux compartido con perdida
# medida. Simula sesiones de 3 tenants bajo netem loss (SOAK_LOSS_PCT,
# por defecto 1,5 %) y aserta la condicion R5: p95 de throughput por
# sesion independiente entre sesiones (ratio max/min p95 <= 1,2).
# Deja docs/verification/loss_soak_report.json (veredicto incluido).
# Si la asercion falla, el producto se declara «mux compartido» en
# docs/runbooks/billing.md y el repricing del GB queda registrado — el
# veredicto del criterio 3, no una excusa. Salida 0 = aislamiento; 1 =
# head-of-line detectado (declarar mux compartido).
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

export SOAK_LOSS_PCT="${SOAK_LOSS_PCT:-1.5}"
echo "loss_soak: netem loss ${SOAK_LOSS_PCT} %, 3 tenants x 10 sesiones, 600 s"
if ! go test ./cmd/botnet-gw/ -run 'TestLossSoakReport|TestLossDrivenConns' -count=1 -v; then
  echo "loss_soak: RATIO FUERA DE 1,2 -> declarar «mux compartido» y repricing del GB" >&2
  exit 1
fi
cat docs/verification/loss_soak_report.json
