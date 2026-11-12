#!/usr/bin/env bash
# T-0104 — clasificacion de banners estilo zgrab2.
#
# Uso:
#   scripts/discovery/zgrab2_grab.sh --input=testdata/banners/hosts.txt [--emit] [--dry]
#   GRAB_FIXTURE=f  doble sin zgrab2 instalado (JSONL {ip,port,banner})
#   ZGRAB2_BIN=f    binario real: `zgrab2 run --targets f --output f`
#   --score=f       modo scoring sobre corpus etiquetado (T-0104.2):
#                   exit != 0 si accuracy < --threshold (def 0.70)
#   DATABASE_URL=   upsert de vendor/model/device_class_guess/banner_hash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

input="" score="" threshold="0.70" emit="" dry=""
for a in "$@"; do
  case "$a" in
    --input=*)  input="${a#--input=}" ;;
    --score=*)  score="${a#--score=}" ;;
    --threshold=*) threshold="${a#--threshold=}" ;;
    --emit)     emit=1 ;;
    --dry)      dry=1 ;;
    *) echo "zgrab2_grab: opcion desconocida $a" >&2; exit 2 ;;
  esac
done

if [[ -n "$score" ]]; then
  go run ./cmd/botnet-discover classify --score="$score" --threshold="$threshold"
  exit $?
fi

[[ -n "$input" ]] || { echo "zgrab2_grab: --input requerido" >&2; exit 2; }

grabs="${GRAB_FIXTURE:-}"
z2out=""
cleanup() { if [[ -n "$z2out" && -f "$z2out" ]]; then rm -f "$z2out"; fi; }
trap cleanup EXIT

if [[ -z "$grabs" ]]; then
  if [[ -n "${ZGRAB2_BIN:-}" ]] || command -v zgrab2 >/dev/null 2>&1; then
    bin="${ZGRAB2_BIN:-zgrab2}"
    z2out="$(mktemp "${TMPDIR:-/tmp}/zgrab2.XXXXXX.json")"
    # 5 s por handshakes con routers viejos; el input ya trae ip:puerto.
    "$bin" run --targets "$input" --ports=all --max-size=4096 \
      --connect-timeout=5 --read-timeout=5 --output "$z2out" >/dev/null
    go run ./cmd/botnet-discover classify --zgrab2="$z2out" \
      ${emit:+--emit} ${dry:+--dry} \
      ${DATABASE_URL:+--db="$DATABASE_URL"}
    exit $?
  fi
  echo "zgrab2_grab: zgrab2 ausente; exporta GRAB_FIXTURE=testdata/banners/grabs.jsonl" >&2
  exit 3
fi

go run ./cmd/botnet-discover classify --grabs="$grabs" \
  ${emit:+--emit} ${dry:+--dry} \
  ${DATABASE_URL:+--db="$DATABASE_URL"}
