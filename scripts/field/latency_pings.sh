#!/usr/bin/env bash
# latency_pings.sh — EP-06 T-0604 AC2: p50/p95/p99/max de N pings al router
# CON el bucket de caudal saturado. docs/verification/EP-06.md cierra el
# AC2 con la nota «los 100 pings ssh reales al router = BLOCKED-HW de campo
# (sin router/nodo Linux en el host)»; este script es esa medición de campo.
#
# Uso:
#   bash scripts/field/latency_pings.sh --target <ssh-host|local> [--count 100] \
#     [--host <ip-destino>] [--saturate-mbps N] [--threshold-p95-ms 200] \
#     [--interval S] [--json] [--dry] [--help]
#
# Cómo se mide:
#   - El ping se lanza con la herramienta nativa del objetivo: en Windows
#     (Git Bash del host F4) `ping -n`; en Linux (nodo por ssh) `ping -c`,
#     con `-i` solo si se es root (intervalos < 2 s sin permisos fallan).
#   - La saturación es un iperf3 en segundo plano que se mata al acabar los
#     pings; si iperf3 no está, sale 3 (es una herramienta del criterio, no
#     un extra). Si el servidor iperf3 no responde, se avisa por stderr y la
#     medición de latencia sigue siendo válida.
#   - Los percentiles se calculan por rango-nearest sobre los RTT de CADA
#     respuesta (no sobre la media del resumen), con regex independiente del
#     idioma: el valor es el último número seguido de `m`/`ms` en la línea.
#
# Salida: texto legible, o JSON plano con --json (números crudos).
# Salidas: 0 p95 <= umbral; 1 p95 > umbral o 0 respuestas; 2 argumentos
# invalidos; 3 falta la herramienta (ping, iperf3 si se pide saturacion, o
# ssh inalcanzable cuando el objetivo es remoto).
set -uo pipefail

usage() {
  cat <<'EOF'
latency_pings.sh — p50/p95/p99/max de N pings con el bucket saturado.

Uso:
  bash scripts/field/latency_pings.sh --target <ssh-host|local> [opciones]

Opciones:
  --target <ssh-host|local>  donde se lanzan los pings (obligatorio)
  --count N                  numero de pings (defecto 100)
  --host <ip-destino>        destino de los pings (defecto 127.0.0.1)
  --saturate-mbps N          carga iperf3 previa de N Mbps (sin carga si se omite)
  --threshold-p95-ms MS      umbral de p95 en ms (defecto 200)
  --interval S               separacion entre pings en Linux root (defecto 0.2)
  --json                     salida JSON en una linea
  --dry                      imprime los comandos y no ejecuta nada
  -h, --help                 esta ayuda

Salidas: 0 p95 <= umbral | 1 p95 > umbral o sin respuestas | 2 argumentos | 3 herramienta ausente.
Variables: PING_BIN, IPERF3_BIN, SSH_BIN (dobles para pruebas sin la herramienta real).
EOF
}

TARGET=""
HOST_DEST="127.0.0.1"
COUNT=100
SATURATE=""
THRESHOLD=200
INTERVAL=0.2
JSON=0
DRY=0
# 2 s por ping: equivalente al -W 2000 de Windows y al -W 2 de Linux.
PING_TIMEOUT=2

die() {
  printf 'latency_pings: %s\n' "$1" >&2
  exit "${2:-2}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --target)       [ $# -ge 2 ] || die '--target necesita un valor'; TARGET="$2"; shift 2 ;;
    --target=*)     TARGET="${1#*=}"; shift ;;
    --host)         [ $# -ge 2 ] || die '--host necesita un valor'; HOST_DEST="$2"; shift 2 ;;
    --host=*)       HOST_DEST="${1#*=}"; shift ;;
    --count)        [ $# -ge 2 ] || die '--count necesita un valor'; COUNT="$2"; shift 2 ;;
    --count=*)      COUNT="${1#*=}"; shift ;;
    --saturate-mbps) [ $# -ge 2 ] || die '--saturate-mbps necesita un valor'; SATURATE="$2"; shift 2 ;;
    --saturate-mbps=*) SATURATE="${1#*=}"; shift ;;
    --threshold-p95-ms) [ $# -ge 2 ] || die '--threshold-p95-ms necesita un valor'; THRESHOLD="$2"; shift 2 ;;
    --threshold-p95-ms=*) THRESHOLD="${1#*=}"; shift ;;
    --interval)     [ $# -ge 2 ] || die '--interval necesita un valor'; INTERVAL="$2"; shift 2 ;;
    --interval=*)   INTERVAL="${1#*=}"; shift ;;
    --json)         JSON=1; shift ;;
    --dry)          DRY=1; shift ;;
    -h|--help)      usage; exit 0 ;;
    *)              die "argumento desconocido: $1" 2 ;;
  esac
done

[ -n "$TARGET" ] || die 'falta --target <ssh-host|local> (usa --help)' 2
[[ "$COUNT" =~ ^[0-9]+$ ]] && [ "$COUNT" -gt 0 ] || die '--count debe ser un entero > 0' 2
[ -n "$HOST_DEST" ] || die '--host no puede ir vacio' 2
is_num() { printf '%s' "$1" | LC_ALL=C awk '{ if ($0 ~ /^[0-9]+([.][0-9]+)?$/) exit 0; exit 1 }'; }
is_num "$THRESHOLD" || die '--threshold-p95-ms debe ser un numero >= 0' 2
is_num "$INTERVAL" || die '--interval debe ser un numero > 0' 2
if [ -n "$SATURATE" ]; then
  is_num "$SATURATE" || die '--saturate-mbps debe ser un numero > 0' 2
fi

PING_BIN="${PING_BIN:-ping}"
IPERF3_BIN="${IPERF3_BIN:-iperf3}"
SSH_BIN="${SSH_BIN:-ssh}"
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=8)

# El SO que decide la sintaxis de ping es el del OBJETIVO: los nodos de la
# malla son Linux; solo `--target local` puede ser el Windows del host F4.
OS_TAG=linux
case "$(uname -s 2>/dev/null)" in
  MINGW* | MSYS* | CYGWIN*) OS_TAG=windows ;;
esac
[ "$TARGET" = local ] || OS_TAG=linux

INTERVAL_OPT=""
if [ "$OS_TAG" = linux ]; then
  # -i < 2 s solo con permisos; con usuario normal se queda en el defecto 1 s.
  if bash -c '[ "$(id -u)" -eq 0 ]' 2>/dev/null || [ "$TARGET" != local ]; then
    INTERVAL_OPT="-i $INTERVAL"
  fi
  PING_ARGS="-c $COUNT -W $PING_TIMEOUT $INTERVAL_OPT $HOST_DEST"
  LOAD_SECS=$(awk -v c="$COUNT" -v i="$INTERVAL" 'BEGIN{ d = int(c*i) + 5; if (d < 10) d = 10; printf "%d", d }')
else
  PING_ARGS="-n $COUNT -w $((PING_TIMEOUT * 1000)) $HOST_DEST"
  LOAD_SECS=$(awk -v c="$COUNT" 'BEGIN{ d = c + 5; if (d < 10) d = 10; printf "%d", d }')
fi

IPERF_BIN=""
IPERF_ARGS=""
if [ -n "$SATURATE" ]; then
  # Solo se sondea iperf3 cuando se pide carga: sin --saturate-mbps su
  # ausencia no puede fastidiar una medición de latencia.
  IPERF_BIN="$IPERF3_BIN"
  # -b N M con el tope del bucket; el servidor iperf3 debe vivir en --host.
  IPERF_ARGS="-c $HOST_DEST -t $LOAD_SECS -b ${SATURATE}M"
fi

if [ "$DRY" = 1 ]; then
  printf '# objetivo=%s os=%s count=%s\n' "$TARGET" "$OS_TAG" "$COUNT"
  if [ "$TARGET" = local ]; then
    printf 'PING: %s %s\n' "$PING_BIN" "$PING_ARGS"
    [ -n "$IPERF_ARGS" ] && printf 'CARGA (fondo): %s %s\n' "$IPERF3_BIN" "$IPERF_ARGS"
  else
    printf 'SSH: %s %s %s -- bash -s (sondea, carga y mide en el nodo)\n' \
      "$SSH_BIN" "${SSH_OPTS[*]}" "$TARGET"
    printf 'PING: %s %s\n' "$PING_BIN" "$PING_ARGS"
    [ -n "$IPERF_ARGS" ] && printf 'CARGA (fondo): %s %s\n' "$IPERF3_BIN" "$IPERF_ARGS"
  fi
  exit 0
fi

# La sonda viaja como script autocontenido: un solo viaje de ssh cubre
# chequeo de herramienta, carga de fondo, pings y limpieza. Las variables
# van INYECTADAS en el guion (no como argumentos): ssh reconstruye la orden
# como una sola linea y un argumento vacio intermedio se perderia.
read -r -d '' PROBE_BODY <<'PROBE_EOF' || true
set -u
command -v "$PING_BIN" >/dev/null 2>&1 || {
  echo "latency_pings: ping no disponible ($PING_BIN) en el objetivo" >&2
  exit 97
}
if [ -n "$IPERF_BIN" ] && ! command -v "$IPERF_BIN" >/dev/null 2>&1; then
  echo "latency_pings: iperf3 no disponible ($IPERF_BIN): no se puede saturar el bucket" >&2
  exit 97
fi
LOAD_LOG=""; LOAD_PID=""
if [ -n "$IPERF_BIN" ]; then
  LOAD_LOG=$(mktemp 2>/dev/null || echo "${TMPDIR:-/tmp}/latency_pings_iperf.$$.log")
  "$IPERF_BIN" $IPERF_ARGS </dev/null >"$LOAD_LOG" 2>&1 &
  LOAD_PID=$!
fi
$PING_BIN $PING_ARGS
rc=$?
if [ -n "$LOAD_PID" ]; then
  kill "$LOAD_PID" 2>/dev/null || true
  wait "$LOAD_PID" 2>/dev/null || true
  # iperf3 sin servidor se queja aqui: aviso, no fallo de latencia.
  if [ -n "$LOAD_LOG" ] && [ -s "$LOAD_LOG" ]; then
    LC_ALL=C head -1 "$LOAD_LOG" | sed 's/^/latency_pings: iperf3: /' >&2
  fi
  [ -n "$LOAD_LOG" ] && rm -f "$LOAD_LOG"
fi
exit $rc
PROBE_EOF

PROBE=$(printf 'PING_BIN=%q\nPING_ARGS=%q\nIPERF_BIN=%q\nIPERF_ARGS=%q\n%s\n' \
  "$PING_BIN" "$PING_ARGS" "$IPERF_BIN" "$IPERF_ARGS" "$PROBE_BODY")

if [ "$TARGET" = local ]; then
  OUT=$(bash -c "$PROBE")
else
  command -v "$SSH_BIN" >/dev/null 2>&1 || die "ssh no disponible para el objetivo $TARGET" 3
  OUT=$(printf '%s\n' "$PROBE" | "$SSH_BIN" "${SSH_OPTS[@]}" "$TARGET" bash -s)
fi
RC=$?

if [ "$RC" -eq 97 ]; then
  exit 3
fi
if [ "$RC" -eq 255 ] && [ "$TARGET" != local ]; then
  die "no se pudo alcanzar el objetivo por ssh ($TARGET)" 3
fi

# Percentiles por rango-nearest sobre los RTT de cada respuesta.
RTTS=$(printf '%s\n' "$OUT" | LC_ALL=C awk '
{
  line = $0
  gsub(/\r/, "", line)
  if (line !~ /[bB][yY][tT][eE][sS]/) next
  if (line !~ /[tT][tT][lL]/) next
  val = ""
  s = line
  while (match(s, /[0-9]+([.][0-9]+)?[ ]*m/)) {
    tok = substr(s, RSTART, RLENGTH)
    gsub(/[^0-9.,]/, "", tok)
    gsub(/,/, ".", tok)
    val = tok
    s = substr(s, RSTART + RLENGTH)
  }
  if (val != "") print val
}')

STATS=$(printf '%s\n' "$RTTS" | LC_ALL=C awk 'NF' | LC_ALL=C sort -n | LC_ALL=C awk '
function pct(p,   i) {
  i = int(p * n / 100)
  if (i * 100 < p * n) i++
  if (i < 1) i = 1
  if (i > n) i = n
  return v[i]
}
{ n++; v[n] = $1; sum += $1; if (max == "" || $1 > max) max = $1; if (min == "" || $1 < min) min = $1 }
END {
  if (n == 0) { print "n=0"; exit }
  printf "n=%d\nmin=%.2f\nmean=%.2f\np50=%.2f\np95=%.2f\np99=%.2f\nmax=%.2f\n", \
    n, min, sum / n, pct(50), pct(95), pct(99), max
}')

stat() { printf '%s\n' "$STATS" | LC_ALL=C awk -F= -v k="$1" '$1 == k { print $2 }'; }
N=$(stat n); N=${N:-0}
P50=$(stat p50); P95=$(stat p95); P99=$(stat p99)
MAXV=$(stat max); MEAN=$(stat mean); MINV=$(stat min)
[ -n "$N" ] || N=0
LOSS=$(awk -v s="$COUNT" -v r="$N" 'BEGIN{ l = (s - r) / s * 100; if (l < 0) l = 0; printf "%.1f", l }')

VERDICT=PASS
EXIT_CODE=0
if [ "$N" -eq 0 ]; then
  VERDICT=FAIL
  EXIT_CODE=1
elif [ "$(awk -v p="${P95:-0}" -v t="$THRESHOLD" 'BEGIN{ print (p <= t) ? 1 : 0 }')" = 1 ]; then
  VERDICT=PASS
  EXIT_CODE=0
else
  VERDICT=FAIL
  EXIT_CODE=1
fi

SAT_JSON=null
[ -n "$SATURATE" ] && SAT_JSON="$SATURATE"

if [ "$JSON" = 1 ]; then
  printf '{"target":"%s","os":"%s","host":"%s","count":%s,"recv":%s,"loss_pct":%s,"p50_ms":%s,"p95_ms":%s,"p99_ms":%s,"max_ms":%s,"mean_ms":%s,"min_ms":%s,"threshold_p95_ms":%s,"saturate_mbps":%s,"verdict":"%s"}\n' \
    "$TARGET" "$OS_TAG" "$HOST_DEST" "$COUNT" "$N" "$LOSS" \
    "${P50:-0}" "${P95:-0}" "${P99:-0}" "${MAXV:-0}" "${MEAN:-0}" "${MINV:-0}" \
    "$THRESHOLD" "$SAT_JSON" "$VERDICT"
else
  printf 'latency_pings: objetivo=%s os=%s destino=%s pings=%d/%d (perdida %s%%)\n' \
    "$TARGET" "$OS_TAG" "$HOST_DEST" "$N" "$COUNT" "$LOSS"
  if [ "$N" -gt 0 ]; then
    printf 'latency_pings: p50=%s ms  p95=%s ms  p99=%s ms  max=%s ms  (media %s, min %s)\n' \
      "$P50" "$P95" "$P99" "$MAXV" "$MEAN" "$MINV"
  else
    printf 'latency_pings: ninguna respuesta, p95 indeterminable\n' >&2
  fi
  printf 'latency_pings: veredicto %s (umbral p95 %s ms%s)\n' "$VERDICT" "$THRESHOLD" \
    "$([ -n "$SATURATE" ] && printf ', saturado a %s Mbps' "$SATURATE")"
fi

exit "$EXIT_CODE"
