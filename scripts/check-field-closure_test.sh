#!/usr/bin/env bash
# Prueba de scripts/check-field-closure.sh: verde con el repo tal cual, rojo si un
# fichero de docs/verification/evidence/ cita una fila todavia marcada pendiente.
# Crea y borra docs/verification/evidence/field-test-tmp.md (limpieza garantizada).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT/scripts/check-field-closure.sh"
EVIDENCE_DIR="$ROOT/docs/verification/evidence"
TMPFILE="$EVIDENCE_DIR/field-test-tmp.md"
FALLOS=0

cleanup() { rm -f "$TMPFILE"; }
trap cleanup EXIT

pass() { printf 'ok   %s\n' "$1"; }
fail() { printf 'FALLO %s\n' "$1"; FALLOS=$((FALLOS + 1)); }

if [ ! -f "$CHECK" ]; then
  printf 'FALLO no existe %s\n' "${CHECK#"$ROOT"/}"
  exit 1
fi

# --- Caso verde: el repo tal cual no tiene incoherencias duras ------------------------
SALIDA_VERDE="$(bash "$CHECK" 2>&1)"; COD_VERDE=$?
if [ "$COD_VERDE" -eq 0 ]; then
  pass "caso verde: exit 0 con el repo actual"
else
  fail "caso verde: exit $COD_VERDE (esperado 0)"
  printf '%s\n' "$SALIDA_VERDE" | sed -n 's/^FAIL /     /p'
fi

# --- Caso rojo: evidencia ya recogida sobre una fila pendiente -----------------------
printf '# Prueba temporal del validador\n\nT-0101 AC1 medido en campo\n' >"$TMPFILE"
SALIDA_ROJA="$(bash "$CHECK" 2>&1)"; COD_ROJO=$?
if [ "$COD_ROJO" -eq 1 ]; then
  pass "caso rojo: exit 1 con $TMPFILE present"
else
  fail "caso rojo: exit $COD_ROJO (esperado 1)"
fi
if printf '%s\n' "$SALIDA_ROJA" | grep -q 'T-0101'; then
  pass 'caso rojo: el mensaje nombra T-0101'
else
  fail 'caso rojo: el mensaje no nombra T-0101'
fi
printf '%s\n' "$SALIDA_ROJA" | sed -n 's/^FAIL  /     /p'

# --- Limpieza: retirado el fichero temporal, vuelve el verde -------------------------
rm -f "$TMPFILE"
SALIDA_LIMPIA="$(bash "$CHECK" 2>&1)"; COD_LIMPIA=$?
if [ "$COD_LIMPIA" -eq 0 ]; then
  pass 'limpieza: exit 0 tras borrar el fichero temporal'
else
  fail "limpieza: exit $COD_LIMPIA (esperado 0)"
  printf '%s\n' "$SALIDA_LIMPIA" | sed -n 's/^FAIL /     /p'
fi
if [ ! -e "$TMPFILE" ]; then
  pass 'limpieza: no queda field-test-tmp.md en evidence/'
else
  fail 'limpieza: sigue quedando field-test-tmp.md en evidence/'
fi

# --- Contrato de uso: opcion desconocida -> 2 ----------------------------------------
bash "$CHECK" --noexiste >/dev/null 2>&1; COD_USAGE=$?
if [ "$COD_USAGE" -eq 2 ]; then
  pass 'uso: opcion desconocida devuelve 2'
else
  fail "uso: opcion desconocida devuelve $COD_USAGE (esperado 2)"
fi

printf '\nRESULTADO: %s\n' "$( [ "$FALLOS" -eq 0 ] && echo 'TEST OK' || echo "TEST FAIL ($FALLOS)" )"
[ "$FALLOS" -eq 0 ] || exit 1
exit 0
