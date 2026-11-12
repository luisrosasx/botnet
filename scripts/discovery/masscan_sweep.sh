#!/usr/bin/env bash
# T-0103 — barrido propio con masscan sobre ASNs candidatos.
#
# Uso:
#   scripts/discovery/masscan_sweep.sh --range=1.0.0.0/16 --ports=23,80,8080,554,32210
# Opciones:
#   --rate=N    pps maximo (def: 10000/cores, tope del runbook ~5 Mbps
#               con SYN de 44 B; ver docs/runbooks/discovery.md)
#   SWEEP_JSON=f  doble sin masscan instalado: parsea NDJSON existente
#   MASSCAN_BIN=f binario de masscan (def: el del PATH)
#   DATABASE_URL= destino; vacio => modo dry (cuenta y extrapolacion)
#
# masscan exige root en Linux (raw sockets): en host Windows se corre
# con SWEEP_JSON y la extrapolacion de tiempo queda documentada.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

range="" ports="23,80,8080,554,32210" rate=""
for a in "$@"; do
  case "$a" in
    --range=*) range="${a#--range=}" ;;
    --ports=*) ports="${a#--ports=}" ;;
    --rate=*)  rate="${a#--rate=}" ;;
    *) echo "masscan_sweep: opcion desconocida $a" >&2; exit 2 ;;
  esac
done

cores="$(nproc 2>/dev/null || echo 4)"
rate="${rate:-$((10000 / cores))}"
[[ $rate -lt 1500 ]] && rate=1500

out=""
sweep_tmp=""
cleanup() { if [[ -n "$sweep_tmp" && -f "$sweep_tmp" ]]; then rm -f "$sweep_tmp"; fi; }
trap cleanup EXIT

if [[ -n "${SWEEP_JSON:-}" ]]; then
  out="$SWEEP_JSON"
  echo "masscan_sweep: doble fixture $out (range=${range:-n/a} ports=$ports rate=$rate pps)"
elif command -v "${MASSCAN_BIN:-masscan}" >/dev/null 2>&1 || [[ -n "${MASSCAN_BIN:-}" ]]; then
  bin="${MASSCAN_BIN:-masscan}"
  sweep_tmp="$(mktemp "${TMPDIR:-/tmp}/bansweep.XXXXXX.ndjson")"
  out="$sweep_tmp"
  # -Pn sin prearpeo de respuesta: SYN-ACK es evidencia suficiente.
  "$bin" "$range" --min-rate="$rate" --max-rate="$rate" --delay=1000 \
    --ports="$ports" --output-format json -o "$out" >/dev/null
  echo "masscan_sweep: masscan $range puertos $ports a $rate pps"
else
  echo "masscan_sweep: masscan ausente y SWEEP_JSON vacio (exporta SWEEP_JSON=fixture)" >&2
  exit 3
fi

if [[ -z "${DATABASE_URL:-}" ]]; then
  go run ./cmd/botnet-discover sweep-parse --json="$out" --dry
else
  go run ./cmd/botnet-discover sweep-parse --json="$out" --db="$DATABASE_URL"
fi
