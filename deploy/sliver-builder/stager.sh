#!/bin/sh
# stager.sh — entrega del binario al dispositivo (T-0205/T-0206, EP-02).
# Copia src -> destino con reintentos y jitter, decide destino por clase de
# flash (clase C u overlay < 8 MB -> /tmp tmpfs-only, exigiendo ≥ 4 MB libres),
# lanza `botd --stage` y muere: cero procesos huérfanos tras el finish.
#
# POSIX sh a propósito: busybox ash en el router de 2009.
#
# Uso:
#   stager.sh --src=dist/botd-mipsle --profile=C [--name=botd]
#             [--dest-dir=/data/botd] [--overlay-free-kb=6000]
#             [--retries=3] [--backoff-base-s=2] [--jitter-range-s=10]
#             [--log=stager.log] [--fail-copies=N] [--stage-cmd=CMD]
#
# Exit: 0 entregado y lanzado; 2 reintentos agotados;
#       4 tope de flash (< 4 MB libres); 5 falta src.
#
# Dobles para CI sin QEMU/tc:
#   --fail-copies=N   las N primeras copias se corrompen a propósito
#                     (doble de `tc netem loss 5%`; sha256 lo detecta)
#   --stage-cmd=CMD   comando que sustituye a "$DEST --stage" (ELF no
#                     ejecutable en la máquina de test)
set -u

log() { # ts epoch | mensaje — los ts permiten medir jitter entre runs (criterio 3)
  printf 'ts=%s %s\n' "$(date -u +%s)" "$*" >> "$LOG"
}

jitter_s() { # entero en [0,range]: busybox sleep sin fracciones
  awk -v r="$JITTER_RANGE" -v s="$(( ${RANDOM:-0} * 32768 + $$ ))" \
    'BEGIN { srand(s); printf "%d", int(rand() * (r + 1)) }'
}

SRC="" PROFILE="" NAME="botd" DEST_DIR="" OVERLAY_KB="" RETRIES=3
BACKOFF_BASE=2 JITTER_RANGE=10 LOG="./stager.log" FAIL_COPIES=0 STAGE_CMD=""
DRY=0

while [ $# -gt 0 ]; do
  case "$1" in
    --src=*) SRC="${1#*=}" ;;
    --profile=*) PROFILE="${1#*=}" ;;
    --name=*) NAME="${1#*=}" ;;
    --dest-dir=*) DEST_DIR="${1#*=}" ;;
    --overlay-free-kb=*) OVERLAY_KB="${1#*=}" ;;
    --retries=*) RETRIES="${1#*=}" ;;
    --backoff-base-s=*) BACKOFF_BASE="${1#*=}" ;;
    --jitter-range-s=*) JITTER_RANGE="${1#*=}" ;;
    --log=*) LOG="${1#*=}" ;;
    --fail-copies=*) FAIL_COPIES="${1#*=}" ;;
    --stage-cmd=*) STAGE_CMD="${1#*=}" ;;
    --dry-run) DRY=1 ;;
    *) echo "stager: argumento desconocido $1" >&2; exit 2 ;;
  esac
  shift
done

[ -n "$SRC" ] || { echo "stager: falta --src" >&2; exit 5; }
[ -f "$SRC" ] || { echo "stager: src inexistente $SRC" >&2; exit 5; }
[ -n "$PROFILE" ] || PROFILE="A"

# --- destino por clase/overlay (T-0206 criterio 1) ---------------------------
if [ -z "$OVERLAY_KB" ] && df -k /overlay >/dev/null 2>&1; then
  OVERLAY_KB="$(df -k /overlay | awk 'NR==2 {print $4}')"
fi
free_note="overlay=sin-medir"
if [ -n "$OVERLAY_KB" ]; then
  free_note="overlay_free_kb=$OVERLAY_KB"
  # tope duro: sin 4 MB libres no se copia (llenar el overlay mata la clase C)
  if [ "$OVERLAY_KB" -lt 4096 ]; then
    log "dest=abort reason=tope-flash free_kb=$OVERLAY_KB"
    echo "stager: < 4 MB libres en destino (<4096 KB)" >&2
    exit 4
  fi
fi
if [ -z "$DEST_DIR" ]; then
  if [ "$PROFILE" = "C" ] || { [ -n "$OVERLAY_KB" ] && [ "$OVERLAY_KB" -lt 8192 ]; }; then
    DEST_DIR="/tmp"
    log "dest=/tmp reason=tmpfs-only $free_note"
  else
    DEST_DIR="/data/botd"
    log "dest=/data/botd reason=persistente $free_note"
  fi
else
  log "dest=$DEST_DIR reason=explicito $free_note"
fi
DEST="$DEST_DIR/$NAME"
SPOOL_DIR="$DEST_DIR/botd-spool"

if [ "$DRY" = 1 ]; then
  echo "dry: $SRC -> $DEST (profile=$PROFILE retries=$RETRIES spool=$SPOOL_DIR)"
  exit 0
fi

mkdir -p "$DEST_DIR" 2>/dev/null
# sha256sum antepones \ a la línea cuando la ruta contiene \ (Windows), lo que
# rompe el corte por campos; el hash es siempre el token hex de 64.
SRC_SHA="$(sha256sum "$SRC" | grep -oE '[0-9a-fA-F]{64}' | head -1)"
[ -n "$SRC_SHA" ] || { echo "stager: sha256sum no disponible para $SRC" >&2; exit 5; }

# --- copia con reintentos y jitter (T-0205 criterios 1-3) -------------------
attempt=1
while : ; do
  cp "$SRC" "$DEST.part"
  if [ "$attempt" -le "$FAIL_COPIES" ]; then
    printf 'X' >> "$DEST.part" # doble de pérdida: corrompe el chunk copiado
  fi
  DST_SHA="$(sha256sum "$DEST.part" | grep -oE '[0-9a-fA-F]{64}' | head -1)"
  if [ "$SRC_SHA" = "$DST_SHA" ]; then
    mv -f "$DEST.part" "$DEST"
    chmod 755 "$DEST" 2>/dev/null
    log "attempt=$attempt result=ok dest=$DEST"
    break
  fi
  rm -f "$DEST.part"
  if [ "$attempt" -ge "$RETRIES" ]; then
    log "attempt=$attempt result=fail-agotado dest=$DEST"
    echo "stager: reintentos agotados ($RETRIES)" >&2
    exit 2
  fi
  delay=$(( BACKOFF_BASE * attempt + $(jitter_s) ))
  log "attempt=$attempt result=retry delay=${delay}s"
  sleep "$delay"
  attempt=$(( attempt + 1 ))
done

mkdir -p "$SPOOL_DIR" 2>/dev/null
log "spool=$SPOOL_DIR profile=$PROFILE"

# --- lanza el agente y muere: el stager no queda residente ------------------
if [ -n "$STAGE_CMD" ]; then
  sh -c "$STAGE_CMD" &
else
  "$DEST" --stage &
fi
log "stage=spawned pid=$! dest=$DEST"
exit 0
