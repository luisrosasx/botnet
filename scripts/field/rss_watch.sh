#!/usr/bin/env bash
# rss_watch.sh — EP-03 T-0301 AC3 (RSS <= 3,1 MB en reposo 10 min) y
# EP-06 T-0603 AC2 (RSS real con 150 streams <= 6 MB): muestreo periodico
# de la memoria residente de un proceso y veredicto contra un umbral.
#
# Uso:
#   bash scripts/field/rss_watch.sh --target <ssh-host|local> --pid <pid|nombre> \
#     [--minutes 10] [--interval 5] [--threshold-mb 3.1] [--json] [--dry] [--help]
#
# Donde se lee la memoria:
#   - Objetivo remoto (nodo Linux de la malla) o local en Linux: `ps -o rss=`,
#     que da KB -> MB.
#   - Objetivo local en Windows (host F4 con Git Bash): no hay /proc accesible
#     ni `ps -o rss=`, asi que se lee el working set con `powershell`
#     (Get-Process/WorkingSet64): es lo mas cercano a RSS sin permisos
#     especiales, y queda anotado en el JSON (campo metric) para no confundir
#     las dos magnitudes.
#   - Un pid escrito desde Git Bash es pid MSYS, distinto del pid de Windows:
#     si Get-Process no lo encuentra se traduce por WINPID con `ps -W` y se
#     reintenta una vez.
#
# --pid acepta numero o nombre de proceso (sin .exe). Con nombre se suma la
# memoria de TODAS las coincidencias (el grupo del proceso); en Windows la
# propia lectura se incluye si el nombre consultado es powershell.
#
# El veredicto usa el PEAK del periodo — lectura estricta del criterio
# «RSS <= 3,1 MB en reposo»; media, ultima y deriva (growth_mb) salen tambien
# para ver si la memoria sube (fuga) aunque el pico entre en umbral.
#
# Salidas: 0 pico <= umbral; 1 pico > umbral o proceso sin lecturas;
# 2 argumentos invalidos; 3 falta la herramienta (powershell/ps, o ssh).
set -uo pipefail

usage() {
  cat <<'EOF'
rss_watch.sh — muestrea la memoria residente de un proceso y decide contra un umbral.

Uso:
  bash scripts/field/rss_watch.sh --target <ssh-host|local> --pid <pid|nombre> [opciones]

Opciones:
  --target <ssh-host|local>  donde vive el proceso (obligatorio)
  --pid <pid|nombre>         pid numerico o nombre del proceso (obligatorio)
  --minutes M                duracion del muestreo en minutos (defecto 10; admite 0.2)
  --interval S               separacion entre muestras en segundos (defecto 5)
  --threshold-mb MB          umbral de memoria en MB (defecto 3.1, EP-03 T-0301 AC3)
  --json                     salida JSON en una linea
  --dry                      imprime los comandos y no ejecuta nada
  -h, --help                 esta ayuda

Salidas: 0 pico <= umbral | 1 pico > umbral o sin lecturas | 2 argumentos | 3 herramienta ausente.
Variables: PS_BIN, POWERSHELL_BIN, SSH_BIN (dobles para pruebas sin la herramienta real).
EOF
}

TARGET=""
PIDSEL=""
MINUTES=10
INTERVAL=5
THRESHOLD=3.1
JSON=0
DRY=0

die() {
  printf 'rss_watch: %s\n' "$1" >&2
  exit "${2:-2}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --target)     [ $# -ge 2 ] || die '--target necesita un valor'; TARGET="$2"; shift 2 ;;
    --target=*)   TARGET="${1#*=}"; shift ;;
    --pid)        [ $# -ge 2 ] || die '--pid necesita un valor'; PIDSEL="$2"; shift 2 ;;
    --pid=*)      PIDSEL="${1#*=}"; shift ;;
    --minutes)    [ $# -ge 2 ] || die '--minutes necesita un valor'; MINUTES="$2"; shift 2 ;;
    --minutes=*)  MINUTES="${1#*=}"; shift ;;
    --interval)   [ $# -ge 2 ] || die '--interval necesita un valor'; INTERVAL="$2"; shift 2 ;;
    --interval=*) INTERVAL="${1#*=}"; shift ;;
    --threshold-mb) [ $# -ge 2 ] || die '--threshold-mb necesita un valor'; THRESHOLD="$2"; shift 2 ;;
    --threshold-mb=*) THRESHOLD="${1#*=}"; shift ;;
    --json)       JSON=1; shift ;;
    --dry)        DRY=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *)            die "argumento desconocido: $1" 2 ;;
  esac
done

[ -n "$TARGET" ] || die 'falta --target <ssh-host|local> (usa --help)' 2
[ -n "$PIDSEL" ] || die 'falta --pid <pid|nombre> (usa --help)' 2
is_num() { printf '%s' "$1" | LC_ALL=C awk '{ if ($0 ~ /^[0-9]+([.][0-9]+)?$/) exit 0; exit 1 }'; }
is_num "$MINUTES" || die '--minutes debe ser un numero > 0' 2
[[ "$INTERVAL" =~ ^[0-9]+$ ]] && [ "$INTERVAL" -gt 0 ] || die '--interval debe ser un entero > 0' 2
is_num "$THRESHOLD" || die '--threshold-mb debe ser un numero >= 0' 2

PS_BIN="${PS_BIN:-ps}"
POWERSHELL_BIN="${POWERSHELL_BIN:-powershell}"
SSH_BIN="${SSH_BIN:-ssh}"
SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=8)

OS_TAG=linux
case "$(uname -s 2>/dev/null)" in
  MINGW* | MSYS* | CYGWIN*) OS_TAG=windows ;;
esac
[ "$TARGET" = local ] || OS_TAG=linux

# Nombre de proceso sin .exe (Get-Process y ps -C lo quieren limpio) y sin
# espacios: con nombre libre se podria inyectar codigo en el sondeo remoto.
NAMESEL="${PIDSEL%.exe}"
if [[ "$NAMESEL" =~ ^[0-9]+$ ]]; then
  IS_NUMERIC_PID=1
else
  IS_NUMERIC_PID=0
  [[ "$NAMESEL" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*$ ]] ||
    die '--pid debe ser un numero o un nombre de proceso simple (sin espacios)' 2
fi

TOTAL_S=$(awk -v m="$MINUTES" 'BEGIN{ s = int(m * 60 + 0.5); if (s < 1) s = 1; printf "%d", s }')
N_SAMPLES=$(( (TOTAL_S + INTERVAL - 1) / INTERVAL ))
[ "$N_SAMPLES" -ge 1 ] || N_SAMPLES=1

# --- lectura de una muestra: deja "<n_procesos> <crudo>" en stdout -------
# crudo = bytes en Windows (WorkingSet64) o KB en Linux (ps rss).
if [ "$OS_TAG" = windows ]; then
  METRIC=working_set_bytes
  UNIT_DIV=1048576
  # El selector va SUSTITUIDO en el guion, no como argumento: con
  # `-Command "<texto>"` PowerShell añade los argumentos restantes al propio
  # texto (los concatena) y $args[0] nunca llega a rellenarse.
  read -r -d '' SNAPSHOT_TEMPLATE <<'PS_EOF' || true
$ErrorActionPreference = "SilentlyContinue"
$sel = '__SEL__'
if ($sel -match '^\d+$') {
  $procs = @(Get-Process -Id ([int]$sel))
} else {
  $procs = @(Get-Process -Name $sel)
}
if ($procs.Count -eq 0) { exit 42 }
$bytes = [long](($procs | Measure-Object -Property WorkingSet64 -Sum).Sum)
"{0} {1}" -f $procs.Count, $bytes
PS_EOF
else
  METRIC=rss_kb
  UNIT_DIV=1024
  # El probe viaja como guion autocontenido con los valores inyectados: ssh
  # reconstruye la orden como una sola linea y un argumento vacio se perderia.
  read -r -d '' LINUX_BODY <<'PROBE_EOF' || true
set -u
command -v "$PS_BIN" >/dev/null 2>&1 || {
  echo "rss_watch: ps no disponible ($PS_BIN) en el objetivo" >&2
  exit 97
}
if [ "$IS_NUMERIC_PID" = 1 ]; then
  RAW=$("$PS_BIN" -o rss= -p "$SEL" 2>/dev/null | LC_ALL=C awk 'BEGIN{ s = 0 } NF { s += $1 } END { print s + 0 }')
  NPROC=$("$PS_BIN" -p "$SEL" 2>/dev/null | LC_ALL=C awk 'END { print NR > 1 ? NR - 1 : 0 }')
else
  RAW=$("$PS_BIN" -o rss= -C "$SEL" 2>/dev/null | LC_ALL=C awk 'BEGIN{ s = 0 } NF { s += $1 } END { print s + 0 }')
  NPROC=$("$PS_BIN" -C "$SEL" 2>/dev/null | LC_ALL=C awk 'END { print NR > 1 ? NR - 1 : 0 }')
fi
if [ -z "${RAW:-}" ] || [ "$RAW" = "0" ] || [ "${NPROC:-0}" = "0" ]; then
  echo "rss_watch: proceso $SEL sin memoria leible ($PS_BIN)" >&2
  exit 98
fi
printf '%s %s\n' "$NPROC" "$RAW"
PROBE_EOF
  LINUX_PROBE=$(printf 'PS_BIN=%q\nSEL=%q\nIS_NUMERIC_PID=%q\n%s\n' \
    "$PS_BIN" "$NAMESEL" "$IS_NUMERIC_PID" "$LINUX_BODY")
fi

sample_cmd() {
  # Una lectura, escrita como comando: para --dry y para el aviso de error.
  if [ "$OS_TAG" = windows ]; then
    printf '%s -NoProfile -Command <Get-Process %s: WorkingSet64 sum>' "$POWERSHELL_BIN" "$NAMESEL"
  elif [ "$IS_NUMERIC_PID" = 1 ]; then
    printf '%s -o rss= -p %s' "$PS_BIN" "$NAMESEL"
  else
    printf '%s -o rss= -C %s' "$PS_BIN" "$NAMESEL"
  fi
}

if [ "$DRY" = 1 ]; then
  printf '# objetivo=%s os=%s metrica=%s lecturas=%dx cada %ss durante %ss\n' \
    "$TARGET" "$OS_TAG" "$METRIC" "$N_SAMPLES" "$INTERVAL" "$TOTAL_S"
  printf 'MUESTRA: %s\n' "$(sample_cmd)"
  if [ "$TARGET" = local ]; then
    printf 'EJECUCION: local (sin ssh)\n'
  else
    printf 'EJECUCION: %s %s %s -- bash -s\n' "$SSH_BIN" "${SSH_OPTS[*]}" "$TARGET"
  fi
  printf 'VEREDICTO: pico <= %s MB\n' "$THRESHOLD"
  exit 0
fi

if [ "$OS_TAG" = windows ] && ! command -v "$POWERSHELL_BIN" >/dev/null 2>&1; then
  printf 'rss_watch: powershell no disponible (%s): sin lectura de memoria local\n' "$POWERSHELL_BIN" >&2
  exit 3
fi
if [ "$OS_TAG" = linux ] && [ "$TARGET" = local ] && ! command -v "$PS_BIN" >/dev/null 2>&1; then
  printf 'rss_watch: ps no disponible (%s): sin lectura de memoria local\n' "$PS_BIN" >&2
  exit 3
fi
if [ "$TARGET" != local ] && ! command -v "$SSH_BIN" >/dev/null 2>&1; then
  printf 'rss_watch: ssh no disponible para el objetivo %s\n' "$TARGET" >&2
  exit 3
fi

SAMPLES=""
READ_OK=0
MISS=0
TOOL_MISSING=0
WINPID_USED=""
WINPID_TRIED=0
# Selector efectivamente resuelto: cambia a WINPID si el pid MSYS no existe
# en Windows, y a partir de ahi TODAS las muestras lo reutilizan.
RESOLVED_SEL="$NAMESEL"

take_sample() {
  # Deja en SAMPLES_LINE "<n> <crudo>", o vacio si esta muestra no sale.
  local out rc script
  SAMPLES_LINE=""
  out=""
  rc=1
  if [ "$OS_TAG" = windows ]; then
    script=${SNAPSHOT_TEMPLATE//__SEL__/"$RESOLVED_SEL"}
    out=$("$POWERSHELL_BIN" -NoProfile -Command "$script" 2>/dev/null)
    rc=$?
    if [ "$rc" -ne 0 ] && [ "$IS_NUMERIC_PID" = 1 ] && [ "$WINPID_TRIED" = 0 ]; then
      # pid MSYS de Git Bash != pid de Windows: se traduce por WINPID.
      WINPID_TRIED=1
      local winpid
      winpid=$("$PS_BIN" -W 2>/dev/null | LC_ALL=C awk -v p="$NAMESEL" '$1 == p { print $4; exit }')
      if [ -n "${winpid:-}" ]; then
        WINPID_USED=$winpid
        RESOLVED_SEL=$winpid
        script=${SNAPSHOT_TEMPLATE//__SEL__/"$winpid"}
        out=$("$POWERSHELL_BIN" -NoProfile -Command "$script" 2>/dev/null)
        rc=$?
      fi
    fi
    if [ "$rc" -eq 42 ]; then
      TOOL_MISSING=2
    elif [ "$rc" -ne 0 ]; then
      TOOL_MISSING=1
    fi
    if [ "$rc" -eq 0 ] && [ -n "${out:-}" ]; then
      SAMPLES_LINE=$(printf '%s' "$out" | LC_ALL=C tr -d '\r' | LC_ALL=C awk 'NF { print; exit }')
    fi
  elif [ "$TARGET" = local ]; then
    out=$(bash -c "$LINUX_PROBE" 2>/dev/null)
    rc=$?
    [ "$rc" -eq 97 ] && TOOL_MISSING=1
    SAMPLES_LINE=$out
  else
    out=$(printf '%s\n' "$LINUX_PROBE" | "$SSH_BIN" "${SSH_OPTS[@]}" "$TARGET" bash -s 2>/dev/null)
    rc=$?
    [ "$rc" -eq 255 ] && die "no se pudo alcanzar el objetivo por ssh ($TARGET)" 3
    [ "$rc" -eq 97 ] && TOOL_MISSING=1
    SAMPLES_LINE=$out
  fi
  return 0
}

i=1
while [ "$i" -le "$N_SAMPLES" ]; do
  take_sample
  if [ -n "${SAMPLES_LINE:-}" ]; then
    SAMPLES="${SAMPLES}${SAMPLES_LINE}
"
    READ_OK=$((READ_OK + 1))
    MB=$(printf '%s' "$SAMPLES_LINE" | LC_ALL=C awk -v d="$UNIT_DIV" '{ printf "%.3f", $2 / d }')
    [ "$JSON" = 1 ] || printf 'rss_watch: t=%ss %s MB\n' "$(( (i - 1) * INTERVAL ))" "$MB"
  else
    MISS=$((MISS + 1))
  fi
  [ "$i" -lt "$N_SAMPLES" ] && sleep "$INTERVAL"
  i=$((i + 1))
done

if [ "$READ_OK" -eq 0 ] && [ "$TOOL_MISSING" = 1 ]; then
  printf 'rss_watch: herramienta de lectura ausente en %s (%s)\n' "$TARGET" \
    "$([ "$OS_TAG" = windows ] && printf '%s' "$POWERSHELL_BIN" || printf '%s/ssh' "$PS_BIN")" >&2
  exit 3
fi

if [ "$READ_OK" -eq 0 ]; then
  printf 'rss_watch: proceso %s sin lecturas en %s muestras — no existe o murio (leeria: %s)\n' \
    "$PIDSEL" "$N_SAMPLES" "$(sample_cmd)" >&2
  if [ "$JSON" = 1 ]; then
    printf '{"target":"%s","os":"%s","pid":"%s","metric":"%s","samples":0,"peak_mb":0,"mean_mb":0,"last_mb":0,"threshold_mb":%s,"verdict":"NOPROCESO"}\n' \
      "$TARGET" "$OS_TAG" "$PIDSEL" "$METRIC" "$THRESHOLD"
  fi
  exit 1
fi

STATS=$(printf '%s' "$SAMPLES" | LC_ALL=C awk -v d="$UNIT_DIV" '
NF {
  n++; v[n] = $2 / d; sum += v[n]
  if (max == "" || v[n] > max) max = v[n]
  if (first == "") first = v[n]
  last = v[n]
}
END {
  if (n == 0) { print "n=0"; exit }
  printf "n=%d\nfirst=%.3f\nlast=%.3f\npeak=%.3f\nmean=%.3f\ngrowth=%.3f\n", \
    n, first, last, max, sum / n, last - first
}')

stat() { printf '%s\n' "$STATS" | LC_ALL=C awk -F= -v k="$1" '$1 == k { print $2 }'; }
N=$(stat n); N=${N:-0}
FIRST=$(stat first); LAST=$(stat last); PEAK=$(stat peak); MEAN=$(stat mean); GROWTH=$(stat growth)

if [ "$(awk -v p="${PEAK:-0}" -v t="$THRESHOLD" 'BEGIN{ print (p <= t) ? 1 : 0 }')" = 1 ]; then
  VERDICT=PASS
  EXIT_CODE=0
else
  VERDICT=FAIL
  EXIT_CODE=1
fi

PID_JSON="$PIDSEL"
[ -n "$WINPID_USED" ] && PID_JSON="$PIDSEL(winpid=$WINPID_USED)"

if [ "$JSON" = 1 ]; then
  printf '{"target":"%s","os":"%s","pid":"%s","metric":"%s","interval_s":%s,"minutes":%s,"samples":%s,"missing":%s,"first_mb":%s,"last_mb":%s,"peak_mb":%s,"mean_mb":%s,"growth_mb":%s,"threshold_mb":%s,"verdict":"%s"}\n' \
    "$TARGET" "$OS_TAG" "$PID_JSON" "$METRIC" "$INTERVAL" "$MINUTES" "$READ_OK" "$MISS" \
    "${FIRST:-0}" "${LAST:-0}" "${PEAK:-0}" "${MEAN:-0}" "${GROWTH:-0}" "$THRESHOLD" "$VERDICT"
else
  printf 'rss_watch: objetivo=%s os=%s proceso=%s lecturas=%d (faltaron %d), metrica=%s\n' \
    "$TARGET" "$OS_TAG" "$PID_JSON" "$READ_OK" "$MISS" "$METRIC"
  printf 'rss_watch: primera=%s MB  ultima=%s MB  pico=%s MB  media=%s MB  deriva=%s MB\n' \
    "$FIRST" "$LAST" "$PEAK" "$MEAN" "$GROWTH"
  printf 'rss_watch: veredicto %s (umbral %s MB)\n' "$VERDICT" "$THRESHOLD"
fi

exit "$EXIT_CODE"
