#!/usr/bin/env bash
# T-0101 — aplica migrations/0002_discovered.up.sql sobre Postgres.
# Equivalente a `make migrate` (el target del Makefile delega aqui).
#   DATABASE_URL=postgres://user:pass@host:port/db scripts/discovery/migrate.sh
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
source "$ROOT/scripts/env.sh" >/dev/null

[[ -n "${DATABASE_URL:-}" ]] || { echo "migrate: DATABASE_URL requerido" >&2; exit 2; }
go run ./cmd/botnet-discover migrate --db="$DATABASE_URL"
