#!/usr/bin/env bash
# netem/apply.sh — EP-02 T-0202 AC3 y EP-09: perdida/delay REALES de tc
# netem sobre la interfaz del nodo (docs/verification/EP-02.md). En el host
# F4 (Windows) no hay `tc`: aqui se valida la GENERACION de la orden y el
# parseo de `tc qdisc show`; la aplicacion real ocurre en los nodos Linux.
#
# Uso:
#   bash scripts/netem/apply.sh --iface <if> --loss <pct> [--delay-ms N] \
#     [--jitter-ms N] [--corrupt <pct>] [--target <ssh-host>|local] [--dry] \
#     [--check] [--json] [--help]
#
# Como se aplica:
#   - `tc qdisc add dev <if> root netem …` y, si el qdisc root ya existe
#     (RTNETLINK "File exists"), `tc qdisc replace` sobre el mismo root: la
#     regla NUNCA se duplica y la norma queda idempotente.
#   - --jitter-ms solo tiene sentido con --delay-ms (netem lo exige).
#   - --check lee `tc qdisc show dev <if>` y confirma que el root es netem
#     con el loss pedido. Sin --loss, --check solo comprueba el netem.
#   - El qdisc vive en el OBJETIVO (--target); en local sin `tc` (este host)
#     sale 3 con mensaje claro, que es el modo esperado en el host F4.
#
# Salidas: 0 aplicado y/o verificado; 1 tc fallo o la regla no esta;
# 2 argumentos invalidos; 3 falta tc (o ssh para el objetivo remoto).
set -uo pipefail

SCRIPT_TAG=netem/apply

usage() {
  cat <<'EOF'
netem/apply.sh — aplica tc netem (perdida, delay, jitter, corrupcion) en la interfaz.

Uso:
  bash scripts/netem/apply.sh --iface <if> --loss <pct> [opciones]

Opciones:
  --iface <if>        interfaz del nodo (obligatoria: nunca se aplica sin ella)
  --loss <pct>        perdida en % (0-100, admite decimales)
  --delay-ms N        retardo en ms
  --jitter-ms N       variacion del retardo en ms (requiere --delay-ms)
  --corrupt <pct>     corrupcion en %
  --target <ssh|local> donde vive la interfaz (defecto local)
  --dry               imprime los comandos tc y no ejecuta nada
  --check             verifica con `tc qdisc show dev <if>` (sin --loss solo comprueba netem)
  --json              salida JSON en una linea
  -h, --help          esta ayuda

Salidas: 0 aplicado/verificado | 1 tc fallo o regla ausente | 2 argumentos | 3 tc o ssh ausente.
Variables: TC_BIN, SSH_BIN (dobles para pruebas sin la herramienta real).
EOF
}

IFACE=""
LOSS=""
DELAY=""
JITTER=""
CORRUPT=""
TARGET=local
DRY=0
CHECK=0
JSON=0

die() {
  printf '%s: %s\n' "$SCRIPT_TAG" "$1" >&2
  exit "${2:-2}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --iface)     [ $# -ge 2 ] || die '--iface necesita un valor'; IFACE="$2"; shift 2 ;;
    --iface=*)   IFACE="${1#*=}"; shift ;;
    --loss)      [ $# -ge 2 ] || die '--loss necesita un valor'; LOSS="$2"; shift 2 ;;
    --loss=*)    LOSS="${1#*=}"; shift ;;
    --delay-ms)  [ $# -ge 2 ] || die '--delay-ms necesita un valor'; DELAY="$2"; shift 2 ;;
    --delay-ms=*) DELAY="${1#*=}"; shift ;;
    --jitter-ms) [ $# -ge 2 ] || die '--jitter-ms necesita un valor'; JITTER="$2"; shift 2 ;;
    --jitter-ms=*) JITTER="${1#*=}"; shift ;;
    --corrupt)   [ $# -ge 2 ] || die '--corrupt necesita un valor'; CORRUPT="$2"; shift 2 ;;
    --corrupt=*) CORRUPT="${1#*=}"; shift ;;
    --target)    [ $# -ge 2 ] || die '--target necesita un valor'; TARGET="$2"; shift 2 ;;
    --target=*)  TARGET="${1#*=}"; shift ;;
    --dry)       DRY=1; shift ;;
    --check)     CHECK=1; shift ;;
    --json)      JSON=1; shift ;;
    -h|--help)   usage; exit 0 ;;
    *)           die "argumento desconocido: $1" 2 ;;
  esac
done

# Nunca se aplica sin interfaz: es el unico ancla de la regla.
[ -n "$IFACE" ] || die 'falta --iface <if> (usa --help)' 2
[[ "$IFACE" =~ ^[A-Za-z0-9._:-]+$ ]] || die '--iface debe ser un nombre de interfaz (sin espacios)' 2
is_pct() { printf '%s' "$1" | LC_ALL=C awk '{ if ($0 ~ /^[0-9]+([.][0-9]+)?$/) exit 0; exit 1 }'; }
if [ -n "$LOSS" ]; then
  is_pct "$LOSS" || die '--loss debe ser un numero entre 0 y 100' 2
  LC_ALL=C awk -v v="$LOSS" 'BEGIN{ exit (v >= 0 && v <= 100) ? 0 : 1 }' ||
    die '--loss debe estar entre 0 y 100' 2
fi
if [ -n "$CORRUPT" ]; then
  is_pct "$CORRUPT" || die '--corrupt debe ser un numero entre 0 y 100' 2
  LC_ALL=C awk -v v="$CORRUPT" 'BEGIN{ exit (v >= 0 && v <= 100) ? 0 : 1 }' ||
    die '--corrupt debe estar entre 0 y 100' 2
fi
for par in DELAY JITTER; do
  val=${!par}
  [ -z "$val" ] || [[ "$val" =~ ^[0-9]+$ ]] || die "--delay-ms y --jitter-ms deben ser enteros >= 0" 2
done
[ -n "$JITTER" ] && [ -z "$DELAY" ] && die '--jitter-ms necesita --delay-ms (netem solo aplica jitter sobre un delay)' 2
[ -n "$LOSS" ] || [ "$CHECK" = 1 ] || die 'falta --loss <pct> (o usa --check para solo verificar)' 2

TC_BIN="${TC_BIN:-tc}"
SSH_BIN="${SSH_BIN:-ssh}"
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=8)

# --- especificacion netem -------------------------------------------------
SPEC="netem"
[ -n "$LOSS" ] && SPEC="$SPEC loss ${LOSS}%"
if [ -n "$DELAY" ]; then
  SPEC="$SPEC delay ${DELAY}ms"
  [ -n "$JITTER" ] && SPEC="$SPEC ${JITTER}ms"
fi
[ -n "$CORRUPT" ] && SPEC="$SPEC corrupt ${CORRUPT}%"

ADD_CMD="$TC_BIN qdisc add dev $IFACE root $SPEC"
REPL_CMD="$TC_BIN qdisc replace dev $IFACE root $SPEC"
APPLY_CMD="$ADD_CMD 2>/dev/null || $REPL_CMD"
SHOW_CMD="$TC_BIN qdisc show dev $IFACE"

run_cmd() {
  # Ejecuta una orden en el objetivo: local o por ssh (una sola linea).
  if [ "$TARGET" = local ]; then
    bash -c "$1"
  else
    "$SSH_BIN" "${SSH_OPTS[@]}" "$TARGET" "$1"
  fi
}

if [ "$DRY" = 1 ]; then
  printf '# netem apply: objetivo=%s iface=%s%s\n' "$TARGET" "$IFACE" \
    "$([ "$CHECK" = 1 ] && printf ' modo=aplicar+verificar' || printf ' modo=aplicar')"
  if [ -n "$LOSS" ]; then
    printf 'TC: %s\n' "$APPLY_CMD"
  else
    printf 'TC: (sin --loss: no se aplica, solo se verifica)\n'
  fi
  [ "$CHECK" = 1 ] && printf 'CHECK: %s\n' "$SHOW_CMD"
  exit 0
fi

if [ "$TARGET" != local ] && ! command -v "$SSH_BIN" >/dev/null 2>&1; then
  printf '%s: ssh no disponible para el objetivo %s\n' "$SCRIPT_TAG" "$TARGET" >&2
  exit 3
fi

# tc se busca DONDE se aplica: en el nodo remoto o en este host.
if [ "$TARGET" = local ]; then
  TC_FOUND=$(command -v "$TC_BIN" 2>/dev/null)
else
  TC_FOUND=$("$SSH_BIN" "${SSH_OPTS[@]}" "$TARGET" "command -v $TC_BIN" 2>/dev/null)
fi
if [ -z "${TC_FOUND:-}" ]; then
  printf '%s: tc no disponible en %s: la regla netem no se puede aplicar aqui (probando con --dry)\n' \
    "$SCRIPT_TAG" "$TARGET" >&2
  exit 3
fi

if [ -n "$LOSS" ]; then
  if ! run_cmd "$APPLY_CMD"; then
    printf '%s: tc fallo aplicando %s en %s (permisos CAP_NET_ADMIN? existe %s?)\n' \
      "$SCRIPT_TAG" "$SPEC" "$IFACE" "$IFACE" >&2
    exit 1
  fi
  APPLIED=true
else
  APPLIED=false
fi

SHOW_OUT=""
CHECK_PRESENT=false
if [ "$CHECK" = 1 ]; then
  SHOW_OUT=$(run_cmd "$SHOW_CMD" 2>/dev/null | LC_ALL=C tr -d '\r')
  # Se mira la linea del qdisc ROOT (no cualquier netem suelto de un ingress).
  # tc solo imprime los parametros no por defecto: sin `loss` en el volcado
  # la perdida real es 0, y ese es el valor a comparar.
  CHECK_PRESENT=$(printf '%s\n' "$SHOW_OUT" | LC_ALL=C awk -v want="$LOSS" '
    /root/ && /netem/ {
      es_netem = 1
      if (match($0, /loss [0-9.]+%/)) { have = substr($0, RSTART + 5, RLENGTH - 6) + 0 }
      else { have = 0 }
    }
    END {
      if (!es_netem) { print "no"; exit }
      if (want == "") { print "si"; exit }
      print ((have + 0) == (want + 0)) ? "si" : "no"
    }')
  [ "$CHECK_PRESENT" = si ] && CHECK_PRESENT=true || CHECK_PRESENT=false
fi

EXIT_CODE=0
if [ "$CHECK" = 1 ] && [ "$CHECK_PRESENT" != true ]; then
  EXIT_CODE=1
fi

if [ "$JSON" = 1 ]; then
  SHOW_JSON=$(printf '%s' "$SHOW_OUT" | LC_ALL=C tr '\n' ' ' | LC_ALL=C sed 's/ *$//')
  printf '{"target":"%s","iface":"%s","spec":"%s","applied":%s,"check":%s,"check_present":%s,"tc_bin":"%s","qdisc":"%s","verdict":"%s"}\n' \
    "$TARGET" "$IFACE" "$SPEC" "$APPLIED" \
    "$([ "$CHECK" = 1 ] && printf true || printf false)" "$CHECK_PRESENT" "$TC_FOUND" \
    "$SHOW_JSON" "$([ "$EXIT_CODE" = 0 ] && printf OK || printf FALLO)"
else
  printf '%s: objetivo=%s iface=%s tc=%s spec=%s\n' "$SCRIPT_TAG" "$TARGET" "$IFACE" "$TC_FOUND" "$SPEC"
  [ -n "$SHOW_OUT" ] && printf '%s: qdisc actual=%s\n' "$SCRIPT_TAG" \
    "$(printf '%s' "$SHOW_OUT" | LC_ALL=C tr '\n' ' ' | LC_ALL=C sed 's/ *$//')"
  printf '%s: %s\n' "$SCRIPT_TAG" \
    "$([ "$EXIT_CODE" = 0 ] && printf 'regla netem OK' || printf 'regla netem NO verificada')"
fi

exit "$EXIT_CODE"
