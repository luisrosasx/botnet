#!/usr/bin/env bash
# build.sh — sliver-builder: compila botd estático por arquitectura y perfil
# (T-0204, EP-02). CGO_ENABLED=0 siempre; -s -w siempre; upx -9 si el perfil
# lo pide y la herramienta existe. El binario nace en dist/<name>-<arch>.
#
# Uso:
#   deploy/sliver-builder/build.sh --arch=mipsle --profile=C
#       [--name=botd] [--cmd=./cmd/botd] [--dist=dist] [--version=V] [--dry-run]
#
# Exit: 0 binario dentro de presupuesto; 1 fuera de presupuesto; 2 uso/arch
# ajeno al perfil.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROFILES="$ROOT/deploy/sliver-builder/profiles.yaml"
ARCH=""
PROFILE=""
NAME="botd"
NAME_SET=0
CMD="./cmd/botd"
DIST="$ROOT/dist"
VERSION="$(git -C "$ROOT" describe --tags --always 2>/dev/null || echo 0.1.0-dev)"
DRY=0

die() { echo "build: $*" >&2; exit 2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --arch) shift; ARCH="${1:?}" ;;
    --arch=*) ARCH="${1#*=}" ;;
    --profile) shift; PROFILE="${1:?}" ;;
    --profile=*) PROFILE="${1#*=}" ;;
    --name) shift; NAME="${1:?}"; NAME_SET=1 ;;
    --name=*) NAME="${1#*=}"; NAME_SET=1 ;;
    --cmd) shift; CMD="${1:?}" ;;
    --cmd=*) CMD="${1#*=}" ;;
    --dist) shift; DIST="${1:?}" ;;
    --dist=*) DIST="${1#*=}" ;;
    --version) shift; VERSION="${1:?}" ;;
    --version=*) VERSION="${1#*=}" ;;
    --dry-run) DRY=1 ;;
    --*) die "flag desconocida $1" ;;
    *) die "argumento desconocido $1" ;;
  esac
  shift
done

[ -n "$ARCH" ] || die "falta --arch=<mipsbe|mipsle|armv5|armv7|arm64|amd64>"
[ -n "$PROFILE" ] || die "falta --profile=<A|B|C>"
[ -f "$PROFILES" ] || die "perfiles ausentes: $PROFILES"

# Perfil -> upx, topes; y validación de que la arch pertenece al perfil.
prof="$(awk -v p="$PROFILE" '
  /^[[:space:]]*[A-C]:[[:space:]]*$/ { cur=($1==p":")?"p":""; next }
  cur && /^[[:space:]]*archs:/ { archs=$0; next }
  cur && /^[[:space:]]*upx:/ { upx=$2; next }
  cur && /^[[:space:]]*rename:/ { rename=$2; next }
  cur && /^[[:space:]]*max_bytes:/ { mb=$2; next }
  cur && /^[[:space:]]*max_bytes_raw:/ { mbr=$2; next }
  END { printf "%s\t%s\t%s\t%s\t%s\n", archs, upx, mb, mbr, rename }' "$PROFILES")"
[ -n "$prof" ] && [ "${prof#undefined}" = "$prof" ] || die "perfil desconocido: $PROFILE"
IFS=$'\t' read -r archs upx mb mbr rename <<< "$prof"
[ -n "$archs" ] || die "perfil sin archs: $PROFILE"
# pertenencia exacta (grep de subcadenas confundiría mips con mipsle).
# orden: quitar espacios ANTES que los corchetes, si no el token " mipsbe"
# conserva el "[" y el primer arch del perfil nunca casa.
list="${archs#*archs:}"; list="${list// /}"; list="${list#\[}"; list="${list%\]}"
arch_ok=0
for a in ${list//,/ }; do
  [ "$a" = "$ARCH" ] && arch_ok=1
done
[ "$arch_ok" = 1 ] || die "arch $ARCH no pertenece al perfil $PROFILE ($archs)"

# Renombre determinista por clase (T-1305 AC2, A1-08): el nombre del
# artefacto sale del perfil (rename:) salvo --name explicito; la etiqueta
# viaja adentro via -ldflags y el manifest la sigue.
if [ "$NAME_SET" = 0 ] && [ -n "${rename:-}" ]; then
  NAME="$rename"
fi

# arch lógica -> GOARCH/GOARM (BLUEPRINT §4: mipsbe es GOARCH=mips).
case "$ARCH" in
  mipsbe) GOARCHV=mips; GOARMV="" ;;
  mipsle) GOARCHV=mipsle; GOARMV="" ;;
  armv5) GOARCHV=arm; GOARMV=5 ;;
  armv7) GOARCHV=arm; GOARMV=7 ;;
  arm64) GOARCHV=arm64; GOARMV="" ;;
  amd64) GOARCHV=amd64; GOARMV="" ;;
  *) die "arch fuera de la matriz: $ARCH" ;;
esac

OUT="$DIST/$NAME-$ARCH"

if [ "$DRY" = 1 ]; then
  echo "dry: GOOS=linux GOARCH=$GOARCHV${GOARMV:+ GOARM=$GOARMV} CGO_ENABLED=0 -> $OUT (upx=$upx, tope=$mb B)"
  exit 0
fi

mkdir -p "$DIST"
cd "$ROOT"
export GOOS=linux GOARCH="$GOARCHV" CGO_ENABLED=0
[ -n "$GOARMV" ] && export GOARM="$GOARMV" || unset GOARM
go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.name=$NAME -X main.classTag=class-$PROFILE" -o "$OUT" "$CMD"

# upx solo si el perfil lo pide y la herramienta está instalada; su ausencia
# no aborta el build (el raw size se mide igual contra max_bytes_raw).
UPXED=0
if [ "$upx" = "true" ]; then
  if command -v upx >/dev/null 2>&1; then
    # -f/--no-backup: idempotencia sobre dist/. Si el fichero ya venía
    # empaquetado (carrera con otro builder sobre dist/), upx lanza
    # AlreadyPackedException: se tolera solo si `upx -t` confirma integridad;
    # un binario roto sigue abortando el build.
    if upx --lzma --best -q -f --no-backup "$OUT" >/dev/null 2>&1; then
      UPXED=1
    elif upx -t "$OUT" >/dev/null 2>&1; then
      echo "build: $OUT ya estaba empaquetado (carrera en dist/): se continúa" >&2
      UPXED=1
    else
      echo "build: upx falló sobre $OUT y no pasa upx -t" >&2
      exit 1
    fi
  else
    echo "build: upx no instalado — presupuesto post-upx ($mb B) no medible aquí" >&2
  fi
fi

size=$(wc -c < "$OUT")
if [ "$UPXED" = 1 ] || [ "$upx" != "true" ]; then
  budget="$mb"
else
  budget="$mbr"
fi
# Guarda de firma (T-1305 AC3): un perfil que no empaqueta no debe llevar
# la magia UPX! — "UPX! sin packer necesario" es firma de bundle (A1-08).
if [ "$upx" != "true" ] && grep -a 'UPX!' "$OUT" >/dev/null 2>&1; then
  echo "build: perfil $PROFILE no empaqueta y $OUT lleva magia UPX!" >&2
  exit 1
fi
if [ "$size" -gt "$budget" ]; then
  echo "build: $OUT $size B supera presupuesto $budget B (perfil $PROFILE${UPXED:+, upx aplicado})" >&2
  exit 1
fi
echo "ok $NAME-$ARCH ${size} B -> $OUT (perfil $PROFILE, upx=$([ "$UPXED" = 1 ] && echo aplicado || echo "perfil=$upx"), presupuesto $budget B)"
