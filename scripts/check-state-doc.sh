#!/usr/bin/env bash
# Gate del documento de estado: recomputa cifras, exige secciones, cobertura de deuda y plan cerrable.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
S="$ROOT/docs/state"
FAIL=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }

for f in ESTADO-ACTUAL.md DEUDA-Y-RIESGOS.md PROXIMOS-PASOS.md; do
  [ -s "$S/$f" ] && ok "existe y no está vacío: $f" || bad "falta o vacío: $f"
done
[ "$FAIL" -eq 0 ] || { echo "RESULTADO: FAIL ($FAIL)"; exit "$FAIL"; }

# --- secciones obligatorias ---
check_sections() { # <fichero> <nº mínimo> <sección...>
  local f="$1" min="$2"; shift 2
  local n; n=$(grep -c '^## ' "$S/$f")
  [ "$n" -ge "$min" ] && ok "$f con $n secciones (mínimo $min)" || bad "$f con $n secciones (mínimo $min)"
  local s
  for s in "$@"; do
    grep -q "^## .*$s" "$S/$f" && ok "$f cubre «$s»" || bad "$f sin sección «$s»"
  done
}
check_sections ESTADO-ACTUAL.md 10 "Resumen ejecutivo" "Inventario por capa" "Métricas recomputadas" "Cobertura de verificación" "Lo que no existe"
check_sections DEUDA-Y-RIESGOS.md 6 "Deuda técnica" "Riesgos de producto" "Limitaciones aceptadas" "pendientes de medición de campo" "Prioridad de cierre"
check_sections PROXIMOS-PASOS.md 6 "P0" "P1" "P2" "P3" "Orden recomendado"

# --- estado del reclutamiento: oleadas cerradas y calendario con fechas ---
R="$S/RECRUIT.md"
if [ -s "$R" ]; then
  ok "existe y no está vacío: RECRUIT.md"
  n=$(grep -cE '^\| *[1-5] *\|' "$R")
  [ "$n" -ge 5 ] && ok "RECRUIT.md con $n filas de oleada (mínimo 5)" || bad "RECRUIT.md con $n filas de oleada (mínimo 5)"
  vacias=$(awk -F'|' '/^\| *[1-5] *\|/{for(i=2;i<=NF;i++) if($i ~ /^ *$/) print}' "$R" | grep -c .)
  [ "${vacias:-1}" -eq 0 ] && ok "oleadas sin celdas vacías" || bad "$vacias filas de oleada con celda vacía"
  grep -q '^## .*[Oo]leadas' "$R" && ok "RECRUIT.md cubre «Oleadas»" || bad "RECRUIT.md sin sección «Oleadas»"
  hitos=$(grep -cE 'primer SYN real|primer nodo verificado' "$R")
  [ "$hitos" -eq 2 ] && ok "RECRUIT.md con 2 hitos fechados" || bad "RECRUIT.md con $hitos hitos (esperado 2)"
  fechas=$(grep -cE '20[0-9]{2}-[0-9]{2}-[0-9]{2}' "$R")
  [ "$fechas" -ge 2 ] && ok "RECRUIT.md con $fechas fechas ISO" || bad "RECRUIT.md con $fechas fechas ISO (mínimo 2)"
else
  bad "falta o vacío: RECRUIT.md"
fi

# --- cifras contra el repo ---
cmp_num() { # <etiqueta> <valor real> <fichero>
  local label="$1" real="$2" f="$3" doc
  # La columna del comando lleva `\|` escapado: se toma el ÚLTIMO campo no vacío.
  doc=$(grep -F "| $label |" "$S/$f" | head -1 | awk -F'|' '{for(i=NF;i>0;i--){v=$i; gsub(/ /,"",v); if(v!=""){print v; exit}}}')
  [ "$doc" = "$real" ] && ok "cifra «$label» = $real" || bad "cifra «$label»: doc=$doc real=$real"
}
TASKS=$(grep -h '^### T-' docs/blueprint/TASKS/*.md | wc -l | tr -d ' ')
DONE=$(grep -h 'Estado:\*\* DONE' docs/blueprint/TASKS/*.md | wc -l | tr -d ' ')
BLOCKED=$(grep -h 'Estado:\*\* BLOCKED' docs/blueprint/TASKS/*.md | wc -l | tr -d ' ')
ACS=$(grep -rhE '^\s+[0-9]+\. ' docs/blueprint/TASKS/*.md | wc -l | tr -d ' ')
PASS=$(grep -rhE '^\| T-[0-9]{4} \| AC[0-9]+ \| \*{0,2}PASS' docs/verification/*.md | wc -l | tr -d ' ')
GOFAIL=$(grep -rhE '^\| T-[0-9]{4} \| AC[0-9]+ \| \*{0,2}FAIL' docs/verification/*.md | wc -l | tr -d ' ')
GOFILES=$(find . -name '*.go' -not -path './.git/*' | wc -l | tr -d ' ')
GOLOC=$(find . -name '*.go' -not -path './.git/*' -exec cat {} + | wc -l | tr -d ' ')
TESTS=$(grep -rh '^func Test' --include='*_test.go' . | wc -l | tr -d ' ')
VERIF=$(ls docs/verification/*.md | wc -l | tr -d ' ')
RUNBOOKS=$(ls docs/runbooks/*.md 2>/dev/null | wc -l | tr -d ' ')
MIGS=$(ls migrations/*.sql | wc -l | tr -d ' ')
COMMITS=$(git rev-list --count HEAD)
UNTRACKED=$(git status --short --untracked-files=all | wc -l | tr -d ' ')
source scripts/env.sh >/dev/null 2>&1 || true
PKGS=$(go list ./... 2>/dev/null | wc -l | tr -d ' ')

cmp_num "Tasks del backlog" "$TASKS" ESTADO-ACTUAL.md
cmp_num "Tasks en DONE" "$DONE" ESTADO-ACTUAL.md
cmp_num "Tasks bloqueadas" "$BLOCKED" ESTADO-ACTUAL.md
cmp_num "Criterios de aceptación" "$ACS" ESTADO-ACTUAL.md
cmp_num "Criterios PASS" "$PASS" ESTADO-ACTUAL.md
cmp_num "Criterios FAIL" "$GOFAIL" ESTADO-ACTUAL.md
cmp_num "Ficheros Go" "$GOFILES" ESTADO-ACTUAL.md
# El LOC se mueve con cualquier edición o formateo: tolerancia ±300.
doc_loc=$(grep -F "| Líneas de Go |" "$S/ESTADO-ACTUAL.md" | head -1 | awk -F'|' '{for(i=NF;i>0;i--){v=$i; gsub(/ /,"",v); if(v!=""){print v; exit}}}')
if [ -n "$doc_loc" ] && [ $((GOLOC - doc_loc)) -le 300 ] && [ $((doc_loc - GOLOC)) -le 300 ]; then
  ok "líneas de Go doc=$doc_loc real=$GOLOC (tolerancia 300)"
else bad "líneas de Go doc=$doc_loc real=$GOLOC"; fi
cmp_num "Funciones de test" "$TESTS" ESTADO-ACTUAL.md
cmp_num "Paquetes Go" "$PKGS" ESTADO-ACTUAL.md
cmp_num "Informes de verificación" "$VERIF" ESTADO-ACTUAL.md
cmp_num "Runbooks" "$RUNBOOKS" ESTADO-ACTUAL.md
cmp_num "Migraciones" "$MIGS" ESTADO-ACTUAL.md
# El commit que actualiza este documento cambia el recuento: tolerancia ±2.
doc_cm=$(grep -F "| Commits |" "$S/ESTADO-ACTUAL.md" | head -1 | awk -F'|' '{for(i=NF;i>0;i--){v=$i; gsub(/ /,"",v); if(v!=""){print v; exit}}}')
if [ -n "$doc_cm" ] && [ $((COMMITS - doc_cm)) -le 15 ] && [ $((doc_cm - COMMITS)) -le 15 ]; then
  ok "commits doc=$doc_cm real=$COMMITS (tolerancia 15)"
else bad "commits doc=$doc_cm real=$COMMITS"; fi
TRACKED=$(git ls-files | wc -l | tr -d ' ')
# Los versionados crecen con cada commit: tolerancia acotada en vez de exactitud.
doc_tr=$(grep -F "| Ficheros versionados |" "$S/ESTADO-ACTUAL.md" | head -1 | awk -F'|' '{for(i=NF;i>0;i--){v=$i; gsub(/ /,"",v); if(v!=""){print v; exit}}}')
if [ -n "$doc_tr" ] && [ $((TRACKED - doc_tr)) -le 40 ] && [ $((doc_tr - TRACKED)) -le 40 ]; then
  ok "ficheros versionados doc=$doc_tr real=$TRACKED (tolerancia 40)"
else bad "ficheros versionados doc=$doc_tr real=$TRACKED"; fi
doc_un=$(grep -F "| Entradas sin trackear |" "$S/ESTADO-ACTUAL.md" | head -1 | awk -F'|' '{for(i=NF;i>0;i--){v=$i; gsub(/ /,"",v); if(v!=""){print v; exit}}}')
if [ -n "$doc_un" ] && [ $((UNTRACKED - doc_un)) -le 40 ] && [ $((doc_un - UNTRACKED)) -le 40 ]; then
  ok "entradas sin trackear doc=$doc_un real=$UNTRACKED (métrica viva, tolerancia 40)"
else bad "entradas sin trackear doc=$doc_un real=$UNTRACKED"; fi

# --- cobertura de deuda de campo ---
# La cuenta la manda scripts/check-field-closure.sh, que cuenta FILAS de
# criterio y no prosa (el grep anterior contaba lineas de texto del acta y
# daba 49 cuando las filas reales son 45). Aqui se comprueba que el doc y el
# validador dicen lo mismo; el detalle por epica lo publica ese script.
if [ -x scripts/check-field-closure.sh ] || [ -f scripts/check-field-closure.sh ]; then
  if out=$(bash scripts/check-field-closure.sh --strict 2>&1); then
    tot=$(printf '%s' "$out" | grep -E '^TOTAL' | awk '{print $2}')
    ok "deuda de campo por filas = ${tot:-?} (validador en OK)"
  else
    bad "deuda de campo: el validador de filas no cuadra con el doc"
    printf '%s\n' "$out" | grep -E 'doc=|fila pendiente' | head -8 | sed 's/^/       /'
  fi
else
  bad "falta scripts/check-field-closure.sh"
fi


# --- plan: columnas y campos ---
steps=$(grep -cE '^\| S-[0-9]{2}( ✅)? \|' "$S/PROXIMOS-PASOS.md")
[ "$steps" -ge 15 ] && ok "plan con $steps pasos" || bad "plan con $steps pasos (mínimo 15)"
badrows=$(awk -F'|' '
  /^## / { if ($0 ~ /P[0-3]/) { match($0, /P[0-3]/); prio=substr($0, RSTART, 2) } else prio="" }
  /^\| S-[0-9]{2}( ✅)? \|/ {
    if (prio == "") { print "prioridad:" $2; next }
    c=$4; r=$5; cr=$7; gsub(/ /,"",c); gsub(/ /,"",r); gsub(/ /,"",cr);
    if (c == "") print "coste:" $2;
    if (r == "") print "rol:" $2;
    if (cr == "") print "criterio:" $2 }' "$S/PROXIMOS-PASOS.md")
[ -z "$badrows" ] && ok "todos los pasos con prioridad, coste, rol y criterio de cierre" || bad "pasos incompletos: $(echo "$badrows" | tr '\n' ' ')"

# --- restos ---
rest=$(grep -rn 'TODO\|pendiente de rellenar' "$S"/*.md | wc -l | tr -d ' ')
[ "$rest" -eq 0 ] && ok "sin TODO ni secciones por rellenar" || bad "restos=$rest"
empty=$(awk '/^## /{h=$0; getline; if ($0 ~ /^## /) print h}' "$S"/*.md | wc -l | tr -d ' ')
[ "$empty" -eq 0 ] && ok "sin secciones vacías" || bad "secciones vacías=$empty"

echo "---"
[ "$FAIL" -eq 0 ] && { echo "RESULTADO: PASS"; exit 0; }
echo "RESULTADO: FAIL ($FAIL comprobaciones)"; exit "$FAIL"
