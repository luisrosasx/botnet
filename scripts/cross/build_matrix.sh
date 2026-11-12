#!/usr/bin/env bash
# T-1401: matriz cross estática de 6 arquitecturas (amd64, arm64, armv5,
# armv7, mipsbe, mipsle) con CGO_ENABLED=0 fijado en el recipe. Salida:
# dist/botd-<tag> por arch. Topes BLUEPRINT §2 (MB decimales): perfil A
# <= 4,0 MB; B/C con upx <= 2,6 MB. Puerta F1: las 5 archs de la fase con
# size <= 4 MB (aquí se comprueban las 6: la puerta F1 queda subsumida).
# Herramientas ausentes en el host (file/size/upx) se degradan con shim y
# se avisa; la aserción CGO_ENABLED=0 via go version -m nunca se degrada.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
[ -f scripts/env.sh ] && source scripts/env.sh

BINARY="${BINARY:-botd}"
DIST="${DIST:-$ROOT/dist}"
VERSION="$(git describe --tags --always 2>/dev/null || echo 0.1.0-dev)"
LDFLAGS="-s -w -X main.version=${VERSION}"
# T-1403 AC2: raiz ed25519 fijada en build para `botd --print-root`. El
# .pub ya es base64 de los 32 B crudos: se inyecta literal (en Git Bash
# Windows `base64 -w0` anade \r, y root_ed25519.pub puede ser CRLF).
if [ -f deploy/signing/keys/root_ed25519.pub ]; then
  LDFLAGS="$LDFLAGS -X github.com/botnet/botnet/internal/agent.RootPubKeyBase64=$(tr -d '\r\n' <deploy/signing/keys/root_ed25519.pub)"
fi
MAX_A=4194304     # 4 MiB, perfil A (slots A/B, flash 8 MB): mismo tope que
MAX_BC_UPX=2726297 # 2,6 MiB post-upx (AC T-0204: el empaquetado pasa a --lzma, -9 solo daba 2,76 MB)
#                  profiles.yaml max_bytes (una sola verdad de la matriz)
MAX_F1=4194304    # puerta F1: 5 archs de la fase <= 4 MB
USE_UPX="${UPX:-1}"
mkdir -p "$DIST"

# tags: nombre público del arch -> (GOARCH, GOARM). mips es big-endian por
# convención Go; mipsle explícito. armv5/armv7 se eligen con GOARM.
tags="amd64 arm64 armv5 armv7 mipsbe mipsle"
class_of() { case "$1" in amd64|arm64) echo A ;; armv5|armv7) echo B ;; *) echo C ;; esac; }
goarch_of() { case "$1" in mipsbe) echo mips ;; armv5|armv7) echo arm ;; *) echo "$1" ;; esac; }
goarm_of() { case "$1" in armv5) echo 5 ;; armv7) echo 7 ;; *) echo "" ;; esac; }

size_bytes() { wc -c <"$1" | tr -d ' '; }
size_line() { # shim de `size -t`: BSD/GNU size no está en todo host.
  if command -v size >/dev/null 2>&1; then size -t "$@"; else
    python3 - "$@" <<'PY'
import os, sys
tot = 0
for p in sys.argv[1:]:
    n = os.path.getsize(p)
    tot += n
    print(f"{n:>10} {os.path.basename(p)}")
print(f"{tot:>10} (TOTALS)")
PY
  fi
}
is_static() { # `file` es la aserción; sin él se lee e_type+INTERP del ELF con od.
  local f="$1"
  if command -v file >/dev/null 2>&1; then
    ! file "$f" | grep "dynamically linked" >/dev/null
    return
  fi
  local etype
  etype=$(od -An -t x1 -j 16 -N 1 "$f" | tr -d ' ')
  [ "$etype" = "02" ] # ET_EXEC: Go CGO_ENABLED=0 no emite PT_INTERP
}

[ "${CGO_ENABLED:-0}" = "0" ] || { echo "FALLO: el recipe exige CGO_ENABLED=0"; exit 1; }
export CGO_ENABLED=0

fail=0
built=0
if [ "$USE_UPX" = "1" ] && ! command -v upx >/dev/null 2>&1; then
  echo "aviso: upx ausente en host; gate B/C de 2,6 MB se valida post-upx en build matrix real (se aplica <= 4,0 MB)"
fi
for tag in $tags; do
  arch=$(goarch_of "$tag"); arm=$(goarm_of "$tag"); class=$(class_of "$tag")
  out="$DIST/$BINARY-$tag"
  rm -f "$out" # reintentos no re-empaquetan un ELF ya pasado por upx
  envargs=(GOOS=linux "GOARCH=$arch" CGO_ENABLED=0)
  [ -n "$arm" ] && envargs+=(GOARM="$arm")
  echo "== $tag: $(env "${envargs[@]}" go env GOOS GOARCH | tr '\n' ' ')(GOARM=${arm:-off}) CGO_ENABLED=$(env "${envargs[@]}" go env CGO_ENABLED)"
  env "${envargs[@]}" go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$out" "./cmd/$BINARY"
  # Criterio 3: las build settings del binario deben mostrar CGO_ENABLED=0.
  # Va ANTES de upx: el empaquetado se lleva las secciones donde vive el
  # buildinfo de Go (`go version -m` solo lee ELF virgen).
  if ! go version -m "$out" | grep -E 'CGO_ENABLED=?"?0"?' >/dev/null; then
    echo "FAIL $tag: go version -m no muestra CGO_ENABLED=0"; fail=1; continue
  fi
  if ! is_static "$out"; then
    echo "FAIL $tag: binario dynamically linked (exigido estático)"; fail=1; continue
  fi
  if [ "$USE_UPX" = "1" ] && command -v upx >/dev/null 2>&1; then
    # --lzma: -9 solo daba 2,76 MB y el AC de clase C pide <= 2,6 MB;
    # con lzma el mismo binario baja a ~2,24 MB.
    upx --lzma --best -q "$out" >/dev/null
    upx -t "$out" >/dev/null # gate: upx valida el empaquetado; rompe => fallo
  fi
  size=$(size_bytes "$out")
  mb=$(awk -v s="$size" 'BEGIN{printf "%.2f", s/1000000}')
  max=$MAX_F1
  [ "$class" = "A" ] && max=$MAX_A
  if [ "$class" != "A" ] && command -v upx >/dev/null 2>&1; then max=$MAX_BC_UPX; fi
  if [ "$size" -gt "$max" ]; then
    printf 'FAIL %-8s %10d B (%s MB) class %s > tope %d B\n' "$tag" "$size" "$mb" "$class" "$max"
    fail=1
  else
    printf 'ok   %-8s %10d B (%s MB) class %s static CGO_ENABLED=0\n' "$tag" "$size" "$mb" "$class"
    built=$((built + 1))
  fi
done
if [ "$built" -eq 6 ]; then echo "(TOTALS)"; size_line "$DIST"/$BINARY-{amd64,arm64,armv5,armv7,mipsbe,mipsle} | tail -n 7; fi
if [ "$fail" -ne 0 ]; then
  echo "cross FAIL: $built/6 binarios; algun arch sobre tope o dinamico (puerta F1)"
  exit 1
fi
echo "cross OK: 6/6 ELFs estaticos en $DIST version=$VERSION"
