#!/usr/bin/env bash
# EP-08 T-0801: equivalente a `make migrate` (aqui no hay make).
#   DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable \
#     bash scripts/backend/migrate.sh
# Aplica migrations/*.up.sql solo adelante via el runner embebido.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null
[[ -n "${DATABASE_URL:-}" ]] || { echo "migrate.sh: DATABASE_URL requerido" >&2; exit 2; }
go run ./cmd/botnet-api migrate
