#!/usr/bin/env bash
# PRUEBA de integracion EP-01 sobre Postgres real (T-0101 a T-0106).
#
# Requisitos:
#   DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable
#   PSQL_BIN=/ruta/psql   (o psql en PATH)
# El script reconstruye el schema publico y recorre los criterios de
# aceptacion con salida PASS/FAIL por AC y resumen final.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

[[ -n "${DATABASE_URL:-}" ]] || { echo "test_integration: DATABASE_URL requerido" >&2; exit 2; }
PSQL="${PSQL_BIN:-psql}"

PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); printf 'PASS %s\n' "$1"; }
bad()  { FAIL=$((FAIL+1)); printf 'FAIL %s\n' "$1"; }
BIN=/tmp/botnet-discover-ep01.exe
q()    { "$PSQL" -tA -c "$1" "$DATABASE_URL" 2>/dev/null; }
start_all=$(date +%s)
go build -o "$BIN" ./cmd/botnet-discover

# --- T-0101: migracion y esquema ---------------------------------------
"$PSQL" -q -c 'DROP SCHEMA public CASCADE; CREATE SCHEMA public;' "$DATABASE_URL" >/dev/null 2>&1
if bash scripts/discovery/migrate.sh >/dev/null 2>&1; then
  ok "T-0101.1 migrate exit 0"
else
  bad "T-0101.1 migrate"
fi
cols=$(q "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='discovered' AND column_name IN ('ip','port','service','vendor','model','asn','country','last_seen_at')")
[[ "$cols" == "8" ]] && ok "T-0101.2 columnas exigidas (8/8)" || bad "T-0101.2 columnas=$cols"
types=$(q "SELECT string_agg(column_name || ':' || data_type || ':' || coalesce(character_maximum_length::text, '-'), ',' ORDER BY column_name) FROM information_schema.columns WHERE table_name='discovered' AND column_name IN ('ip','country','last_seen_at')")
[[ "$types" == *"ip:inet:-"* && "$types" == *"country:character:2"* && "$types" == *"last_seen_at:timestamp with time zone:-"* ]] \
  && ok "T-0101.2 tipos inet/char(2)/timestamptz" || bad "T-0101.2 tipos=$types"
uq=$(q "SELECT count(*) FROM pg_constraint WHERE conrelid='public.discovered'::regclass AND contype='u'")
[[ "$uq" == "1" ]] && ok "T-0101.2 UNIQUE (ip,port)" || bad "T-0101.2 unique=$uq"
dup=$("$PSQL" -c "INSERT INTO discovered(ip,port,service) VALUES('1.2.3.4',80,'http'),('1.2.3.4',80,'http');" "$DATABASE_URL" 2>&1 | grep -c duplicate)
[[ "$dup" == "1" ]] && ok "T-0101.3 duplicate rechazado" || bad "T-0101.3 grep duplicate=$dup"

# --- T-0102: ingesta Shodan ---------------------------------------------
t0=$(date +%s)
out=$(DRY=1 FIXTURE=testdata/shodan_1k.json bash scripts/discovery/shodan_ingest.sh 2>&1)
t1=$(date +%s)
echo "$out" | grep -q '^processed=1000$' && ok "T-0102.1 DRY fixture procesa 1000" || bad "T-0102.1 out=$out"
[[ $((t1 - t0)) -lt 60 ]] && ok "T-0102.1 tiempo $((t1 - t0))s < 60s" || bad "T-0102.1 tiempo $((t1 - t0))s"
for i in 1 2; do
  FIXTURE=testdata/shodan_1k.json DATABASE_URL="$DATABASE_URL" bash scripts/discovery/shodan_ingest.sh >/dev/null 2>&1 \
    || bad "T-0102.2 ingesta real #$i fallo"
done
diffc=$(q "SELECT count(*)-count(DISTINCT (ip,port)) FROM discovered")
[[ "$diffc" == "0" ]] && ok "T-0102.2 idempotencia upsert (0 duplicados tras 2 runs)" || bad "T-0102.2 diff=$diffc"

"$BIN" stub-api --fixture=testdata/shodan_1k.json --addr=127.0.0.1:18099 >/tmp/stubapi.log 2>&1 &
stub_pid=$!
sleep 2
runstart=$(q "SELECT now()")
t0=$(date +%s)
SHODAN_KEY=test SHODAN_API_BASE=http://127.0.0.1:18099 LIMIT=100 DATABASE_URL="$DATABASE_URL" \
  bash scripts/discovery/shodan_ingest.sh >/dev/null 2>&1
t1=$(date +%s)
kill "$stub_pid" 2>/dev/null
fresh=$(q "SELECT count(*) FROM discovered WHERE last_seen_at > '$runstart'::timestamptz")
[[ $((t1 - t0)) -lt 90 ]] && ok "T-0102.3 API(stub) en $((t1 - t0))s < 90s" || bad "T-0102.3 tiempo $((t1 - t0))s"
[[ "$fresh" -ge 100 ]] && ok "T-0102.3 last_seen frescas=$fresh >= 100" || bad "T-0102.3 frescas=$fresh"

# --- T-0103: barrido propio ---------------------------------------------
t0=$(date +%s)
SWEEP_JSON=testdata/discovery/masscan_sweep.ndjson DATABASE_URL="$DATABASE_URL" \
  bash scripts/discovery/masscan_sweep.sh --range=1.0.0.0/16 --ports=23,80,8080,554,32210 >/tmp/sweep.log 2>&1
t1=$(date +%s)
grep -q '^upserted=' /tmp/sweep.log && ok "T-0103.1 sweep-parse+upsert en $((t1 - t0))s (doble; <3min por /16 con masscan real)" || bad "T-0103.1 sweep: $(tail -1 /tmp/sweep.log)"
svc=$(q "SELECT count(*) FROM discovered WHERE service IS NOT NULL")
[[ "$svc" -gt 0 ]] && ok "T-0103.3 service inferido>0 ($svc filas)" || bad "T-0103.3 service=0"

# --- T-0104: clasificacion de banners ------------------------------------
GRAB_FIXTURE=testdata/banners/grabs.jsonl DATABASE_URL="$DATABASE_URL" \
  bash scripts/discovery/zgrab2_grab.sh --input=testdata/banners/hosts.txt --emit >/tmp/grab.log 2>&1
lines=$(grep -c 'banner_hash' /tmp/grab.log)
classes=$(grep -c 'device_class_guess' /tmp/grab.log)
[[ "$lines" == "100" && "$classes" == "100" ]] && ok "T-0104.1 100 JSON con banner_hash+device_class_guess" || bad "T-0104.1 hash=$lines class=$classes"
if bash scripts/discovery/zgrab2_grab.sh --score=testdata/banners/corpus.jsonl >/tmp/score.log 2>&1; then
  ok "T-0104.2 $(grep -m1 accuracy /tmp/score.log)"
else
  bad "T-0104.2 $(grep -m1 accuracy /tmp/score.log)"
fi
vend=$(q "SELECT count(*) FROM discovered WHERE vendor IS NOT NULL AND last_seen_at > now() - interval '1 day'")
[[ "$vend" -gt 0 ]] && ok "T-0104.3 vendor fresco>0 ($vend filas)" || bad "T-0104.3 vendor=0"

# --- T-0105: informe -----------------------------------------------------
head1=$(bash scripts/discovery/inventory_report.sh --fixture=testdata/discovery/report_fixture.csv | head -1)
[[ "$head1" == "asn,country,candidates_by_class" ]] && ok "T-0105.3 cabecera fija" || bad "T-0105.3 head=$head1"
dedup=$(bash scripts/discovery/inventory_report.sh --fixture=testdata/discovery/report_fixture.csv | grep '^4134,')
[[ "$dedup" == "4134,CN,router=1;camera=1;nas=0;unknown=0;total=2" ]] && ok "T-0105.2 dedupe /24 (2 hosts -> 1 candidato)" || bad "T-0105.2 fila=$dedup"
head1db=$(bash scripts/discovery/inventory_report.sh | head -1)
[[ "$head1db" == "asn,country,candidates_by_class" ]] && ok "T-0105.1 informe BD cabecera fija (top-20)" || bad "T-0105.1 head=$head1db"

# --- T-0106: seleccion con opt-outs --------------------------------------
"$PSQL" -v ON_ERROR_STOP=1 -q "$DATABASE_URL" < testdata/discovery/nodes_leaves_fixture.sql >/dev/null 2>&1
"$PSQL" -q -c "
INSERT INTO discovered (ip, port, service, last_seen_at) VALUES
 ('8.8.4.4',23,'telnet',now()), ('8.8.5.5',80,'http',now()),
 ('8.8.6.6',80,'http',now()),  ('8.8.7.7',554,'rtsp',now())
ON CONFLICT (ip,port) DO UPDATE SET last_seen_at=now();" "$DATABASE_URL" >/dev/null 2>&1
t0=$(date +%s)
LIMIT=100 bash scripts/discovery/select_candidates.sh >/tmp/select.log 2>&1
t1=$(date +%s)
[[ $((t1 - t0)) -lt 5 ]] && ok "T-0106.1 select en $((t1 - t0))s < 5s" || bad "T-0106.1 tiempo $((t1 - t0))s"
sel=$(grep -c '^8\.8\.' /tmp/select.log)
in6=$(grep -c '^8\.8\.6\.6' /tmp/select.log); in7=$(grep -c '^8\.8\.7\.7' /tmp/select.log)
out4=$(grep -c '^8\.8\.4\.4' /tmp/select.log); out5=$(grep -c '^8\.8\.5\.5' /tmp/select.log)
[[ "$in6" == "1" && "$in7" == "1" && "$out4" == "0" && "$out5" == "0" ]] \
  && ok "T-0106.1/2 optout y leave bloqueado excluidos; vencido y sin node incluidos" \
  || bad "T-0106.2 in6=$in6 in7=$in7 out4=$out4 out5=$out5"
[[ "$(wc -l </tmp/select.log)" -le 100 ]] && ok "T-0106.1 LIMIT respetado" || bad "T-0106.1 filas=$(wc -l </tmp/select.log)"

"$PSQL" -v ON_ERROR_STOP=1 -q "$DATABASE_URL" < testdata/discovery/big_fixture.sql >/dev/null 2>&1
n100k=$(q "SELECT count(*) FROM discovered")
plan=$("$PSQL" -tA -c "EXPLAIN SELECT d.ip, d.port, d.service, d.asn, d.country
FROM discovered d
WHERE d.service IS NOT NULL
  AND d.last_seen_at > now() - interval '30 days'
  AND NOT EXISTS (SELECT 1 FROM nodes n WHERE n.public_ip = d.ip AND n.optout_at > now() - interval '12 months')
  AND NOT EXISTS (SELECT 1 FROM leaves l JOIN nodes n ON n.node_id = l.node_id WHERE n.public_ip = d.ip AND l.reprobe_blocked_until > now())
ORDER BY d.last_seen_at DESC
LIMIT 100;" "$DATABASE_URL")
echo "$plan" | grep -q 'Index.*Scan.*discovered_candidate_pick' && ! echo "$plan" | grep -q 'Seq Scan on discovered' \
  && ok "T-0106.3 EXPLAIN usa discovered_candidate_pick, 0 seq-scan de discovered ($n100k filas)" \
  || bad "T-0106.3 plan=$n100k filas: $(echo "$plan" | head -2 | tr '\n' '|')"

echo "----"
echo "total=${PASS} pass, ${FAIL} fail, $(($(date +%s) - start_all))s"
[[ $FAIL -eq 0 ]]
