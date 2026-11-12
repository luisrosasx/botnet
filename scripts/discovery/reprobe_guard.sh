#!/usr/bin/env bash
# EP-10 T-1007 — equivalente a `make reprobe-guard` (aqui no hay make):
# 0 filas de nodes con optout_at no nulo y enrolled_at > optout_at en
# < 12 meses — nadie re-cluto sin consentimiento al dueno que se bajo.
#
#   DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable \
#     bash scripts/discovery/reprobe_guard.sh
#
# Trae base scratch con las migraciones aplicadas (la guardia es SQL
# puro sobre el DDL real), audita 0 violaciones yuxtapuestas y, para
# que el guard no sea decorativo, INYECTA una violacion y exige que la
# detecte (count >= 1) antes de dar el OK.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null
[[ -n "${DATABASE_URL:-}" ]] || { echo "reprobe_guard: DATABASE_URL requerido" >&2; exit 2; }

PSQL="${PSQL_BIN:-psql}"
command -v "$PSQL" >/dev/null 2>&1 || PSQL="$HOME/pgsql/bin/psql.exe"
[[ -x "$PSQL" || "$PSQL" == "psql" ]] || { echo "reprobe_guard: psql no disponible" >&2; exit 2; }

SB="botnet_reprobe_guard"
# DSN de la base scratch: troca solo el nombre de la base del DSN.
SCRATCH_URL="$(printf '%s' "$DATABASE_URL" | sed -E 's#(://[^/]*/)[^?]*#\1'"$SB"'#')"

admin() { "$PSQL" -tA "$@" "$DATABASE_URL"; }
sb() { "$PSQL" -tA "$@" "$SCRATCH_URL"; }

admin -c "DROP DATABASE IF EXISTS $SB WITH (FORCE)" >/dev/null 2>&1
admin -c "CREATE DATABASE $SB" >/dev/null || { echo "reprobe_guard: no se pudo crear la scratch" >&2; exit 2; }
trap 'admin -c "DROP DATABASE IF EXISTS $SB WITH (FORCE)" >/dev/null 2>&1' EXIT

DATABASE_URL="$SCRATCH_URL" go run ./cmd/botnet-api migrate >/dev/null || {
  echo "reprobe_guard: migraciones fallaron en la scratch" >&2; exit 2; }

GUARD="SELECT count(*) FROM nodes n
 WHERE n.optout_at IS NOT NULL
   AND n.enrolled_at > n.optout_at
   AND n.enrolled_at < n.optout_at + interval '12 months'"

base="$(sb -c "$GUARD" | tr -d '[:space:]')"
[[ "$base" == "0" ]] || { echo "reprobe_guard: base contaminada ($base)"; exit 1; }

# Inyeccion: dueno que se bajo hace 30 dias y fue re-clutado a los 1 —
# el guard tiene que verlo.
sb -q -c "
INSERT INTO nodes (node_id, slug, device_token_hash, arch, device_class,
                   consent_at, optout_at, enrolled_at, status)
VALUES (gen_random_uuid(), 'reprobe-inj', md5(random()::text), 'amd64', 'C',
        now() - interval '40 days', now() - interval '30 days',
        now() - interval '29 days', 'retired')" || {
  echo "reprobe_guard: inyeccion fallo" >&2; exit 1; }

inj="$(sb -c "$GUARD" | tr -d '[:space:]')"
if [[ "$inj" == "1" ]]; then
  echo "reprobe-guard: 0 violaciones en base limpia; 1 detectada con re-clutaje a 1 mes del opt-out"
  exit 0
fi
echo "reprobe_guard: la inyeccion no fue detectada (count=$inj, want 1)" >&2
exit 1
