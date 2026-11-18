#!/usr/bin/env bash
# load_250_1000.sh — carga real del plano de control del gateway a 250 y
# 1.000 nodos, con P99 de seleccion y porcentaje de streams saturados.
#
# No reimplementa la carga: dirige el test Go ya existente,
# cmd/botnet-gw/load_real_test.go (TestGwLoadReal), pasandole el tamano del
# pool por LOAD_NODES y pidiendole el informe JSON por LOAD_JSON_OUT. El test
# levanta el fixture real (PoolReader + GeoIndex + AdmissionPolicy con los
# limites de produccion: ClassCap y MaxActiveTenantsPerNode) y mide:
#   - p99_ms: P99 de la seleccion real de nodo para cada stream nuevo.
#   - streams_saturated_pct: % de aperturas rechazadas por saturacion real
#     de admision (el fan-out de tenant se cuenta aparte, no como
#     saturacion del pool).
# El alambre iperf3 (ancho de banda real) queda fuera: lo documenta el
# campo latency_scope del informe — es BLOCKED-HW.
#
# Umbrales del piloto: P99 < 1.500 ms y saturacion < 10 %.
#
# Uso:
#   bash scripts/field/load_250_1000.sh [--nodes 250,1000]
#     [--json-out docs/verification/gw_load_real.json] [--dry] [--json] [--help]
#
# Salida: humano por stdout; con --json SOLO el JSON agregado va por stdout
# (los avisos salen por stderr). El agregado se escribe SIEMPRE en
# --json-out y tiene la forma:
#   {generated_by, threshold_ms, threshold_streams_pct,
#    runs: [{nodos, gateways, p99_ms, saturated_pct, nota, verdict}, ...],
#    verdict}
#
# Exit: 0 todos los tramos en verde | 1 algun tramo en FALLO |
# 2 argumentos invalidos | 3 herramienta ausente (jq).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2

# Regla del repo: todo lo que invoque go pasa por aqui primero (Go portable,
# GOFLAGS, GOTMPDIR fuera de %TEMP%; ver scripts/env.sh).
# shellcheck disable=SC1091
source scripts/env.sh || exit 3

USAGE='load_250_1000.sh — carga real del gateway a N nodos (P99 y saturacion)

Uso:
  bash scripts/field/load_250_1000.sh [--nodes 250,1000]
       [--json-out docs/verification/gw_load_real.json] [--dry] [--json] [--help]

  --nodes LISTA   tamanos de pool separados por comas (defecto 250,1000).
                  Cada N lanza UNA ejecucion de TestGwLoadReal con
                  LOAD_NODES=N (cmd/botnet-gw/load_real_test.go).
  --json-out F    donde escribir el informe agregado (defecto
                  docs/verification/gw_load_real.json). Se escribe siempre,
                  con o sin --json.
  --dry           imprime el plan (que N, que comando Go, donde cae el JSON)
                  y no ejecuta nada.
  --json          salida JSON por stdout (solo el JSON; los avisos van a
                  stderr).
  --help          esta ayuda.

Umbrales: P99 < 1.500 ms y streams_saturated_pct < 10 %.
Salidas: 0 todo en verde | 1 algun FALLO | 2 argumentos | 3 herramienta ausente.'

log() { # avisos: con --json van a stderr para dejar el stdout limpio
  if [ "${JSON:-0}" = 1 ]; then printf '%s\n' "$*" >&2; else printf '%s\n' "$*"; fi
}
die() { # die <msg> <exit>
  printf 'load_250_1000: %s\n' "$1" >&2
  exit "${2:-2}"
}

# ---------------------------------------------------------------- argumentos
NODES_ARG="250,1000"
JSON_OUT="docs/verification/gw_load_real.json"
DRY=0 JSON=0

while [ $# -gt 0 ]; do
  case "$1" in
    --nodes) NODES_ARG="${2:-}"; shift 2 || true ;;
    --nodes=*) NODES_ARG="${1#*=}"; shift ;;
    --json-out) JSON_OUT="${2:-}"; shift 2 || true ;;
    --json-out=*) JSON_OUT="${1#*=}"; shift ;;
    --dry) DRY=1; shift ;;
    --json) JSON=1; shift ;;
    -h|--help) printf '%s\n' "$USAGE"; exit 0 ;;
    *) die "argumento no reconocido: $1" 2 ;;
  esac
done

[ -n "$NODES_ARG" ] || die "--nodes quiere una lista como 250,1000" 2
[ -n "$JSON_OUT" ] || die "--json-out quiere una ruta" 2
command -v jq >/dev/null 2>&1 || die "jq no disponible" 3
command -v go >/dev/null 2>&1 || die "go no disponible tras source scripts/env.sh" 3

IFS=',' read -r -a NODES <<< "$NODES_ARG"
for n in "${NODES[@]}"; do
  case "$n" in ''|*[!0-9]*) die "--nodes: '$n' no es un entero >= 1" 2 ;; esac
  [ "$n" -ge 1 ] || die "--nodes: '$n' debe ser >= 1" 2
done

# ---------------------------------------------------------------------- plan
GO_PKG="./cmd/botnet-gw"
GO_CMD=(go test "$GO_PKG" -run '^TestGwLoadReal$' -count=1)
GO_SHOW="${GO_CMD[*]}"

if [ "$DRY" = 1 ]; then
  if [ "$JSON" = 1 ]; then
    jq -n --arg nodes "$NODES_ARG" --arg json_out "$JSON_OUT" --arg cmd "$GO_SHOW" \
      '{dry:true, nodes:($nodes|split(",")|map(tonumber)), comando_go:$cmd,
        json_out:$json_out, umbral_p99_ms:1500, umbral_saturacion_pct:10,
        nota:"cada N lanza TestGwLoadReal con LOAD_NODES=N (cmd/botnet-gw/load_real_test.go)"}'
  else
    printf 'load_250_1000 (plan): %s nodos -> %s\n' "$NODES_ARG" "$GO_SHOW"
    printf '  entorno: LOAD_NODES=<N> LOAD_JSON_OUT=<informe por N>\n'
    printf '  agregado: %s\n' "$JSON_OUT"
    printf '  umbrales: P99 < 1500 ms, saturacion < 10 %%\n'
    printf '  (modo seco: no se ha ejecutado nada)\n'
  fi
  exit 0
fi

# ------------------------------------------------------------------- ejecucion
WORK="$(mktemp -d "${TMPDIR:-/tmp}/load_250_1000.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$(dirname "$JSON_OUT")" 2>/dev/null || true

: > "$WORK/runs.jsonl"
todos_ok=1
for n in "${NODES[@]}"; do
  informe="$WORK/gw_load_$n.json"
  log "load_250_1000: lanzando carga con $n nodos (puede tardar unos segundos)..."
  # El test escribe el informe ANTES de comprobar umbrales, asi que un JSON
  # con datos validos existe incluso cuando el tramo sale FALLO por umbral.
  LOAD_NODES="$n" LOAD_JSON_OUT="$informe" "${GO_CMD[@]}" > "$WORK/go_$n.log" 2>&1
  go_rc=$?

  if [ -s "$informe" ]; then
    p99="$(jq -r '.["p99_ms"]' "$informe" 2>/dev/null)"
    sat="$(jq -r '.["streams_saturated_pct"]' "$informe" 2>/dev/null)"
    gws="$(jq -r '.["gateways"]' "$informe" 2>/dev/null)"
    case "$p99$sat" in *[!0-9.]*|'') p99=""; sat="" ;; esac
  else
    p99=""; sat=""; gws=""
  fi

  if [ -z "$p99" ] || [ -z "$sat" ]; then
    # Sin numeros no hay veredicto honesto: FALLO (t1 o t2 de go-test).
    nota="sin informe JSON (go test rc=$go_rc; ver $WORK/go_$n.log)"
    run_verdict=FALLO
    todos_ok=0
  else
    p99_ok="$(awk -v a="$p99" 'BEGIN { print (a < 1500) ? 1 : 0 }')"
    sat_ok="$(awk -v a="$sat" 'BEGIN { print (a < 10) ? 1 : 0 }')"
    if [ "$go_rc" -ne 0 ]; then
      nota="go test rc=$go_rc (umbral o fallo interno)"
      run_verdict=FALLO
      todos_ok=0
    elif [ "$p99_ok" = 1 ] && [ "$sat_ok" = 1 ]; then
      nota="verificado en el test Go (plano de control)"
      run_verdict=PASS
    else
      nota="P99 o saturacion fuera de umbral"
      run_verdict=FALLO
      todos_ok=0
    fi
  fi

  jq -n --argjson nodos "$n" --argjson gws "$( [ -n "$gws" ] && printf '%s' "$gws" || echo null )" \
    --argjson p99 "$( [ -n "$p99" ] && printf '%s' "$p99" || echo null )" \
    --argjson sat "$( [ -n "$sat" ] && printf '%s' "$sat" || echo null )" \
    --arg nota "$nota" --arg verdict "$run_verdict" \
    '{nodos:$nodos, gateways:$gws, p99_ms:$p99, saturated_pct:$sat,
      nota:$nota, verdict:$verdict}' >> "$WORK/runs.jsonl"

  log "  $n nodos: p99=${p99:-?} ms, saturacion=${sat:-?} % -> $run_verdict"
done

if [ "$todos_ok" = 1 ]; then agregado_verdict=PASS; else agregado_verdict=FALLO; fi

jq -s \
  --arg json_out "$JSON_OUT" \
  --arg verdict "$agregado_verdict" \
  '{generated_by:"scripts/field/load_250_1000.sh -> cmd/botnet-gw/load_real_test.go (LOAD_NODES)",
    threshold_ms:1500, threshold_streams_pct:10,
    runs:., verdict:$verdict}' "$WORK/runs.jsonl" > "$JSON_OUT"

if [ "$JSON" = 1 ]; then
  cat "$JSON_OUT"
else
  printf 'load_250_1000: agregado escrito en %s\n' "$JSON_OUT"
fi

if [ "$agregado_verdict" = PASS ]; then exit 0; else exit 1; fi
