#!/usr/bin/env bash
# netem/clear.sh — retira la regla netem del root de la interfaz, dejando el
# qdisc por defecto del kernel (noqueue/pfifo_fast). Es el contrapunto de
# netem/apply.sh: despues de medir perdida real (EP-02 T-0202 AC3, EP-09) el
# nodo vuelve a su estado limpio.
#
# Uso:
#   bash scripts/netem/clear.sh --iface <if> [--target <ssh-host>|local] [--dry] [--json] [--help]
#
# Idempotencia: `tc qdisc del dev <if> root || true` — si no hay qdisc que
# quitar (recien arranque, o doble limpieza) NO es un fallo: borrar netem
# siempre sale 0, salvo que falte la herramienta o la interfaz.
#
# Salidas: 0 siempre que la orden se ejecute (o --dry); 2 argumentos
# invalidos; 3 falta tc (o ssh para el objetivo remoto).
set -uo pipefail

SCRIPT_TAG=netem/clear

usage() {
  cat <<'EOF'
netem/clear.sh — quita el qdisc netem del root de la interfaz.

Uso:
  bash scripts/netem/clear.sh --iface <if> [opciones]

Opciones:
  --iface <if>          interfaz del nodo (obligatoria)
  --target <ssh|local>  donde vive la interfaz (defecto local)
  --dry                 imprime el comando tc y no ejecuta nada
  --json                salida JSON en una linea
  -h, --help            esta ayuda

Salidas: 0 borrado (o nada que borrar) | 2 argumentos | 3 tc o ssh ausente.
Variables: TC_BIN, SSH_BIN (dobles para pruebas sin la herramienta real).
EOF
}

IFACE=""
TARGET=local
DRY=0
JSON=0

die() {
  printf '%s: %s\n' "$SCRIPT_TAG" "$1" >&2
  exit "${2:-2}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --iface)   [ $# -ge 2 ] || die '--iface necesita un valor'; IFACE="$2"; shift 2 ;;
    --iface=*) IFACE="${1#*=}"; shift ;;
    --target)  [ $# -ge 2 ] || die '--target necesita un valor'; TARGET="$2"; shift 2 ;;
    --target=*) TARGET="${1#*=}"; shift ;;
    --dry)     DRY=1; shift ;;
    --json)    JSON=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *)         die "argumento desconocido: $1" 2 ;;
  esac
done

[ -n "$IFACE" ] || die 'falta --iface <if> (usa --help)' 2
[[ "$IFACE" =~ ^[A-Za-z0-9._:-]+$ ]] || die '--iface debe ser un nombre de interfaz (sin espacios)' 2

TC_BIN="${TC_BIN:-tc}"
SSH_BIN="${SSH_BIN:-ssh}"
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=8)

DEL_CMD="$TC_BIN qdisc del dev $IFACE root 2>/dev/null || true"
SHOW_CMD="$TC_BIN qdisc show dev $IFACE"

if [ "$DRY" = 1 ]; then
  printf '# netem clear: objetivo=%s iface=%s\n' "$TARGET" "$IFACE"
  printf 'TC: %s\n' "$DEL_CMD"
  printf 'CHECK: %s\n' "$SHOW_CMD"
  exit 0
fi

if [ "$TARGET" != local ] && ! command -v "$SSH_BIN" >/dev/null 2>&1; then
  printf '%s: ssh no disponible para el objetivo %s\n' "$SCRIPT_TAG" "$TARGET" >&2
  exit 3
fi

if [ "$TARGET" = local ]; then
  TC_FOUND=$(command -v "$TC_BIN" 2>/dev/null)
else
  TC_FOUND=$("$SSH_BIN" "${SSH_OPTS[@]}" "$TARGET" "command -v $TC_BIN" 2>/dev/null)
fi
if [ -z "${TC_FOUND:-}" ]; then
  printf '%s: tc no disponible en %s: no hay qdisc netem que retirar aqui\n' "$SCRIPT_TAG" "$TARGET" >&2
  exit 3
fi

run_cmd() {
  if [ "$TARGET" = local ]; then
    bash -c "$1"
  else
    "$SSH_BIN" "${SSH_OPTS[@]}" "$TARGET" "$1"
  fi
}

run_cmd "$DEL_CMD"
RESIDUAL=$(run_cmd "$SHOW_CMD" 2>/dev/null | LC_ALL=C tr -d '\r' | \
  LC_ALL=C awk '/netem/ { print; found = 1 } END { if (!found) print "" }')

if [ -n "${RESIDUAL:-}" ]; then
  VERDICT=QUEDA_NETEM
  EXIT_CODE=1
else
  VERDICT=LIMPIO
  EXIT_CODE=0
fi

if [ "$JSON" = 1 ]; then
  printf '{"target":"%s","iface":"%s","cmd":"%s","residual_netem":"%s","verdict":"%s"}\n' \
    "$TARGET" "$IFACE" "$DEL_CMD" "$RESIDUAL" "$VERDICT"
else
  printf '%s: objetivo=%s iface=%s tc=%s -> %s\n' "$SCRIPT_TAG" "$TARGET" "$IFACE" "$TC_FOUND" \
    "$([ "$EXIT_CODE" = 0 ] && printf 'root sin netem' || printf 'aun queda netem: %s' "$RESIDUAL")"
fi

exit "$EXIT_CODE"
