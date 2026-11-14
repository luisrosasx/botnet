#!/usr/bin/env bash
# Puerta de salida del goal de implementación: exige tarea DONE + verificación 1:1 por criterio + suites verdes.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/scripts/env.sh" || exit 1
cd "$ROOT"

# Motores de test (portables, fuera del repo). Sobrescribibles por entorno.
export DATABASE_URL="${DATABASE_URL:-postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable}"
export REDIS_URL="${REDIS_URL:-redis://127.0.0.1:16379/9}"
export NATS_URL="${NATS_URL:-nats://127.0.0.1:14233}"

FAIL=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }

# Preflight de recursos: una sesión cargada dispara 0xc0000142 (el
# compilador no consigue inicializar DLLs y el fallo parece del código).
# Se detecta antes de dejar un rojo engañoso.
# El fallo real es el heap de escritorio de la sesion: cuenta procesos y
# consolas, no solo RAM. Con la sesion cargada compile.exe muere con
# 0xc0000142 aunque haya memoria (docs/runbooks/dev-host.md).
procs=$(tasklist //FO CSV 2>/dev/null | wc -l | tr -d ' ')
consolas=$(tasklist //FO CSV 2>/dev/null | grep -icE '"(OpenConsole|powershell|pwsh)\.exe')
if [ "${procs:-0}" -gt 620 ]; then
  bad "preflight: $procs procesos / $consolas consolas. Corre: bash scripts/dev/reap.sh x --orphans"
else
  ok "preflight: $procs procesos y $consolas consolas"
fi

TASKS=$(ls docs/blueprint/TASKS/*.md 2>/dev/null)
[ -n "$TASKS" ] || { echo "FAIL sin ficheros de tasks"; exit 1; }

TOTAL=$(grep -h '^### T-' $TASKS | wc -l | tr -d ' ')
DONE=$(grep -h '^- \*\*Estado:\*\* DONE' $TASKS | wc -l | tr -d ' ')
BLOCKED=$(grep -h '^- \*\*Estado:\*\* BLOCKED-HW' $TASKS | wc -l | tr -d ' ')
[ "$DONE" -eq "$TOTAL" ] && ok "tasks DONE=$DONE/$TOTAL" || ok "tasks DONE=$DONE/$TOTAL (bloqueadas por hardware=$BLOCKED)"
[ $((DONE + BLOCKED)) -eq "$TOTAL" ] || bad "tasks sin cerrar=$((TOTAL - DONE - BLOCKED))"
[ "$BLOCKED" -le 5 ] || bad "demasiadas tasks bloqueadas ($BLOCKED > 5)"
NOMOTIVO=$(grep -h '^- \*\*Estado:\*\* BLOCKED-HW' $TASKS | grep -vc 'MOTIVO:')
[ "$NOMOTIVO" -eq 0 ] && ok "toda task bloqueada declara MOTIVO" || bad "bloqueadas sin motivo=$NOMOTIVO"

VDIR="$ROOT/docs/verification"
if [ -d "$VDIR" ]; then
  missing=""
  badrows=$(grep -rhE '^\| T-[0-9]{4} \| AC[0-9]+ \| \*{0,2}FAIL' "$VDIR"/*.md 2>/dev/null | wc -l | tr -d ' ')
  for f in $TASKS; do
    epic=$(grep -m1 '^epic: ' "$f" | sed 's/epic: //' | tr -d '\r')
    [ -n "$epic" ] || continue
    while read -r t; do
      n_ac=$(awk -v id="$t" 'BEGIN{c=0;inb=0} /^### T-/{inb=($2==id)} inb&&/^- \*\*Criterios de aceptación:\*\*/{f=1;next} inb&&/^- \*\*/{f=0} f&&/^[[:space:]]+[0-9]+\./{c++} END{print c}' "$f")
      n_pass=$(grep -h "^| $t \|" "$VDIR/$epic.md" 2>/dev/null | grep -c 'PASS')
      [ "$n_pass" -ge "$n_ac" ] || missing="$missing $t($n_pass/$n_ac)"
    done < <(grep -oE '^### (T-[0-9]{4})' "$f" | awk '{print $2}')
  done
  [ -z "$missing" ] && ok "verificación 1:1 PASS por AC en las 14 epics" || bad "verificación incompleta:$missing"
  [ "$badrows" -eq 0 ] && ok "sin filas FAIL" || bad "filas FAIL=$badrows"
else
  bad "falta docs/verification"
fi

if go build -p 1 ./... 2>/tmp/ci_build.log; then ok "go build"; else bad "go build"; sed 's/^/       /' /tmp/ci_build.log | head -20; fi
if go vet -p 1 ./... 2>/tmp/ci_vet.log; then ok "go vet"; else bad "go vet"; sed 's/^/       /' /tmp/ci_vet.log | head -20; fi
# La suite completa en un solo proceso acumula memoria hasta que Windows
# deja de inicializar compile.exe (0xc0000142). Se corre en tandas de 6
# paquetes: cada go test termina, libera y el pico queda acotado.
chunk_fail=0
# `while read` sobre proceso sustituido (no tuberia) para que `bad` y
# chunk_fail se queden en el shell actual. El `for x in $(...)` partia por
# palabras y dejaba `go test` sin paquetes (compilaba "." y fallaba).
while read -r grupo; do
  set -- $grupo
  shift # fuera la clave numerica del grupo: solo paquetes
  if ! go test -p 1 -count=1 "$@" >/tmp/ci_test_chunk.log 2>&1; then
    chunk_fail=1
    bad "go test falla en: $*"
    tail -8 /tmp/ci_test_chunk.log | sed 's/^/       /'
  fi
done < <(go list -p 1 ./... | awk 'BEGIN{i=0} {b=int(i/6); print b"|"$0; i++}' \
  | awk -F'|' '{c[$1]=c[$1]" "$2} END{for(k in c) print k c[k]}' | sort -t' ' -k1n)
[ "$chunk_fail" -eq 0 ] && ok "go test (tandas de 6 paquetes)"

if [ -d web ] && [ -f web/package.json ]; then
  if (cd web && pnpm -s build >/tmp/ci_web.log 2>&1); then ok "web build"; else bad "web build"; tail -15 /tmp/ci_web.log | sed 's/^/       /'; fi
else
  bad "falta web/ con package.json"
fi

echo "---"
[ "$FAIL" -eq 0 ] && { echo "RESULTADO: PASS"; exit 0; }
echo "RESULTADO: FAIL ($FAIL comprobaciones)"; exit 1
