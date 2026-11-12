#!/usr/bin/env bash
# legba_run.sh — runner legba multi-protocolo (T-0201, EP-02).
# Ejecuta legba contra TARGET con el diccionario user:pass y normaliza la
# salida al veredicto JSON {protocol, cred_index, model_guess} (la lógica de
# parse vive en internal/intrusion vía `botnet-strike legba-parse`).
#
# Salida: JSON en stdout siempre (también en fallo: protocol null).
# Exit: 0 puerta abierta; 1 sin credencial válida; 2 error de uso/herramienta.
#
# Uso:
#   scripts/intrusion/legba_run.sh TARGET=10.70.0.5 [--timeout 20]
#       [--dict FILE] [--fixture FILE] [--banner FILE] [--protocols]
#
# Dobles para CI sin QEMU/legba:
#   --fixture FILE   lee FILE como salida capturada de legba (no ejecuta nada)
#   STRIKE_BIN=path  binario botnet-strike precompilado (default: PATH,
#                    dist/botnet-strike, o `go run ./cmd/botnet-strike`)
#   LEGBA_BIN=name   comando legba local (default: legba)
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DICT=""
FIXTURE=""
BANNER=""
TARGET=""
TIMEOUT=20
SHOW_PROTOCOLS=0

die() { echo "legba_run: $*" >&2; exit 2; }

strike() {
  if [ -n "${STRIKE_BIN:-}" ] && [ -x "${STRIKE_BIN}" ]; then
    "$STRIKE_BIN" "$@"
  elif command -v botnet-strike >/dev/null 2>&1; then
    botnet-strike "$@"
  elif [ -x "$ROOT/dist/botnet-strike" ]; then
    "$ROOT/dist/botnet-strike" "$@"
  elif command -v go >/dev/null 2>&1; then
    ( cd "$ROOT" && go run ./cmd/botnet-strike "$@" )   # vía lenta, ~2-4 s
  else
    die "helper botnet-strike no disponible (compílalo o exporta STRIKE_BIN)"
  fi
}

# args: flags y KEY=VALUE (TARGET=..., LEGBA=...) como pide el criterio.
while [ $# -gt 0 ]; do
  case "$1" in
    --protocols) SHOW_PROTOCOLS=1 ;;
    --timeout) shift; TIMEOUT="${1:?}" ;;
    --timeout=*) TIMEOUT="${1#*=}" ;;
    --dict) shift; DICT="${1:?}" ;;
    --dict=*) DICT="${1#*=}" ;;
    --fixture) shift; FIXTURE="${1:?}" ;;
    --fixture=*) FIXTURE="${1#*=}" ;;
    --banner) shift; BANNER="${1:?}" ;;
    --banner=*) BANNER="${1#*=}" ;;
    --*) die "flag desconocida $1" ;;
    *=*)
      key="${1%%=*}"; val="${1#*=}"
      case "$key" in
        TARGET) TARGET="$val" ;;
        LEGBA) LEGBA_BIN="$val" ;;
        DICT) DICT="$val" ;;
        *) die "clave desconocida $key" ;;
      esac ;;
    *) die "argumento desconocido $1" ;;
  esac
  shift
done

if [ "$SHOW_PROTOCOLS" = 1 ]; then
  strike protocols
  exit $?
fi

[ -n "$TARGET" ] || die "falta TARGET=<ip> (o --protocols)"
[ -n "$DICT" ] || DICT="$ROOT/scripts/intrusion/dictionaries/cred-defaults.txt"
[ -f "$DICT" ] || die "diccionario inexistente: $DICT"

# Diccionario user:pass -> dos ficheros (formato de entrada de legba).
USERS="$(mktemp)"; PASSES="$(mktemp)"; RAW="$(mktemp)"
trap 'rm -f "$USERS" "$PASSES" "$RAW"' EXIT
cut -d: -f1 "$DICT" > "$USERS"
cut -d: -f2- "$DICT" > "$PASSES"

if [ -n "$FIXTURE" ]; then
  [ -f "$FIXTURE" ] || die "fixture inexistente: $FIXTURE"
  cat "$FIXTURE" > "$RAW"
else
  LEGBA="${LEGBA_BIN:-legba}"
  command -v "$LEGBA" >/dev/null 2>&1 || die "legba no instalado ($LEGBA); usa --fixture para CI"
  PROTO_CSV="$(strike protocols | paste -sd, -)"
  # timeout duro por encima del timeout propio de legba: tope de 30 s del
  # criterio y cero procesos colgados si legba se atasca en el QEMU.
  if command -v timeout >/dev/null 2>&1; then
    timeout -k 2 $(( TIMEOUT + 10 )) \
      "$LEGBA" --protocols "$PROTO_CSV" --target "$TARGET" \
      --user-file "$USERS" --pass-file "$PASSES" --timeout "$TIMEOUT" \
      --output-format json > "$RAW" 2>/dev/null
  else
    "$LEGBA" --protocols "$PROTO_CSV" --target "$TARGET" \
      --user-file "$USERS" --pass-file "$PASSES" --timeout "$TIMEOUT" \
      --output-format json > "$RAW" 2>/dev/null
  fi
fi

if [ -n "$BANNER" ]; then
  strike legba-parse --banner "$BANNER" < "$RAW"
else
  strike legba-parse < "$RAW"
fi
exit $?
