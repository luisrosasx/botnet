#!/usr/bin/env bash
# collect_test.sh — prueba de campo de collect.sh, ejecutada de verdad.
#
# Uso:   bash scripts/field/collect_test.sh
# Exit:  0 si todas las comprobaciones pasan; 1 si alguna falla;
#        3 si falta una herramienta imprescindible (sha256).
#
# Comprobaciones:
#   1) --help sale 0 y menciona --dry.
#   2) sin --label sale 2 con mensaje claro.
#   3) una pasada real con dos ficheros temporales que citan 'T-0301 AC3'
#      crea docs/verification/evidence/field-<fecha>-test/, su INDEX.md
#      contiene T-0301 y el sha256 de los ficheros aparece en el indice.
#   4) --dry no crea nada.
# Al final limpia el/los directorio(s) que creo, para dejar el repo igual.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2
S="scripts/field/collect.sh"
EV="docs/verification/evidence"

if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  echo "collect_test: sin sha256sum ni shasum no hay prueba posible" >&2
  exit 3
fi

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -c1-64
  else shasum -a 256 "$1" | cut -c1-64; fi
}

TMPD="$(mktemp -d)"
CREADOS=""          # directorios de evidencia que hay que borrar al salir
limpia() {
  rm -rf "$TMPD"
  for d in $CREADOS; do rm -rf "$d"; done
}
trap limpia EXIT

FALLAS=0
ok() { printf '  ok   %s\n' "$*"; }
ko() { printf '  FALLA %s\n' "$*"; FALLAS=$(( FALLAS + 1 )); }

printf 'collect_test (%s)\n' "$S"

# --- 1) --help --------------------------------------------------------
H="$(bash "$S" --help 2>&1)"; RC=$?
if [ "$RC" = 0 ] && printf '%s' "$H" | grep -qi -- '--dry'; then
  ok "--help sale 0 y documenta --dry"
else
  ko "--help deberia salir 0 y mencionar --dry (rc=$RC)"
fi

# --- 2) sin --label: 2 con mensaje ------------------------------------
ERR="$(bash "$S" --paths "$TMPD/x" 2>&1 >/dev/null)"; RC=$?
if [ "$RC" = 2 ] && printf '%s' "$ERR" | grep -qi -- '--label'; then
  ok "sin --label sale 2 con mensaje claro: $ERR"
else
  ko "sin --label deberia salir 2 y nombrar --label (rc=$RC, msg='$ERR')"
fi

# --- 3) --dry no crea nada -------------------------------------------
printf 'pasada de humo T-0404 AC2\n' > "$TMPD/seco.txt"
BEFORE="$(ls "$EV" 2>/dev/null | wc -l | tr -d ' ')"
bash "$S" --label seco --paths "$TMPD/seco.txt" --dry >/dev/null 2>&1; RCD=$?
AFTER="$(ls "$EV" 2>/dev/null | wc -l | tr -d ' ')"
if [ "$RCD" = 0 ] && [ "$BEFORE" = "$AFTER" ]; then
  ok "--dry sale 0 y no crea directorios en $EV"
else
  ko "--dry no deberia crear nada (rc=$RCD, antes=$BEFORE despues=$AFTER)"
fi

# --- 4) pasada real con cita de criterio ------------------------------
printf 'humo de bootloader T-0301 AC3 en TP-Link ER60\n' > "$TMPD/nota.txt"
printf 'guard con cron: T-0301 AC3 y leave_code en la baja\n' > "$TMPD/log.txt"
SHA_NOTA="$(sha256_of "$TMPD/nota.txt")"
SHA_LOG="$(sha256_of "$TMPD/log.txt")"

OUT="$(bash "$S" --label test --paths "$TMPD/nota.txt,$TMPD/log.txt" --json 2>/dev/null)"; RC=$?
if command -v jq >/dev/null 2>&1; then
  DIR="$(printf '%s' "$OUT" | jq -r '.evidence_dir' 2>/dev/null)"
else
  DIR="$(printf '%s' "$OUT" | sed -e 's/.*"evidence_dir":"\([^"]*\)".*/\1/')"
fi
[ -n "$DIR" ] && [ -d "$DIR" ] && CREADOS="$DIR"

if [ "$RC" = 0 ] && [ -n "$DIR" ] && [ -d "$DIR" ]; then
  ok "pasada real sale 0 y crea $DIR/"
else
  ko "pasada real deberia crear el directorio de evidencia (rc=$RC, dir='$DIR')"
fi

if [ -n "$DIR" ] && [ -f "$DIR/INDEX.md" ]; then
  ok "INDEX.md existe dentro de $DIR/"
  if grep -q 'T-0301' "$DIR/INDEX.md"; then
    ok "INDEX.md cita T-0301 (fila de criterio extraida de los artefactos)"
  else
    ko "INDEX.md no cita T-0301"
  fi
  if grep -q 'AC3' "$DIR/INDEX.md"; then
    ok "INDEX.md cita AC3"
  else
    ko "INDEX.md no cita AC3"
  fi
  if grep -q "$SHA_NOTA" "$DIR/INDEX.md"; then
    ok "INDEX.md trae el sha256 de nota.txt (${SHA_NOTA:0:16}...)"
  else
    ko "INDEX.md sin sha256 de nota.txt"
  fi
  if grep -q "$SHA_LOG" "$DIR/INDEX.md"; then
    ok "INDEX.md trae el sha256 de log.txt (${SHA_LOG:0:16}...)"
  else
    ko "INDEX.md sin sha256 de log.txt"
  fi
  if grep -q 'Comando:' "$DIR/INDEX.md" && grep -q 'Host:' "$DIR/INDEX.md" && grep -q 'Etiqueta: test' "$DIR/INDEX.md"; then
    ok "INDEX.md trae fecha, host, etiqueta y el comando que lo genero"
  else
    ko "INDEX.md le falta fecha/host/etiqueta/comando"
  fi
  N="$(find "$DIR" -maxdepth 1 -type f | wc -l | tr -d ' ')"
  if [ "$N" = 3 ]; then
    ok "$DIR/ tiene 2 artefactos copiados + INDEX.md"
  else
    ko "$DIR/ deberia tener 3 ficheros (2 artefactos + INDEX.md), hay $N"
  fi
else
  ko "no hay INDEX.md que comprobar"
fi

# --- 5) nunca borra: dos pasadas con la misma etiqueta conviven -------
if [ -n "$DIR" ]; then
  sleep 1     # la marca de tiempo es a segundos: sin pausa repetiria nombre
  OUT2="$(bash "$S" --label test --paths "$TMPD/nota.txt" --json 2>/dev/null)"
  if command -v jq >/dev/null 2>&1; then
    DIR2="$(printf '%s' "$OUT2" | jq -r '.evidence_dir' 2>/dev/null)"
  else
    DIR2="$(printf '%s' "$OUT2" | sed -e 's/.*"evidence_dir":"\([^"]*\)".*/\1/')"
  fi
  [ -n "$DIR2" ] && [ -d "$DIR2" ] && CREADOS="$CREADOS $DIR2"
  if [ -n "$DIR2" ] && [ -d "$DIR" ] && [ -d "$DIR2" ] && [ "$DIR" != "$DIR2" ]; then
    ok "la segunda pasada no pisa la primera ($DIR/ y $DIR2/ conviven)"
  else
    ko "las dos pasadas deberian dejar directorios distintos y vivos"
  fi
fi

printf '%s\n' "---"
if [ "$FALLAS" = 0 ]; then
  printf 'collect_test: TODAS las comprobaciones pasaron\n'
  exit 0
fi
printf 'collect_test: %d comprobacion(es) fallida(s)\n' "$FALLAS"
exit 1
