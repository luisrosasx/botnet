#!/usr/bin/env bash
# Entorno comun de ejecucion. Todo subagente debe hacer `source scripts/env.sh` primero.
export PATH="$HOME/sdk/go/bin:$PATH"
# Herramientas portables de test del host (Postgres, y lo que se instale en ~/go/bin).
[ -d "$HOME/pgsql/bin" ] && export PATH="$HOME/pgsql/bin:$PATH"
[ -d "$HOME/go/bin" ] && export PATH="$HOME/go/bin:$PATH"
export CGO_ENABLED=0
export GOFLAGS="-mod=mod"
export GOTOOLCHAIN=local
export GOPATH="${GOPATH:-$HOME/go}"
export BOTNET_ENV=test
go version >/dev/null 2>&1 || { echo "Go no disponible en PATH" >&2; exit 1; }
