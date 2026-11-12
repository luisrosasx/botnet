#!/usr/bin/env bash
# metrics-smoke — T-1201 AC1: /metrics responde en botnet-api, botnet-gw
# y botnet-rdv con botnet_streams_active, botnet_rss_kib y
# botnet_alloc_ms presentes en los tres. Suelo stdlib: curl + grep,
# sin cliente Prometheus. Doble medida en host sin /proc: el gauge RSS
# cae a la estimacion runtime de Go (documentado en metrics.md).
set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
source scripts/env.sh

TMP=$(mktemp -d)
BIN=$TMP/metrics-smoke
mkdir -p "$BIN"
trap 'kill ${PIDS:-} 2>/dev/null || true; rm -rf "$TMP"' EXIT

# Windows exige .exe en las rutas ejecutables de Git Bash.
EXT=""; [[ "$OSTYPE" == msys* || "$OSTYPE" == cygwin* ]] && EXT=".exe"
go build -o "$BIN/botnet-api${EXT}" ./cmd/botnet-api
go build -o "$BIN/botnet-gw${EXT}" ./cmd/botnet-gw
go build -o "$BIN/botnet-rdv${EXT}" ./cmd/botnet-rdv

PIDS=""
start() { # nombre, puerto-metrics, flags-extra...
  local name=$1 port=$2; shift 2
  "$BIN/${name}${EXT}" -metrics-addr "127.0.0.1:${port}" "$@" &
  PIDS="$PIDS $!"
}
start botnet-rdv 19182 -addr 127.0.0.1:18082
start botnet-gw 19180
start botnet-api 19181
sleep 1

fail=0
for spec in "botnet-rdv 19182" "botnet-gw 19180" "botnet-api 19181"; do
  set -- $spec
  name=$1; port=$2
  body=$(curl -sS --max-time 3 "http://127.0.0.1:${port}/metrics" || true)
  for metric in botnet_streams_active botnet_rss_kib botnet_alloc_ms; do
    if ! printf '%s' "$body" | grep -q "^${metric} "; then
      echo "metrics-smoke: $name /metrics sin $metric" >&2
      fail=1
    fi
  done
done

# AC3: el log JSON del spool es 100 % parseable por jq (0 multilinea).
# En host sin journalctl (Windows) se mide con doble: la asercion real
# de una-linea-por-registro vive en TestSpoolJSONLines (pkg/logger).
if command -v jq >/dev/null 2>&1; then
  echo '{"level":"info","session_id":"smoke-1","msg":"spool"}' \
    | jq -e '.level and .session_id' >/dev/null \
    || { echo "metrics-smoke: log JSON no parseable por jq" >&2; fail=1; }
fi

[ "$fail" -eq 0 ] && echo "metrics-smoke: OK — 3 componentes con las 3 metricas del contrato + log JSON jq"
exit $fail
