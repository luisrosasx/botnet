#!/usr/bin/env bash
# stagger_mtime.sh — mtimes escalonados del bundle en dispositivo
# (T-1305 AC1, A1-08): cuatro ficheros con el mismo mtime del overlay es
# firma de bundle ("paquete del proveedor de 2009"). Este script escala
# los mtimes de un directorio de bundle a paso >= step (10 s por defecto)
# y aserta la huella:
#   stat -c %Y /data/botd/* | sort -u | wc -l  ==  numero de ficheros
# y hueco minimo >= step. BusyBox en dispositivo soporta `touch -d @epoch`;
# aqui usamos coreutils (Git Bash) como doble.
#
# Uso:
#   scripts/build/stagger_mtime.sh --dir=/data/botd [--base=EPOCH] [--step=10]
#   scripts/build/stagger_mtime.sh --self-test
# Exit: 0 escalonado y verificado; 1 verificacion fallida; 2 uso.
set -euo pipefail

die() { echo "stagger_mtime: $*" >&2; exit 2; }

DIR=""
BASE=""
STEP=10
SELF=0

while [ $# -gt 0 ]; do
  case "$1" in
    --dir) shift; DIR="${1:?}" ;;
    --dir=*) DIR="${1#*=}" ;;
    --base) shift; BASE="${1:?}" ;;
    --base=*) BASE="${1#*=}" ;;
    --step) shift; STEP="${1:?}" ;;
    --step=*) STEP="${1#*=}" ;;
    --self-test) SELF=1 ;;
    --*) die "flag desconocida $1" ;;
    *) die "argumento desconocido $1" ;;
  esac
  shift
done

[ "$STEP" -ge 10 ] 2>/dev/null || die "step debe ser >= 10 s (criterio A1-08)"

stagger() { # $1=dir $2=step; deja mtimes unicos escalonados
  local dir="$1" step="$2" i=0 base f
  base="${BASE:-}"
  if [ -z "$base" ]; then
    base=$(find "$dir" -maxdepth 1 -type f -printf '%T@\n' 2>/dev/null | sort -n | head -1 | cut -d. -f1)
  fi
  [ -n "$base" ] || die "sin ficheros que escalonar en $dir"
  while IFS= read -r f; do
    touch -m -d "@$(( base + i * step ))" "$f"
    i=$(( i + 1 ))
  done < <(find "$dir" -maxdepth 1 -type f | sort)
  echo "$i"
}

verify() { # $1=dir $2=step: mtimes unicos == ficheros y gap minimo >= step
  local dir="$1" step="$2" n uniq gap
  n=$(find "$dir" -maxdepth 1 -type f | wc -l | tr -d ' ')
  uniq=$(find "$dir" -maxdepth 1 -type f -printf '%T@\n' | cut -d. -f1 | sort -u | wc -l | tr -d ' ')
  gap=$(find "$dir" -maxdepth 1 -type f -printf '%T@\n' | cut -d. -f1 | sort -n | awk 'NR>1{d=$1-p; if(m==""||d<m)m=d} {p=$1} END{if(m=="")m=99999; print m}')
  result=PASS
  [ "$n" -eq "$uniq" ] || result=FAIL
  [ "$gap" -ge "$step" ] || result=FAIL
  echo "files=$n unique_mtimes=$uniq min_gap_s=$gap step=${step}s result=$result"
  [ "$result" = PASS ]
}

if [ "$SELF" = 1 ]; then
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  same=$(date -u +%s)
  for f in botd manifest.json node.json cve.yml; do
    echo fixture > "$tmp/$f"
    touch -m -d "@$same" "$tmp/$f"
  done
  before=$(find "$tmp" -maxdepth 1 -type f -printf '%T@\n' | cut -d. -f1 | sort -u | wc -l | tr -d ' ')
  [ "$before" = 1 ] || { echo "stagger_mtime self-test: el fixture deberia nacer con mtime unico comun ($before)" >&2; exit 1; }
  stagger "$tmp" "$STEP" >/dev/null
  verify "$tmp" "$STEP" || exit 1
  echo "self-test ok: bundle de 4 ficheros paso de 1 mtime comun a 4 unicos con paso >= ${STEP}s"
  exit 0
fi

[ -n "$DIR" ] || die "falta --dir=<bundle>"
[ -d "$DIR" ] || die "directorio inexistente: $DIR"

stagger "$DIR" "$STEP" >/dev/null
verify "$DIR" "$STEP"
