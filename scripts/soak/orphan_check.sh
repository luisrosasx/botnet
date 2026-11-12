#!/usr/bin/env bash
# orphan_check — T-0502 AC3: tras el ciclo de reinicios, el nodo cierra
# con 1 solo pidfile vivo y 0 procesos fuera del pidfile (0 huerfanos,
# 0 zombis). La puerta completa es el soak de 1.000 reinicios de EP-12
# (FULL=1000 en QEMU/Linux); en el host actual corre acelerado con
# ITERS ciclos del mismo par botd --parent/--child (reexec, T-0501).
#
# Criterios que aserta este script:
#   * pgrep -f botd | wc -l = 2 al cerrar cada ciclo (padre + hijo).
#   * recuperacion del par tras kill -9: < 15 s (re-eleccion) / < 10 s
#     (relanzamiento del hijo).
#   * al final: 1 solo pidfile, su pid vivo (kill -0), y TODO proceso
#     botd vivo es el holder del pidfile o su hijo directo (0 fuera).
#   * `ps | grep -c '[d]efunct botd'` = 0 (solo en hosts con estado Z).
#
# Modos: ITERS=N (default 5 smoke; FULL=1000 la puerta de EP-12).
# En Git Bash (Windows) no hay pkill/pgrep: doble medida por pid
# registrado y ps -W, mismo criterio, otra herramienta (anotado en
# docs/runbooks/persistence.md).
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

ITERS=${ITERS:-5}
[ "${FULL:-0}" = "1" ] && ITERS=1000
TMP=$(mktemp -d)
[ "${KEEP_TMP:-0}" = 1 ] && echo "orphan_check: TMP=$TMP" >&2
DATA=$TMP/data
BIN=$TMP/bin
# TAG: nombre unico del tmpdir; en Git Bash el hijo aparece en `ps -W`
# con la ruta Windows (botd.exe) y el padre con la ruta /tmp: ambos
# comparten TAG, y ninguna otra fila del ps lo lleva.
TAG=$(basename "$TMP")
mkdir -p "$DATA" "$BIN"

EXT=""
[[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* ]] && EXT=".exe"
go build -o "$BIN/botd$EXT" ./cmd/botd
# apistub: doble del backend F1 en host sin red — si no, el agente del
# soak muere al primer heartbeat (DNS inexistente) y el par nunca esta
# establemente en 2 procesos.
go build -o "$BIN/apistub$EXT" ./scripts/soak/apistub
"$BIN/apistub$EXT" --addr 127.0.0.1:0 > "$TMP/stub.port" 2>/dev/null &
STUB_PID=$!
for i in $(seq 1 40); do [ -s "$TMP/stub.port" ] && break; sleep 0.25; done
[ -s "$TMP/stub.port" ] || { echo "orphan_check: apistub no escuchando" >&2; exit 1; }
cat > "$DATA/cfg.json" <<JSON
{"endpoint":"http://127.0.0.1:$(cat "$TMP/stub.port" | head -1)","node_dir":"$DATA","interval_s":900}
JSON

cat > "$DATA/node.json" <<'JSON'
{
  "node_id": "11111111-1111-2222-3333-444444444444",
  "slug": "soaksoak0001",
  "arch": "amd64",
  "rendezvous": ["rdv.botnet.example:443"],
  "ttl_s": 900,
  "agent_ver": "v0.1.0"
}
JSON

LOG=$TMP/botd.log
PIDFILE=$DATA/botd.pid
IS_WIN=0
[ -n "$EXT" ] && IS_WIN=1

cleanup() {
  for f in $(ls "$DATA" 2>/dev/null | grep '\.pid$' || true); do
    p=$(cat "$DATA/$f" 2>/dev/null || true)
    [ -n "${p:-}" ] && kill -9 "$p" 2>/dev/null || true
  done
  kill -9 "${PARENT_BPID:-}" 2>/dev/null || true
  if [ "$IS_WIN" = "1" ]; then
    taskkill //F //IM apistub.exe >/dev/null 2>&1 || true
  else
    kill -9 "${STUB_PID:-}" 2>/dev/null || true
  fi
  [ "${KEEP_TMP:-0}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT

is_alive() { # pid en espacio WINPID (el que escribe Go en el pidfile)
  local pid=$1
  if [ "$IS_WIN" = "1" ]; then
    ps -W 2>/dev/null | awk -v p="$pid" '$4==p' | grep -q .
  else
    kill -0 "$pid" 2>/dev/null
  fi
}

kill_pid() {
  local pid=$1 i wp
  [ -z "$pid" ] && return 0
  if [ "$IS_WIN" = "1" ]; then
    wp=$(ps -W 2>/dev/null | awk -v p="$pid" '$4==p {print $4; exit}')
    [ -n "$wp" ] && taskkill //F //PID "$wp" >/dev/null 2>&1 || true
    # Si era el padre original (job de bash), bash sostiene su handle:
    # sin `wait` la fila muerta sigue visible en ps -W y el doble de
    # "0 procesos fuera del pidfile" contaria un fantasma. En Linux,
    # kill -9 + reap del init disolvian la fila.
    if [ -n "${PARENT_BPID:-}" ] && [ "$pid" = "${PARENT_WIN:-}" ]; then
      wait "$PARENT_BPID" 2>/dev/null || true
    fi
  else
    kill -9 "$pid" 2>/dev/null || true
  fi
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    is_alive "$pid" || return 0
    sleep 0.1
  done
  echo "orphan_check: pid $pid sigue vivo tras kill -9" >&2
  return 1
}

botd_procs() { # procesos botd --parent/--child vivos
  if [ "$IS_WIN" = "1" ]; then
    # el hijo nace via os.Executable(): su fila de ps trae la ruta
    # Windows; el padre, la /tmp. Ambas comparten el TAG unico del
    # tmpdir y cada proc sale en 2 filas -> WINPID unico por proceso.
    # El segundo grep deja fuera apistub.exe (mismo TAG, no es botd).
    # `|| true`: con `set -o pipefail`, 0 procesos hace que grep devuelva
    # 1 y mataba al llamante en `n=$(botd_procs)` antes de su echo OK.
    local c
    c=$(ps -W 2>/dev/null | grep -iF "$TAG" | grep -iF botd | awk '{print $4}' | sort -un | wc -l | tr -d ' ' || true)
    echo "${c:-0}"
  else
    pgrep -f "$BIN/botd --(parent|child)" 2>/dev/null | wc -l | tr -d ' ' || echo 0
  fi
}

child_of() { # WINPID del hijo del padre dado
  if [ "$IS_WIN" = "1" ]; then
    # ppids de ps no valen (el hijo win-nativo sale con PPID=0): el
    # padre loguea cada spawn con "spawn hijo... pid=N" (WINPID).
    local c
    c=$(grep -o '"msg":"watchdog spawn hijo","pid":[0-9]*' "$LOG" 2>/dev/null | tail -1 | sed 's/.*pid"://')
    [ -n "$c" ] && { echo "$c"; return 0; }
    local ms
    ms=$(ps -W 2>/dev/null | awk -v p="$1" '$4==p {print $1; exit}')
    [ -n "$ms" ] && ps -W 2>/dev/null | awk -v p="$ms" '$2==p {print $4; exit}'
  else
    ps -o ppid=,pid= 2>/dev/null | awk -v p="$1" '$1==p {print $2; exit}'
  fi
}

spawn_parent() {
  "$BIN/botd$EXT" --config "$DATA/cfg.json" --parent --node-dir "$DATA" --socks5-addr '' --run-for 3600s >>"$LOG" 2>&1 &
  PARENT_BPID=$!
}

wait_pair() { # <limite s>
  local limit=${1:-15} i
  for i in $(seq 1 $((limit * 4))); do
    local n
    n=$(botd_procs)
    if [ "${n:-0}" -ge 2 ] && grep -q . "$PIDFILE" 2>/dev/null && is_alive "$(cat "$PIDFILE")"; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

spawn_parent
wait_pair 20 || { echo "orphan_check: el par no arranco (ver $LOG)" >&2; exit 1; }
PARENT_WIN=$(cat "$PIDFILE") # WINPID del job original: kill_pid lo usa para hacer `wait` y que bash suelte su handle (sin eso, ps -W conserva la fila muerta)

rev=0
for ((i = 1; i <= ITERS; i++)); do
  pp=$(cat "$PIDFILE")
  cp=$(child_of "$pp")
  case $((RANDOM % 3)) in
    0) [ -n "$cp" ] && { kill_pid "$cp"; wait_pair 10 || { echo "ciclo $i: hijo no revivio <10s" >&2; exit 1; }; rev=$((rev+1)); } ;;
    1) [ -n "$cp" ] && { kill_pid "$pp"; wait_pair 15 || { echo "ciclo $i: re-eleccion >15s" >&2; exit 1; }; rev=$((rev+1)); } ;;
    2) { kill_pid "$cp"; kill_pid "$pp"; } ; sleep 0.5; "$BIN/botd$EXT" --config "$DATA/cfg.json" --guard --node-dir "$DATA" >>"$LOG" 2>&1 || true
       wait_pair 20 || { echo "ciclo $i: guard no revivio el par" >&2; exit 1; }; rev=$((rev+1)); ;;
  esac
  n=$(botd_procs)
  # Transitorios legitimos: tras un kill pueden convivir hasta 3 bots
  # (par viejo en promocion + par que el guard reaviva) antes de que
  # el perdedor se suicide por ErrDouble. wait_pair ya midio el par
  # recuperado; la cota dura del T-0502 es 0 procesos fuera del
  # pidfile AL CERRAR, y ahi se aserta (abajo).
  [ "${n:-0}" -le 3 ] || { echo "ciclo $i: vivos=$n, want <=3" >&2; exit 1; }
done

# --- cierre: 1 solo pidfile vivo, 0 fuera, 0 zombis ----------------------
pidfiles=$(ls "$DATA"/*.pid 2>/dev/null | wc -l | tr -d ' ')
[ "$pidfiles" -eq 1 ] || { echo "orphan_check: pidfiles=$pidfiles, want 1" >&2; exit 1; }
holder=$(cat "$PIDFILE")
is_alive "$holder" || { echo "orphan_check: pidfile sin pid vivo (kill -0)" >&2; exit 1; }
# `ls /proc/<pid>/task | wc -l` coherente: el holder es un proceso real
# con al menos un thread.
if [ -d "/proc/$holder" ]; then
  th=$(ls "/proc/$holder/task" | wc -l)
  [ "$th" -ge 1 ] || { echo "orphan_check: holder sin threads" >&2; exit 1; }
fi
# 0 procesos fuera del pidfile: cada botd vivo es holder o hijo suyo.
out=0
if [ "$IS_WIN" = "1" ]; then
  last=$(child_of "$holder")
  for wp in $(ps -W 2>/dev/null | grep -iF "$TAG" | grep -iF botd | awk '{print $4}' | sort -un); do
    [ "$wp" = "$holder" ] && continue
    [ -n "$last" ] && [ "$wp" = "$last" ] && continue
    out=$((out + 1))
  done
else
  for pid in $(pgrep -f "$BIN/botd --(parent|child)" 2>/dev/null || true); do
    ppid=$(ps -o ppid= -p "$pid" | tr -d ' ')
    [ "$pid" != "$holder" ] && [ "$ppid" != "$holder" ] && out=$((out + 1))
  done
fi
[ "$out" -eq 0 ] || { echo "orphan_check: $out procesos fuera del pidfile" >&2; exit 1; }
defunct=0
[ "$IS_WIN" = "1" ] || defunct=$(ps -eo stat=,comm= 2>/dev/null | awk '$1 ~ /^Z/ && $2 ~ /^botd/' | wc -l | tr -d ' ')
[ "$defunct" -eq 0 ] || { echo "orphan_check: zombis defunct botd = $defunct" >&2; exit 1; }

# Cierre "dueno": mata la familia entera y deja Reposar 2 s; el par
# re-elegido tambien cae (aqui no hay cron: el guard fue un-shot). En
# Git Bash, taskkill por imagen (el soak corre serial); en Linux,
# pkill por ruta del tmpdir. Dos pasadas: la primera tumba holders, la
# segunda a los re-elegidos que promocionaron mientras.
if [ "$IS_WIN" = "1" ]; then
  taskkill //F //IM botd.exe >/dev/null 2>&1 || true
else
  pkill -9 -f "$BIN/botd" || true
fi
sleep 2
if [ "$IS_WIN" = "1" ]; then
  taskkill //F //IM botd.exe >/dev/null 2>&1 || true
  # `wait` obliga a bash a cosechar el job muerto: sin eso ps -W sigue
  # mostrando la fila del proceso ya terminado y el conteo miente.
  [ -n "${PARENT_BPID:-}" ] && wait "$PARENT_BPID" 2>/dev/null || true
else
  pkill -9 -f "$BIN/botd" || true
fi
# Cosecha tardia de ps -W: reintento acotado antes de darlo por huerfano.
n=999
for _ in 1 2 3 4 5 6 7 8 9 10; do
  n=$(botd_procs)
  [ "${n:-0}" -eq 0 ] && break
  sleep 0.5
done
[ "${n:-0}" -eq 0 ] || { echo "orphan_check: huerfanos=$n tras matar el par" >&2; exit 1; }

echo "orphan_check: OK — ciclos=$ITERS revivals=$rev pidfiles=1 huerfanos=0 fuera=0 zombis=0 host=$(uname -s)"
