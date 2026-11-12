#!/usr/bin/env bash
# Puerta de salida del goal BOT.NET: valida que el blueprint y el backlog son ejecutables.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BP="$ROOT/docs/blueprint"
FAIL=0
ok()   { printf 'ok   %s\n' "$1"; }
bad()  { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }
need() { if [ -e "$1" ]; then ok "existe ${1#$ROOT/}"; else bad "falta ${1#$ROOT/}"; fi; }

need "$BP/BLUEPRINT.md"
need "$BP/BACKLOG.md"
need "$BP/EPICS"
need "$BP/TASKS"

EPIC_COUNT=$(ls "$BP/EPICS"/*.md 2>/dev/null | wc -l | tr -d ' ')
TASK_FILES=$(ls "$BP/TASKS"/*.md 2>/dev/null | wc -l | tr -d ' ')
TASK_COUNT=$(grep -h '^### T-' "$BP/TASKS"/*.md 2>/dev/null | wc -l | tr -d ' ')

[ "$EPIC_COUNT" -eq 14 ] && ok "14 epics" || bad "epics=$EPIC_COUNT (esperado 14)"
[ "$TASK_FILES" -ge 14 ] && ok "ficheros de tasks=$TASK_FILES" || bad "ficheros de tasks=$TASK_FILES (esperado >=14)"
if [ "$TASK_COUNT" -ge 80 ] && [ "$TASK_COUNT" -le 110 ]; then ok "total de tasks=$TASK_COUNT"; else bad "total de tasks=$TASK_COUNT (esperado 80-110)"; fi

DUP=$(grep -h '^### T-' "$BP/TASKS"/*.md 2>/dev/null | grep -oE 'T-[0-9]{4}' | sort | uniq -d | tr '\n' ' ')
[ -z "$DUP" ] && ok "IDs de task unicos" || bad "IDs duplicados: $DUP"

DUP_EPIC=$(grep -h '^id: EP-' "$BP/EPICS"/*.md 2>/dev/null | sed 's/id: //' | sort | uniq -d | tr '\n' ' ')
[ -z "$DUP_EPIC" ] && ok "IDs de epic unicos" || bad "epics duplicados: $DUP_EPIC"

missing_ref=""
for f in "$BP"/EPICS/EP-*.md; do
  id=$(grep -m1 '^id: EP-' "$f" | sed 's/id: //' | tr -d '\r')
  grep -q "$id" "$BP/BLUEPRINT.md" || missing_ref="$missing_ref $id"
done
[ -z "$missing_ref" ] && ok "BLUEPRINT referencia los 14 epics" || bad "BLUEPRINT no referencia:$missing_ref"

FIELDS_BAD=""
AC_BAD=""
for f in "$BP"/TASKS/*.md; do
  awk -v FILE="${f#$ROOT/}" '
    function close_block() {
      if (id == "") return
      if (ac < 3) { printf "%s: %s con %d criterios (min 3)\n", FILE, id, ac; badac = 1 }
      if (!dep) printf "%s: %s sin Depende de\n", FILE, id
      if (!est) printf "%s: %s sin Estimacion\n", FILE, id
      if (!dod) printf "%s: %s sin DoD\n", FILE, id
      if (!fic) printf "%s: %s sin Ficheros\n", FILE, id
      id = ""; ac = 0; dep = est = dod = fic = 0; inac = 0
    }
    /^### T-[0-9][0-9][0-9][0-9]/ { close_block(); id = $2; next }
    /^- \*\*Criterios de aceptación:\*\*/ { inac = 1; next }
    /^- \*\*Depende de:\*\*/              { dep = 1; inac = 0; next }
    /^- \*\*Estimación:\*\*/              { est = 1; inac = 0; next }
    /^- \*\*DoD:\*\*/                     { dod = 1; inac = 0; next }
    /^- \*\*Ficheros:\*\*/                { fic = 1; inac = 0; next }
    /^- \*\*/                          { inac = 0; next }
    inac && /^[[:space:]]+[0-9]+\./    { ac++ }
    END { close_block() }
  ' "$f"
done > /tmp/bp_tasks.txt 2>&1

if [ -s /tmp/bp_tasks.txt ]; then bad "tasks incompletas:"; sed 's/^/       /' /tmp/bp_tasks.txt; else ok "todas las tasks con >=3 criterios y campos obligatorios"; fi

AC_COUNT=$(grep -hcE '^\s+[0-9]+\. ' "$BP"/TASKS/*.md 2>/dev/null | awk '{s+=$1} END {print s+0}')
[ "$AC_COUNT" -ge $((TASK_COUNT*3)) ] && ok "criterios de aceptacion totales=$AC_COUNT" || bad "criterios=$AC_COUNT (< 3 por task)"

BANNED=$(grep -hoiE 'funciona correctamente|esta optimizado|es robusto' "$BP"/TASKS/*.md 2>/dev/null | wc -l | tr -d ' ')
ok "menciones a criterios vagos=$BANNED"

for hid in A2-01 A3-01 A3-06; do
  grep -rq "$hid" "$BP/TASKS" && ok "S1 bloqueante $hid tiene task" || bad "S1 bloqueante $hid sin task"
done

grep -q 'Trazabilidad' "$BP/BACKLOG.md" && ok "BACKLOG con trazabilidad" || bad "BACKLOG sin trazabilidad"
grep -c 'injerto:' "$BP/BLUEPRINT.md" | awk '{exit ($1>=12)?0:1}' && ok "blueprint con injertos marcados" || bad "blueprint sin injertos suficientes"

echo "---"
if [ "$FAIL" -eq 0 ]; then echo "RESULTADO: PASS"; else echo "RESULTADO: FAIL ($FAIL comprobaciones)"; fi
exit "$FAIL"
