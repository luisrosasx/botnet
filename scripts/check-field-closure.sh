#!/usr/bin/env bash
# Validador de cierre de criterios de campo (solo lectura; no modifica nada).
#
# Cuenta por FILAS de las tablas de docs/verification/EP-XX.md los criterios que siguen
# pendientes de medir en campo, en dos versiones: estricto (solo el marcador BLOCKED-HW)
# y extendido (la lista completa de marcadores). Cruza el reparto con la tabla §4 de
# docs/state/DEUDA-Y-RIESGOS.md y detecta filas pendientes ya citadas por un fichero de
# docs/verification/evidence/ (evidencia recogida pero veredicto sin actualizar).
#
# Uso: bash scripts/check-field-closure.sh [--json] [--list] [--strict] [--help]
# Salida: 0 sin incoherencias, 1 con incoherencias, 2 error de uso o de entradas.
#
# Severidad: la unica incoherencia dura (FAIL) es «fila pendiente con evidencia ya
# recogida». Las divergencias con la tabla §4 del doc de deuda son AVISOS: ese doc lleva
# cifras recalculadas a mano y aqui no se reescribe. --strict promueve los avisos a fallo.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERIF="$ROOT/docs/verification"
EVIDENCE_DIR="$VERIF/evidence"
DEBT_DOC="$ROOT/docs/state/DEUDA-Y-RIESGOS.md"
DEBT_REL="$(basename "$DEBT_DOC")"

# --- Marcadores de «pendiente de campo» (ampliar aqui, separados por |) ---------------
# Estricto = marcador historico del doc de deuda. Extendido = estricto + anadidos.
# Se comparan en minusculas contra el texto de la fila (veredicto + evidencia).
MARK_STRICT='blocked-hw'
MARK_EXTRA='queda al soak|sin tshark'
MARK_EXT="$MARK_STRICT|$MARK_EXTRA"

usage() {
  cat <<'EOF'
Uso: bash scripts/check-field-closure.sh [--json] [--list] [--strict] [--help]

Cuenta por filas los criterios pendientes de campo en docs/verification/EP-XX.md
(estricto = solo BLOCKED-HW; extendido = lista completa de marcadores), cruza el
reparto por epica con la tabla §4 de docs/state/DEUDA-Y-RIESGOS.md y busca citas de
esas mismas filas en los ficheros de docs/verification/evidence/.

Opciones:
  --json     salida JSON por stdout (sin informe legible)
  --list     lista las filas pendientes (extendido): EP-01 T-0101 AC1
  --strict   trata los avisos de deuda del §4 como fallo (afecta al codigo de salida)
  -h, --help esta ayuda

Salida: 0 sin incoherencias, 1 con incoherencias, 2 error de uso o de entradas.
EOF
}

MODE='report'
STRICT_DEBT=0
while [ $# -gt 0 ]; do
  case "$1" in
    --json)    MODE='json' ;;
    --list)    MODE='list' ;;
    --strict)  STRICT_DEBT=1 ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'ERROR: opcion desconocida: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

FAIL=0     # incoherencias duras (siempre afectan al codigo de salida)
AVISOS=0   # divergencias con el doc de deuda (solo con --strict afectan)

# --- Entradas ------------------------------------------------------------------------
shopt -s nullglob
EP_FILES=("$VERIF"/EP-*.md)
if [ ${#EP_FILES[@]} -eq 0 ]; then
  printf 'ERROR: no hay ficheros %s/EP-*.md\n' "${VERIF#"$ROOT"/}" >&2
  exit 2
fi
if [ ! -f "$DEBT_DOC" ]; then
  printf 'ERROR: no existe %s\n' "${DEBT_DOC#"$ROOT"/}" >&2
  exit 2
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
ROWS="$TMP/rows.tsv"           # epica, task, ac, estricto(0|1), extendido(0|1)
COUNTS="$TMP/counts.tsv"       # epica, estricto, extendido — en el orden de los ficheros
DOCROWS="$TMP/doc.tsv"         # epica|TOTAL, cifra, numero de linea en el doc
CITED="$TMP/cited.tsv"         # task, ac, fichero de evidencia
DEBTDIV="$TMP/debtdiv.tsv"     # epica, doc, script, linea del doc (solo divergencias)
EVIDFAIL="$TMP/evidfail.tsv"   # epica, task, ac, fichero
DEBTMSG="$TMP/debtmsg.txt"     # lineas ya rotuladas (ok/AVISO) del cruce con el doc
EVIDMSG="$TMP/evidmsg.txt"     # lineas ya rotuladas (ok/FAIL) del cruce con evidence/
: >"$ROWS"; : >"$DOCROWS"; : >"$CITED"; : >"$DEBTDIV"; : >"$EVIDFAIL"
: >"$DEBTMSG"; : >"$EVIDMSG"

# --- Filas de las tablas de verificacion ---------------------------------------------
# Fila canonica: | T-0101 | AC1 | PASS | ...evidencia... |
# Solo cuentan las filas de tabla (celula 1 = T-XXXX, celula 2 = ACn), nunca la prosa.
# Los marcadores se buscan a partir de la celula de veredicto: varias filas anotan
# «BLOCKED-HW de campo» dentro del veredicto y otras llevan | dentro de la evidencia.
# Si una pareja task+AC se repite en el mismo fichero, gana la ULTIMA aparicion.
extract_rows() {
  local f ep
  for f in "${EP_FILES[@]}"; do
    ep="$(basename "$f" .md)"
    grep -E '^[[:space:]]*\|[[:space:]]*T-[0-9]{4}[[:space:]]*\|[[:space:]]*AC[0-9]+[[:space:]]*\|' "$f" \
      | awk -v ep="$ep" -v strict="$MARK_STRICT" -v ext="$MARK_EXT" '
          {
            line = $0
            sub(/\r$/, "", line)
            split(line, cell, "|")
            task = cell[2]; ac = cell[3]
            gsub(/[[:space:]]/, "", task); gsub(/[[:space:]]/, "", ac)
            resto = line
            sub(/^[^|]*\|[^|]*\|[^|]*\|/, "", resto)
            resto = tolower(resto)
            st = (resto ~ strict) ? 1 : 0
            ex = (st == 1) ? 1 : ((resto ~ ext) ? 1 : 0)
            key = task SUBSEP ac
            if (!(key in seen)) { nrow++; order[nrow] = key; seen[key] = 1 }
            sarr[key] = st; earr[key] = ex
          }
          END {
            for (i = 1; i <= nrow; i++) {
              k = order[i]
              split(k, p, SUBSEP)
              printf "%s\t%s\t%s\t%d\t%d\n", ep, p[1], p[2], sarr[k], earr[k]
            }
          }' >>"$ROWS"
  done
}

# --- Tabla §4 del doc de deuda --------------------------------------------------------
# Filas | EP-01 | 6 | ... | y la de totales | **Total** | **49** | |
extract_debt_rows() {
  awk '
    /^## 4\./ { dentro = 1; next }
    dentro && /^## / { exit }
    dentro && /^\|[[:space:]]*\*\*[[:space:]]*Total/ {
      split($0, c, "|"); v = c[3]; gsub(/[^0-9]/, "", v)
      if (v != "") printf "TOTAL\t%s\t%d\n", v, NR
    }
    dentro && /^\|[[:space:]]*EP-[0-9][0-9][[:space:]]*\|/ {
      ep = $0; sub(/^\|[[:space:]]*/, "", ep); sub(/[[:space:]]*\|.*$/, "", ep)
      split($0, c, "|"); v = c[3]; gsub(/[^0-9]/, "", v)
      if (v != "") printf "%s\t%s\t%d\n", ep, v, NR
    }
  ' "$DEBT_DOC" >"$DOCROWS"
}

# --- Evidencia de campo ---------------------------------------------------------------
# Un fichero de evidence/ cita un criterio si menciona a la vez un T-XXXX y un ACn.
extract_cited() {
  local f tasks acs t a
  [ -d "$EVIDENCE_DIR" ] || return 0
  for f in "$EVIDENCE_DIR"/*; do
    [ -f "$f" ] || continue
    tasks=$(grep -oE '\bT-[0-9]{4}' "$f" 2>/dev/null | sort -u)
    acs=$(grep -oiE '\bAC[[:space:]]*[0-9]+' "$f" 2>/dev/null | grep -oE '[0-9]+' | sort -un)
    [ -n "$tasks" ] && [ -n "$acs" ] || continue
    for t in $tasks; do
      for a in $acs; do
        printf '%s\tAC%s\t%s\n' "$t" "$a" "${f#"$EVIDENCE_DIR"/}" >>"$CITED"
      done
    done
  done
}

repo_counts() { # <epica> -> «estricto extendido» del repo (0 0 si no aparece)
  awk -F'\t' -v ep="$1" '$1 == ep { printf "%s %s\n", $2, $3; found = 1; exit }
                          END { if (!found) print "0 0" }' "$COUNTS"
}

extract_rows
awk -F'\t' '
  { if (!($1 in seen)) { seen[$1] = 1; n++; ord[n] = $1 }; s[$1] += $4; e[$1] += $5 }
  END { for (i = 1; i <= n; i++) printf "%s\t%d\t%d\n", ord[i], s[ord[i]], e[ord[i]] }
' "$ROWS" >"$COUNTS"

TOTAL_ROWS=$(awk 'END { print NR + 0 }' "$ROWS")
TOTAL_STRICT=$(awk -F'\t' '{ t += $2 } END { print t + 0 }' "$COUNTS")
TOTAL_EXT=$(awk -F'\t' '{ t += $3 } END { print t + 0 }' "$COUNTS")
PEND_EXT=$(awk -F'\t' '$5 == 1 { c++ } END { print c + 0 }' "$ROWS")

extract_debt_rows
EVID_FILES=$(find "$EVIDENCE_DIR" -maxdepth 1 -type f 2>/dev/null | wc -l | tr -d ' ')
extract_cited
EVID_CITADOS=$(awk 'END { print NR + 0 }' "$CITED")

# --- Avisos y fallos: se encolan por seccion; el modo decide donde se imprimen --------
# En json/list los mensajes van a stderr (stdout queda limpio para parsear o para --list).
CURBUF="$DEBTMSG"
if [ "$MODE" = 'report' ]; then
  say_ok()    { printf 'ok    %s\n' "$1" >>"$CURBUF"; }
  say_aviso() { printf 'AVISO %s\n' "$1" >>"$CURBUF"; AVISOS=$((AVISOS + 1)); }
  say_fail()  { printf 'FAIL  %s\n' "$1" >>"$CURBUF"; FAIL=$((FAIL + 1)); }
else
  say_ok()    { :; }
  say_aviso() { printf 'AVISO %s\n' "$1" >&2; AVISOS=$((AVISOS + 1)); }
  say_fail()  { printf 'FAIL  %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
fi

DOC_TOTAL=''
DOC_TOTAL_LN=''
debt_check() {   # comparamos la columna del doc contra el recuento ESTRICTO del repo
  local ep docv st ext ln
  local doc_epicas="$TMP/doc-epicas.txt"; : >"$doc_epicas"
  while IFS=$'\t' read -r ep docv ln; do
    if [ "$ep" = 'TOTAL' ]; then DOC_TOTAL="$docv"; DOC_TOTAL_LN="$ln"; continue; fi
    printf '%s\n' "$ep" >>"$doc_epicas"
    read -r st ext <<<"$(repo_counts "$ep")"
    if [ "$docv" = "$st" ]; then
      say_ok "deuda §4 $ep: doc=$docv = script=$st"
    else
      say_aviso "deuda §4 $ep: doc=$docv script=$st -> corrige la fila $ep en $DEBT_REL:$ln"
      printf '%s\t%s\t%s\t%s\n' "$ep" "$docv" "$st" "$ln" >>"$DEBTDIV"
    fi
  done <"$DOCROWS"

  # Epicas con pendientes estrictos en el repo que no tienen fila en la tabla del doc.
  while IFS=$'\t' read -r ep st ext; do
    [ "$st" -gt 0 ] || continue
    grep -qx "$ep" "$doc_epicas" && continue
    say_aviso "deuda §4 $ep: script=$st y la tabla §4 de $DEBT_REL no tiene fila para $ep"
    printf '%s\tnull\t%s\tsin-fila\n' "$ep" "$st" >>"$DEBTDIV"
  done <"$COUNTS"

  if [ -z "$DOC_TOTAL" ]; then
    say_aviso "deuda §4: $DEBT_REL no tiene fila de Total -> no se puede cruzar el total"
  elif [ "$DOC_TOTAL" = "$TOTAL_STRICT" ]; then
    say_ok "deuda §4 TOTAL: doc=$DOC_TOTAL = script=$TOTAL_STRICT"
  else
    say_aviso "deuda §4 TOTAL: doc=$DOC_TOTAL script=$TOTAL_STRICT -> corrige la fila Total en $DEBT_REL:$DOC_TOTAL_LN"
    printf 'TOTAL\t%s\t%s\t%s\n' "$DOC_TOTAL" "$TOTAL_STRICT" "$DOC_TOTAL_LN" >>"$DEBTDIV"
  fi
}

evidence_check() {
  local ep t a st ext fich
  while IFS=$'\t' read -r ep t a st ext; do
    [ "$ext" = '1' ] || continue
    fich=$(awk -F'\t' -v t="$t" -v a="$a" '$1 == t && $2 == a { print $3; exit }' "$CITED")
    [ -n "$fich" ] || continue
    printf '%s\t%s\t%s\t%s\n' "$ep" "$t" "$a" "$fich" >>"$EVIDFAIL"
    say_fail "fila pendiente con evidencia ya recogida: $ep $t $a citada por docs/verification/evidence/$fich — actualiza el veredicto en docs/verification/$ep.md"
  done <"$ROWS"
}

CURBUF="$DEBTMSG"
if [ ! -s "$DOCROWS" ]; then
  say_aviso "deuda §4: no hay tabla en la seccion 4 de $DEBT_REL -> no se puede cruzar"
else
  debt_check
fi
CURBUF="$EVIDMSG"
evidence_check
if [ "$FAIL" -eq 0 ]; then
  say_ok 'ninguna fila pendiente esta citada por la evidencia recogida'
fi

# --- Salida ---------------------------------------------------------------------------
report_render() {
  printf '# Cierre de criterios de campo — %s/EP-XX.md\n' "${VERIF#"$ROOT"/}"
  printf 'filas de tabla leidas: %s | pendientes (estricto=%s, extendido=%s)\n' \
    "$TOTAL_ROWS" "$TOTAL_STRICT" "$TOTAL_EXT"
  printf 'marcadores: estricto="%s" | extra="%s"\n\n' "$MARK_STRICT" "$MARK_EXTRA"
  printf '%-8s %9s %10s\n' 'Epica' 'estricto' 'extendido'
  awk -F'\t' '{ printf "%-8s %9s %10s\n", $1, $2, $3 }' "$COUNTS"
  printf '%-8s %9s %10s\n' 'TOTAL' "$TOTAL_STRICT" "$TOTAL_EXT"

  printf '\n## Cruce con %s (tabla §4, columna «Pendientes de campo» = BLOCKED-HW)\n' "$DEBT_REL"
  cat "$DEBTMSG"

  printf '\n## Evidencia de campo (%s/, %s ficheros, %s citas)\n' \
    "${EVIDENCE_DIR#"$ROOT"/}" "$EVID_FILES" "$EVID_CITADOS"
  cat "$EVIDMSG"

  printf '\n## Resultado\n'
  if [ "$FAIL" -gt 0 ]; then
    printf 'RESULTADO: FAIL (%s)\n' "$FAIL"
  elif [ "$STRICT_DEBT" -eq 1 ] && [ "$AVISOS" -gt 0 ]; then
    printf 'RESULTADO: FAIL (%s avisos de deuda con --strict)\n' "$AVISOS"
  elif [ "$AVISOS" -gt 0 ]; then
    printf 'RESULTADO: OK — %s avisos de deuda con %s (usa --strict para que fallen)\n' \
      "$AVISOS" "$DEBT_REL"
  else
    printf 'RESULTADO: OK\n'
  fi
}

json_render() {
  local first=1 ep st ext docv ln t a fich
  printf '{\n'
  printf '  "filas_tabla": %s,\n' "$TOTAL_ROWS"
  printf '  "marcadores": { "estricto": "%s", "extra": "%s" },\n' "$MARK_STRICT" "$MARK_EXTRA"
  printf '  "epicas": ['
  first=1
  while IFS=$'\t' read -r ep st ext; do
    [ "$first" -eq 1 ] || printf ','
    first=0
    printf '\n    { "epica": "%s", "estricto": %s, "extendido": %s }' "$ep" "$st" "$ext"
  done <"$COUNTS"
  printf '\n  ],\n'
  printf '  "totales": { "estricto": %s, "extendido": %s, "pendientes": %s },\n' \
    "$TOTAL_STRICT" "$TOTAL_EXT" "$PEND_EXT"
  printf '  "deuda": { "documento": "%s", "coincide": ' "$DEBT_REL"
  if [ "$AVISOS" -eq 0 ]; then printf 'true'; else printf 'false'; fi
  printf ', "doc_total": %s, "script_total": %s, "divergencias": [' \
    "${DOC_TOTAL:-null}" "$TOTAL_STRICT"
  first=1
  while IFS=$'\t' read -r ep docv st ln; do
    [ "$first" -eq 1 ] || printf ','
    first=0
    printf '\n    { "epica": "%s", "doc": %s, "script": %s, "linea_doc": "%s" }' "$ep" "$docv" "$st" "$ln"
  done <"$DEBTDIV"
  printf '\n  ] },\n'
  printf '  "evidencia": { "ficheros": %s, "citas": %s, "pendientes_citadas": [' \
    "$EVID_FILES" "$EVID_CITADOS"
  first=1
  while IFS=$'\t' read -r ep t a fich; do
    [ "$first" -eq 1 ] || printf ','
    first=0
    printf '\n    { "epica": "%s", "task": "%s", "ac": "%s", "fichero": "%s" }' "$ep" "$t" "$a" "$fich"
  done <"$EVIDFAIL"
  printf '\n  ] },\n'
  printf '  "resultado": "%s",\n' "$( [ "$FAIL" -gt 0 ] && printf 'FAIL' || { [ "$STRICT_DEBT" -eq 1 ] && [ "$AVISOS" -gt 0 ] && printf 'FAIL' || printf 'OK'; } )"
  printf '  "codigo": %s\n' "$( [ "$FAIL" -gt 0 ] && printf 1 || { [ "$STRICT_DEBT" -eq 1 ] && [ "$AVISOS" -gt 0 ] && printf 1 || printf 0; } )"
  printf '}\n'
}

list_render() {
  awk -F'\t' '$5 == 1 { printf "%s %s %s\n", $1, $2, $3 }' "$ROWS"
}

case "$MODE" in
  json)   json_render ;;
  list)   list_render ;;
  report) report_render ;;
esac

# --- Codigos de salida ----------------------------------------------------------------
if [ "$FAIL" -gt 0 ]; then exit 1; fi
if [ "$STRICT_DEBT" -eq 1 ] && [ "$AVISOS" -gt 0 ]; then exit 1; fi
exit 0
