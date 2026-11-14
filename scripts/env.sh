#!/usr/bin/env bash
# Entorno comun de ejecucion. Todo subagente debe hacer `source scripts/env.sh` primero.
export PATH="$HOME/sdk/go/bin:$PATH"
# Herramientas portables de test del host (Postgres, y lo que se instale en ~/go/bin).
[ -d "$HOME/pgsql/bin" ] && export PATH="$HOME/pgsql/bin:$PATH"
[ -d "$HOME/go/bin" ] && export PATH="$HOME/go/bin:$PATH"
export CGO_ENABLED=0
# -p=2: tope de acciones de build en paralelo. Con 24 CPUs Go lanzaba
# hasta 24 compile.exe a la vez y el host (editor + navegador + WSL ya
# comen la memoria) se queda sin margen: el compilador muere con
# 0xc0000142 (STATUS_DLL_INIT_FAILED, docs/runbooks/dev-host.md).
# El gate ademas fuerza -p 1 en build/vet/test.
export GOFLAGS="-mod=mod -p=2"
export GOTOOLCHAIN=local
export GOPATH="${GOPATH:-$HOME/go}"
export BOTNET_ENV=test
go version >/dev/null 2>&1 || { echo "Go no disponible en PATH" >&2; exit 1; }
