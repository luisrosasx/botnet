#!/usr/bin/env bash
# EP-08 T-0805: contract-test del contrato cliente (equivalente a
# `make contract-test`). El contrato ES docs/api/openapi.yaml: se
# construye con curl contra el `serve` real y se verifican los codigos
# y campos exactos del spec.
#
# Requisitos (este host, sin Docker):
#   DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable
#   REDIS_SERVER_BIN (o redis-server en PATH / ya escuchando en 16379)
#   psql en PATH (o PSQL_BIN)
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null
[[ -n "${DATABASE_URL:-}" ]] || { echo "contract_test: DATABASE_URL requerido" >&2; exit 2; }

PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); printf 'PASS %s\n' "$1"; }
bad() { FAIL=$((FAIL+1)); printf 'FAIL %s\n' "$1"; }

PSQL="${PSQL_BIN:-psql}"
command -v "$PSQL" >/dev/null 2>&1 || PSQL="$HOME/pgsql/bin/psql.exe"
PORT="${PORT:-18085}"
KEY="sk-contract-0001"
BASE="http://127.0.0.1:${PORT}"
BODY='{"country":"ES","city":"Madrid","mode":"rotating"}'

# --- motor Redis para el test ----------------------------------------
REDIS_PID=""
REDIS_SERVER_BIN="${REDIS_SERVER_BIN:-$HOME/tools/redis/Redis-7.4.3-Windows-x64-msys2/redis-server.exe}"
redis_cli="${REDIS_CLI:-$HOME/tools/redis/Redis-7.4.3-Windows-x64-msys2/redis-cli.exe}"
if ! (echo >"/dev/tcp/127.0.0.1/16379") 2>/dev/null; then
  [[ -x "$REDIS_SERVER_BIN" ]] || { echo "contract_test: redis-server no disponible" >&2; exit 2; }
  "$REDIS_SERVER_BIN" --port 16379 --save "" --appendonly no --bind 127.0.0.1 >/dev/null 2>&1 &
  REDIS_PID=$!
  sleep 1
fi

# --- base scratch + fixtures -------------------------------------------
SB=botnet_contract
"$PSQL" -tA -c "SELECT 1" "$DATABASE_URL" >/dev/null || { echo "contract_test: Postgres no responde" >&2; exit 2; }
"$PSQL" -q -c "DROP DATABASE IF EXISTS $SB WITH (FORCE)" "$DATABASE_URL" >/dev/null 2>&1
"$PSQL" -q -c "CREATE DATABASE $SB" "$DATABASE_URL" >/dev/null
SB_URL="${DATABASE_URL%/*}/$SB"
if [[ "$DATABASE_URL" == *\?* ]]; then
  SB_URL="${DATABASE_URL%%\?*}"; SB_URL="${SB_URL%/*}/$SB?${DATABASE_URL#*\?}"
fi
DATABASE_URL="$SB_URL" bash scripts/backend/migrate.sh >/dev/null 2>&1 \
  && ok "T-0805.3 migrate scratch exit 0" || bad "migrate scratch"

"$PSQL" -v ON_ERROR_STOP=1 -q "$SB_URL" <<SQL >/dev/null 2>&1
INSERT INTO tenants (tenant_id, name) VALUES ('550e8400-e29b-41d4-a716-446655440001', 'contract');
INSERT INTO quotas (tenant_id, gb_allowance, concurrent_limit, period_start)
  VALUES ('550e8400-e29b-41d4-a716-446655440001', 10, 50, date_trunc('day', now())::date);
INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class, country, city, status, consent_at, last_seen_at)
  VALUES ('550e8400-e29b-41d4-a716-446655440002', 'contractslug', repeat('a',64), 'amd64', 'B', 'ES', 'Madrid', 'live', now(), now());
INSERT INTO api_keys (tenant_id, key_hash, mode)
  VALUES ('550e8400-e29b-41d4-a716-446655440001',
          encode(sha256('$KEY'::text::bytea), 'hex'), 'rotating');
SQL

# --- servidor -----------------------------------------------------------
go build -o /tmp/botnet-api-contract.exe ./cmd/botnet-api || { echo "build fallo" >&2; exit 1; }
DATABASE_URL="$SB_URL" REDIS_URL="redis://127.0.0.1:16379" GW_ADDR="gw.contract:1080" PORT="$PORT" \
  API_ADMIN_TOKEN=dev-admin-token /tmp/botnet-api-contract.exe serve >/tmp/botnet-api-contract.log 2>&1 &
API_PID=$!
trap 'kill $API_PID 2>/dev/null; [[ -n "$REDIS_PID" ]] && kill $REDIS_PID 2>/dev/null; "$PSQL" -q -c "DROP DATABASE IF EXISTS $SB WITH (FORCE)" "$DATABASE_URL" >/dev/null 2>&1' EXIT
for _ in $(seq 1 30); do curl -sf "$BASE/healthz" >/dev/null 2>&1 && break; sleep 0.5; done

# --- AC1: 201 con el campo exacto del spec ------------------------------
resp=$(curl -s -o /tmp/ct201.json -w '%{http_code}' -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" -d "$BODY" "$BASE/api/v1/sessions")
[[ "$resp" == "201" ]] && ok "T-0805.1 201 sesion creada" || bad "POST /api/v1/sessions -> $resp: $(cat /tmp/ct201.json)"
fields=$(grep -c 'session_id\|proxy_url\|expires_at' /tmp/ct201.json)
grep -q '"proxy_url":"socks5h://' /tmp/ct201.json && proxy=1 || proxy=0
grep -q '"expires_at":"' /tmp/ct201.json && exp=1 || exp=0
grep -q '"session_id":' /tmp/ct201.json && sid=1 || sid=0
[[ $((proxy+exp+sid)) == 3 && $fields -ge 1 ]] && ok "T-0805.1 {session_id, proxy_url socks5h://, expires_at} segun spec" \
  || bad "campos 201: proxy=$proxy expires=$exp sid=$sid body=$(cat /tmp/ct201.json)"

# --- AC2: codigos tipados ------------------------------------------------
resp=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Content-Type: application/json" -d "$BODY" "$BASE/api/v1/sessions")
[[ "$resp" == "403" ]] && ok "T-0805.2 sin api key -> 403" || bad "sin key -> $resp"

# cuota agotada via Redis (fuente caliente del contador del periodo)
"$redis_cli" -p 16379 SET "tenant:550e8400-e29b-41d4-a716-446655440001:gb_used:$(date -u +%F)" 99 >/dev/null 2>&1
resp=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d "$BODY" "$BASE/api/v1/sessions")
[[ "$resp" == "402" ]] && ok "T-0805.2 cuota agotada -> 402" || bad "cuota -> $resp"
"$redis_cli" -p 16379 SET "tenant:550e8400-e29b-41d4-a716-446655440001:gb_used:$(date -u +%F)" 1 >/dev/null 2>&1

n429=0
for _ in $(seq 1 100); do
  c=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" -d "$BODY" "$BASE/api/v1/sessions")
  [[ "$c" == "429" ]] && n429=$((n429+1))
done
[[ $n429 -ge 1 ]] && ok "T-0805.2 100 requests/1s -> $n429 429 (>=1)" || bad "rate-limit: $n429 429 en 100 requests"

# --- AC3: el contrato es el spec ----------------------------------------
grep -q 'pattern: "\^socks5h://"' docs/api/openapi.yaml && grep -q '"428"' docs/api/openapi.yaml \
  && grep -q 'nonce:' docs/api/openapi.yaml && ok "T-0805.3 spec declarado (pattern socks5h://, gates 428, nonce)" \
  || bad "openapi.yaml sin el contrato de sessions/enroll completo"

echo "----"
echo "total=${PASS} pass, ${FAIL} fail"
[[ $FAIL -eq 0 ]]
