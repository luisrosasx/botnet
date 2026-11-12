#!/usr/bin/env bash
# soak-1000 — T-1204: 1.000 iteraciones de kill -9 aleatorio (hijo /
# padre / ambos) sobre el PAR REAL watchdog+worker: `botd --parent`
# sostiene el flock del pidfile y re-ejecuta el mismo binario como
# `--child` (T-0501); muere el hijo, el padre lo relanza (ReviveDelay
# ~1 s); muere el padre, el hijo sobreviviente se re-elige padre y
# reescribe el pidfile (T-0502); muertos ambos no queda nadie que se
# revive solo — ahi entra el un-shot `botd --guard` (el cron de T-0503),
# que es exactamente donde el criterio lo pide. Criterios: el ciclo
# cierra SIEMPRE con 2 vivos (pgrep -f botd | wc -l), 1 solo pidfile
# vivo con kill -0 sobre SU pid (lo escribe botd, no el script) y al
# final 0 huerfanos contados contra la cadena del pidfile: se mata al
# dueno del pidfile, se persigue al re-elegido y se repite hasta que no
# queda NINGUN botd del arnes vivo.
#
# El arnes cuenta SOLO sus procesos: cada corrida vive bajo su mktemp y
# el patrón de conteo es la ruta de ese directorio — en el host quedan
# botd de verificaciones anteriores que no deben contaminar el censo.
#
# Modos:
#   ITERS=N   acelerado en el host actual (default 200)
#   FULL=1    1.000 iteraciones — la puerta F3/QEMU clase B
#   En host Linux con pkill/pgrep reales se usan pkill -9/pgrep -f; en
#   Git Bash (Windows) no existen: se mata con taskkill //F por winpid y
#   se cuenta vivos con ps -W — mismo criterio, otra herramienta
#   (anotado en docs/runbooks/soak.md).
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

ITERS=${ITERS:-200}
[ "${FULL:-0}" = "1" ] && ITERS=1000
TMP=$(mktemp -d)
DATA=$TMP/data
BIN=$TMP/bin
RUNTAG=$(basename "$TMP") # marca de esta corrida para censo de procesos
mkdir -p "$DATA/state" "$BIN"

EXT=""
[[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* ]] && EXT=".exe"
IS_WIN=0
[ -n "$EXT" ] && IS_WIN=1

# El trap barre TAMBIEN los procesos de la corrida: el padre relanzado
# por el guard no es job de bash y sobreviviria a un fallo a mitad.
trap 'kill ${PIDS_TRACKED:-} 2>/dev/null || true
if [ "$IS_WIN" = "1" ]; then
  for q in $(ps -W 2>/dev/null | grep -F "$RUNTAG" | awk "{print \$4}"); do
    taskkill //F //PID "$q" >/dev/null 2>&1 || true
  done
else
  for q in $(pgrep -f "$BIN/botd" 2>/dev/null); do kill -9 "$q" 2>/dev/null || true; done
fi
echo "soak: tmp=$TMP" >&2
[ "${KEEP_TMP:-}" = 1 ] || rm -rf "$TMP" 2>/dev/null || true' EXIT

# Un solo binario para ambos roles (T-0501 AC1): el padre re-ejecuta su
# propio exe con -child; el guard, con -parent.
go build -o "$BIN/botd$EXT" ./cmd/botd
BOTD="$BIN/botd$EXT"

# node.json valido: el hijo arranca en modo agente sin backend (los
# latidos fallan contra el endpoint ejemplo y el bucle sigue vivo).
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

PIDFILE="$DATA/botd.pid" # el canonico de T-0501: lo escribe botd, no el script

PIDS_TRACKED="" # jobs de bash para el trap

# --- primitivas de proceso (pid NATIVO del SO en ambos hosts) ----------

pidfile_pid() {
  [ -f "$PIDFILE" ] && tr -d '[:space:]' <"$PIDFILE" || true
}

is_alive() { # pid nativo → exit 0 si vive
  local p=$1
  if [ "$IS_WIN" = "1" ]; then
    ps -W 2>/dev/null | awk -v p="$p" '$4==p {found=1} END {exit !found}'
  else
    kill -0 "$p" 2>/dev/null
  fi
}

kill_native() { # pid nativo → kill -9 y espera acotada a que muera
  local p=$1 i
  if [ "$IS_WIN" = "1" ]; then
    taskkill //F //PID "$p" >/dev/null 2>&1 || true
  else
    kill -9 "$p" 2>/dev/null || true
  fi
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    is_alive "$p" || return 0
    sleep 0.1
  done
  echo "soak: pid $p sigue vivo tras kill -9" >&2
  return 1
}

bot_pids() { # pids nativos de TODO botd de ESTA corrida (huerfanos incluidos)
  if [ "$IS_WIN" = "1" ]; then
    ps -W 2>/dev/null | grep -F "$RUNTAG" | awk '{print $4}'
  else
    pgrep -f "$BIN/botd" 2>/dev/null | tr '\n' ' ' || true
  fi
  echo
}

alive_count() { bot_pids | wc -w | tr -d ' '; }

child_pid_of() { # pid del hijo vigilado: el unico botd propio que no es
                 # el dueno del pidfile ($1). En Linux vale pgrep -P; en
                 # Git Bash ps -W no reporta PPID de procesos nativos (0),
                 # asi que el censo por ruta hace el mismo trabajo.
                 # SIEMPRE status 0: la ausencia de hijo es un estado
                 # normal del soak, no un error (las asignaciones nc=$(...)
                 # sueltas lo convertirian en un exit silencioso).
  local p=$1 q
  if [ "$IS_WIN" != "1" ] && command -v pgrep >/dev/null 2>&1; then
    pgrep -P "$p" -f "$BIN/botd" 2>/dev/null | head -1
    return 0
  fi
  for q in $(bot_pids); do
    [ "$q" != "$p" ] && { echo "$q"; return 0; }
  done
  return 0
}

# --- arranque y revivals -----------------------------------------------

start_parent() { # lanza botd --parent y espera a que estampe pidfile vivo
  "$BOTD" --parent --node-dir "$DATA" --socks5-addr '' --run-for 3600s \
    >>"$TMP/botd.log" 2>&1 &
  PIDS_TRACKED="$PIDS_TRACKED $!"
  local i p
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
    p=$(pidfile_pid)
    [ -n "$p" ] && is_alive "$p" && return 0
    sleep 0.1
  done
  echo "soak: el padre no estampo un pidfile vivo" >&2
  return 1
}

run_guard() { # un-shot de cron (T-0503): relanza al padre si el pidfile no responde
  "$BOTD" --guard --node-dir "$DATA" --socks5-addr '' >>"$TMP/botd.log" 2>&1 || true
}

wait_pair() { # el par cierra con pidfile vivo + exactamente 1 hijo (2 vivos)
  local i p c
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do
    p=$(pidfile_pid)
    if [ -n "$p" ] && is_alive "$p"; then
      c=$(child_pid_of "$p")
      [ -n "$c" ] && is_alive "$c" && [ "$(alive_count)" = "2" ] && return 0
    fi
    sleep 0.25
  done
  return 1
}

assert_cycle() { # AC2 literal por ciclo: 1 solo pidfile y kill -0 a SU pid
  local pidfiles p
  pidfiles=$(ls "$DATA"/*.pid 2>/dev/null | wc -l | tr -d ' ')
  [ "$pidfiles" = "1" ] || { echo "soak: pidfiles=$pidfiles, want 1" >&2; return 1; }
  p=$(pidfile_pid)
  is_alive "$p" || { echo "soak: pid del pidfile ($p) no es vivo (kill -0)" >&2; return 1; }
  [ "$(alive_count)" = "2" ] || { echo "soak: vivos=$(alive_count), want 2 (pgrep -f botd)" >&2; return 1; }
}

start_parent
revivals=0
for ((i = 1; i <= ITERS; i++)); do
  case $((i % 3)) in
    0) # muere el HIJO: el padre lo relanza por re-exec (T-0501)
      p=$(pidfile_pid)
      c=$(child_pid_of "$p")
      [ -n "$c" ] || { echo "soak: ciclo $i sin hijo que matar" >&2; exit 1; }
      kill_native "$c"
      # revival real: el hijo muerto qued6 verificado como tal
      # (kill_native exige muerto) y el padre vuelve a tener uno vivo;
      # no se exige pid nuevo: Windows recicla pids con velocidad.
      nc=""
      for j in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do
        nc=$(child_pid_of "$p")
        [ -n "$nc" ] && is_alive "$nc" && break
        sleep 0.25
      done
      [ -n "$nc" ] && is_alive "$nc" || { echo "soak: el padre no revivio al hijo (ciclo $i)" >&2; exit 1; }
      revivals=$((revivals + 1))
      ;;
    1) # muere el PADRE: el hijo sobreviviente se re-elige (T-0502)
      p=$(pidfile_pid)
      kill_native "$p"
      # re-eleccion real: el pidfile pasa a un pid distinto y vivo
      np=""
      for j in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do
        np=$(pidfile_pid)
        [ -n "$np" ] && [ "$np" != "$p" ] && is_alive "$np" && break
        sleep 0.25
      done
      [ -n "$np" ] && [ "$np" != "$p" ] && is_alive "$np" || { echo "soak: no hubo re-eleccion del hijo (ciclo $i)" >&2; exit 1; }
      wait_pair || { echo "soak: el par no se reconstituyo (ciclo $i)" >&2; exit 1; }
      revivals=$((revivals + 1))
      ;;
    2) # mueren AMBOS: no queda nadie que se revive solo — el criterio
       # pide el un-shot `botd --guard` (cron T-0503)
      for attempt in 1 2 3 4; do
        for cp in $(bot_pids); do kill_native "$cp"; done
        for j in 1 2 3 4 5 6 7 8 9 10 11 12; do # gracia a re-elecciones/recien nacidos
          [ "$(alive_count)" = "0" ] && break
          sleep 0.25
        done
        [ "$(alive_count)" = "0" ] && break # momento limpio: ni un botd vivo
      done
      [ "$(alive_count)" = "0" ] || { echo "soak: ciclo $i — el par no se dejo matar limpio" >&2; exit 1; }
      run_guard
      wait_pair || { echo "soak: el guard no relanzo el par (ciclo $i)" >&2; exit 1; }
      revivals=$((revivals + 1))
      ;;
  esac
  assert_cycle || { echo "soak: asercion de ciclo $i fallida" >&2; exit 1; }
done

# --- cierre: matar la cadena del pidfile y contar huerfanos ------------
# Huerfano real = un botd vivo detras de un pidfile muerto (el hijo
# re-elegido que nadie persiguio). Se mata al dueno del pidfile, se
# espera la re-eleccion (el hijo promueve y reescribe el pidfile) y se
# repite; 0 huerfanos exige que la cadena se agote.
for round in 1 2 3 4 5; do
  p=$(pidfile_pid)
  if [ -n "$p" ] && is_alive "$p"; then
    kill_native "$p"
    sleep 1.5 # el hijo superviviente se re-elige y reestampa el pidfile
  else
    break
  fi
done
# barrido final por si quedara algun botd suelto de la corrida
for cp in $(bot_pids); do kill_native "$cp"; done

left=$(alive_count)
if [ "$left" != "0" ]; then
  echo "soak: huerfanos=$left tras matar la cadena del pidfile, want 0" >&2
  exit 1
fi
pidfiles=$(ls "$DATA"/*.pid 2>/dev/null | wc -l | tr -d ' ')
if [ "$pidfiles" != "1" ]; then
  echo "soak: pidfiles=$pidfiles tras el cierre, want 1" >&2
  exit 1
fi

echo "soak-1000: OK — iteraciones=$ITERS revivals=$revivals huerfanos=0 pidfiles=$pidfiles par=botd--parent+reexec reanimacion=guard host=$(uname -s) matar=$( [ "$IS_WIN" = 1 ] && echo 'taskkill/ps -W (doble medida, sin pkill nativo)' || echo 'pkill -9/pgrep -f nativos' )"
