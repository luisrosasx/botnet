#!/usr/bin/env bash
# collect.sh — empaqueta la evidencia de una pasada de campo.
#
# Crea docs/verification/evidence/field-<YYYYMMDD-HHMMSS>-<label>/, copia
# dentro los artefactos pedidos (o los que encuentra bajo --from si no se
# le da --paths) y escribe un INDEX.md con: fecha, host, etiqueta, el
# comando exacto que lo genero, la lista de ficheros con tamano y sha256,
# y las FILAS DE CRITERIO que se citan dentro de esos artefactos
# (T-\d{4} y AC\d) — lo que permite atar una evidencia de campo con su
# criterio del plan sin depender de la memoria de nadie.
#
# Sin argumentos peligrosos: nunca borra nada (si el directorio ya existe
# le pone sufijo -2, -3...), no escribe fuera del repo y rechaza rutas
# con `..`.
#
# Uso:
#   scripts/field/collect.sh --label <texto> [--from <dir>] [--paths a,b,c]
#     [--dry] [--json] [--help]
# Ejemplos:
#   scripts/field/collect.sh --label tp-link --paths dist/qemu-logs/boot.log,docs/verification/EP-05.md
#   scripts/field/collect.sh --label mips --from dist/qemu-logs --dry --json
# Salida: humano por stdout; con --json SOLO el JSON va por stdout (los
# avisos y los trazas de --dry salen por stderr, para que jq pueda leer).
# Exit: 0 evidencia empaquetada (o impresa en --dry); 2 argumentos
# invalidos (falta --label, etiqueta insegura, ruta fuera del repo);
# 3 herramienta o directorio ausente (sin sha256, --from inexistente).
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT" || exit 2

# Sin `source scripts/env.sh`: coleccionar evidencia es bash + coreutils;
# env.sh hace `exit 1` si falta Go y no debe tumbar un cierre de campo.
EVIDENCE_DIR="docs/verification/evidence"
MAX_FILES=200          # tope de artefactos por pasada (evita tragarse un arbol enorme)

USAGE='collect.sh — empaqueta la evidencia de una pasada de campo

Uso:
  bash scripts/field/collect.sh --label <texto> [--from <dir>] [--paths a,b,c]
       [--dry] [--json] [--help]

  --label   etiqueta de la pasada (obligatoria; letras, digitos, . _ -).
  --from    donde buscar artefactos si no se dan --paths (defecto:
            docs/verification).
  --paths   lista separada por comas de ficheros o directorios (relativos
            al repo o absolutos dentro del repo) que copiar.
  --dry     imprime lo que haria (directorio, ficheros, criterios), sin
            crear nada.
  --json    salida JSON (solo el JSON por stdout).
  --help    esta ayuda.

Sin argumentos peligrosos: nunca borra, nunca sale del repo.
Si el directorio de destino ya existe, se crea field-...-<label>-2/.'

LABEL="" FROM="docs/verification" PATHS="" DRY=0 JSON=0

log() { # avisos: stderr con --json para dejar el stdout limpio
  if [ "$JSON" = 1 ]; then printf '%s\n' "$*" >&2; else printf '%s\n' "$*"; fi
}

die2() { printf 'collect: %s\n' "$*" >&2; exit 2; }
die3() { printf 'collect: %s\n' "$*" >&2; exit 3; }

while [ $# -gt 0 ]; do
  case "$1" in
    --help|-h) printf '%s\n' "$USAGE"; exit 0 ;;
    --label)   [ -n "${2:-}" ] || die2 "--label sin valor"; LABEL="$2"; shift 2; continue ;;
    --label=*) LABEL="${1#*=}" ;;
    --from)    [ -n "${2:-}" ] || die2 "--from sin valor"; FROM="$2"; shift 2; continue ;;
    --from=*)  FROM="${1#*=}" ;;
    --paths)   [ -n "${2:-}" ] || die2 "--paths sin valor"; PATHS="$2"; shift 2; continue ;;
    --paths=*) PATHS="${1#*=}" ;;
    --dry)     DRY=1 ;;
    --json)    JSON=1 ;;
    *)         die2 "opcion desconocida: $1 (prueba --help)" ;;
  esac
  shift
done

[ -n "$LABEL" ] || die2 "falta --label <texto> (prueba --help)"
case "$LABEL" in
  *[!A-Za-z0-9._-]*) die2 "etiqueta insegura: '$LABEL' (solo letras, digitos, punto, guion y bajo guion)" ;;
esac

# --- sha256: imprescindible para que la evidencia sea atable ----------
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" 2>/dev/null | cut -c1-64
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" 2>/dev/null | cut -c1-64
  fi
}
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  die3 "ni sha256sum ni shasum disponibles: sin huella no hay evidencia atable"
fi

size_of() { wc -c < "$1" 2>/dev/null | tr -d ' '; }

# --- rutas ------------------------------------------------------------
# Se le puede dar artefactos de fuera del repo (un log recogido en /tmp
# durante la pasada) porque collect solo LEE ahi y escribe siempre dentro.
# En cambio se rechaza cualquier ruta con `..`: es la firma de un error de
# invocacion, y collect prefiere pararse a copiar la cosa equivocada.
abs_of() { # $1=ruta -> stdout ruta absoluta (sin resolver enlaces)
  local p="$1" a
  case "$p" in
    /*) a="$p" ;;
    [A-Za-z]:/*|[A-Za-z]:\\*) a="$p" ;;   # ruta absoluta tipo Windows
    *) a="$ROOT/$p" ;;
  esac
  printf '%s' "${a%/}"
}

rel_or_abs() { # $1=ruta -> relativa al repo si cae dentro; si no, absoluta
  local a; a="$(abs_of "$1")"
  case "$a" in
    "$ROOT") printf '.' ;;
    "$ROOT"/*) printf '%s' "${a#"$ROOT"/}" ;;
    *) printf '%s' "$a" ;;
  esac
}

REQ=()   # origenes a copiar (absolutos)
BAD=""
add_path() { # $1 = fichero o directorio tal como lo dio el usuario
  local p r n
  p="$(abs_of "$1")"
  if [ ! -e "$p" ]; then BAD="$1 (no existe)"; return 1; fi
  if [ -f "$p" ]; then REQ+=("$p"); return 0; fi
  n=0
  while IFS= read -r r; do
    [ -n "$r" ] || continue
    REQ+=("$r")
    n=$(( n + 1 ))
  done < <(find "$p" -maxdepth 2 -type f 2>/dev/null | sort | head -n 50)
  [ "$n" -gt 0 ] || { BAD="$1 (directorio vacio)"; return 1; }
  return 0
}

if [ -n "$PATHS" ]; then
  OLDIFS=$IFS; IFS=','      # coma como separador, sin partir por espacios
  for p in $PATHS; do
    IFS=$OLDIFS
    if [ -n "$p" ]; then
      case "$p" in *..*) die2 "ruta con '..': $p (collect no sale del repo)" ;; esac
      add_path "$p" || die2 "ruta invalida: $BAD"
    fi
    IFS=$OLDIFS
  done
  IFS=$OLDIFS
else
  [ -d "$FROM" ] || die3 "--from inexistente: $FROM"
  log "sin --paths: se buscan artefactos bajo $FROM (maxdepth 2, tope $MAX_FILES)"
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    REQ+=("$f")
  done < <(find "$FROM" -maxdepth 2 -type f \
             ! -path "$EVIDENCE_DIR/*" \
             \( -name '*.log' -o -name '*.json' -o -name '*.md' -o -name '*.txt' -o -name '*.csv' \) \
             2>/dev/null | sort | head -n "$MAX_FILES")
  if [ "${#REQ[@]}" -eq 0 ]; then
    # Doble simple: sin filtros, los ficheros sueltos del --from.
    while IFS= read -r f; do
      [ -n "$f" ] || continue
      REQ+=("$f")
    done < <(find "$FROM" -maxdepth 1 -type f 2>/dev/null | sort | head -n "$MAX_FILES")
  fi
  [ "${#REQ[@]}" -gt 0 ] || die2 "ni artefactos en $FROM ni --paths: no hay nada que empaquetar"
fi

# --- destino: nunca borra, nunca pisa ---------------------------------
STAMP="$(date +%Y%m%d-%H%M%S)"
DEST="$EVIDENCE_DIR/field-$STAMP-$LABEL"
if [ -e "$DEST" ]; then
  i=2
  while [ -e "$DEST-$i" ]; do i=$(( i + 1 )); done
  log "aviso: $DEST ya existia → se usara $DEST-$i"
  DEST="$DEST-$i"
fi

GEN_CMD="bash scripts/field/collect.sh --label $LABEL"
[ -z "$FROM" ]  || GEN_CMD="$GEN_CMD --from $FROM"
[ -z "$PATHS" ] || GEN_CMD="$GEN_CMD --paths $PATHS"
[ "$DRY" = 1 ]  && GEN_CMD="$GEN_CMD --dry"
[ "$JSON" = 1 ] && GEN_CMD="$GEN_CMD --json"
HOSTN="$(hostname 2>/dev/null || echo host-desconocido)"
WHEN="$(date '+%Y-%m-%d %H:%M:%S %z')"

# --- copia ------------------------------------------------------------
COPIED=()   # nombre dentro del destino
SRCS=()     # origen mostrado (relativo al repo si cae dentro; si no, absoluto)
copy_one() { # $1 = origen absoluto
  local src="$1" base flat other
  base="$(basename "$src")"
  # Nombre simple si no hay colision dentro de la misma pasada; si la hay,
  # se prefija con el directorio (docs__verification__EP-05.md) para no pisar.
  for other in ${COPIED[@]+"${COPIED[@]}"}; do
    if [ "$other" = "$base" ]; then
      flat="$(rel_or_abs "$(dirname "$src")")"
      [ "$flat" = "." ] && flat=""
      [ -n "$flat" ] && base="$(printf '%s' "$flat" | tr '/:' '__')__${base}"
      break
    fi
  done
  if [ "$DRY" = 0 ]; then
    cp "$src" "$DEST/$base" 2>/dev/null || { log "aviso: no se pudo copiar $src"; return 1; }
  fi
  SRCS+=("$(rel_or_abs "$src")")
  COPIED+=("$base")
  return 0
}

if [ "$DRY" = 0 ]; then
  mkdir -p "$DEST" || die3 "no se pudo crear $DEST"
fi
for rel in ${REQ[@]+"${REQ[@]}"}; do
  copy_one "$rel"
done
[ "${#COPIED[@]}" -gt 0 ] || die2 "ningun artefacto copiable (revisa --paths)"

# --- filas de criterio citadas ---------------------------------------
# Se leen de los originales del repo: mismo contenido que la copia, y en
# --dry no hay copia que leer.
CRIT_T="$(cat ${REQ[@]+"${REQ[@]}"} 2>/dev/null | grep -oE 'T-[0-9]{4}' 2>/dev/null | sort -u | tr '\n' ' ' | sed -e 's/ *$//')"
CRIT_AC="$(cat ${REQ[@]+"${REQ[@]}"} 2>/dev/null | grep -oE 'AC[0-9]+' 2>/dev/null | sort -u | tr '\n' ' ' | sed -e 's/ *$//')"

# --- INDEX.md ---------------------------------------------------------
if [ "$DRY" = 0 ]; then
  {
    printf '# Evidencia de campo: %s\n\n' "$LABEL"
    printf -- '- Fecha: %s\n' "$WHEN"
    printf -- '- Host: %s\n' "$HOSTN"
    printf -- '- Etiqueta: %s\n' "$LABEL"
    printf -- '- Directorio: %s/\n' "$DEST"
    printf -- '- Comando: `%s`\n' "$GEN_CMD"
    printf -- '- Artefactos: %d\n\n' "${#COPIED[@]}"
    printf '## Ficheros\n\n'
    printf '| fichero | tamano (bytes) | sha256 |\n|---|---|---|\n'
    i=0
    while [ "$i" -lt "${#COPIED[@]}" ]; do
      b="${COPIED[$i]}"
      printf '| %s | %s | %s |\n' "$b" "$(size_of "$DEST/$b")" "$(sha256_of "$DEST/$b")"
      i=$(( i + 1 ))
    done
    printf '\n## Filas de criterio citadas\n\n'
    printf 'Extraidas de los artefactos copiados con `T-[0-9]{4}` y `AC[0-9]+`.\n\n'
    printf '| tipo | criterios citados |\n|---|---|\n'
    printf '| T (tarea del plan) | %s |\n' "$( [ -n "$CRIT_T" ] && printf '%s' "$CRIT_T" || printf '—' )"
    printf '| AC (criterio de aceptacion) | %s |\n' "$( [ -n "$CRIT_AC" ] && printf '%s' "$CRIT_AC" || printf '—' )"
    printf '\ncollect.sh no borra nada y no escribe fuera de %s/.\n' "$EVIDENCE_DIR"
  } > "$DEST/INDEX.md" || die3 "no se pudo escribir $DEST/INDEX.md"
  log "evidencia empaquetada en $DEST/ (${#COPIED[@]} artefactos + INDEX.md)"
else
  log "(dry) crearia $DEST/ y copiaria ${#COPIED[@]} artefactos:"
  for c in ${COPIED[@]+"${COPIED[@]}"}; do log "  + $c"; done
  log "(dry) escribira $DEST/INDEX.md con fecha, host, etiqueta, comando, tamano + sha256 y las filas de criterio"
  log "(dry) criterios detectados: T=[${CRIT_T:-ninguno}] AC=[${CRIT_AC:-ninguno}]"
fi

# --- JSON -------------------------------------------------------------
json_escape() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

if [ "$JSON" = 1 ]; then
  FILES_JSON=""
  i=0
  while [ "$i" -lt "${#COPIED[@]}" ]; do
    b="${COPIED[$i]}"; src="${SRCS[$i]}"
    if [ "$DRY" = 1 ]; then SZ="?"; SH="?"; else SZ="$(size_of "$DEST/$b")"; SH="$(sha256_of "$DEST/$b")"; fi
    [ -n "$FILES_JSON" ] && FILES_JSON="$FILES_JSON,"
    FILES_JSON="$FILES_JSON{\"name\":\"$(json_escape "$b")\",\"source\":\"$(json_escape "$src")\",\"size_bytes\":\"$SZ\",\"sha256\":\"$SH\"}"
    i=$(( i + 1 ))
  done
  printf '{"label":"%s","host":"%s","generated_at":"%s","command":"%s","evidence_dir":"%s","dry":%s,"file_count":%d,"files":[%s],"criteria":{"T":"%s","AC":"%s"},"index_md":"%s/INDEX.md","borra_nada":true}\n' \
    "$(json_escape "$LABEL")" "$(json_escape "$HOSTN")" "$(json_escape "$WHEN")" "$(json_escape "$GEN_CMD")" \
    "$(json_escape "$DEST")" "$( [ "$DRY" = 1 ] && printf true || printf false )" "${#COPIED[@]}" "$FILES_JSON" \
    "$(json_escape "$CRIT_T")" "$(json_escape "$CRIT_AC")" "$(json_escape "$DEST")"
else
  printf 'collect: %s/\n' "$DEST"
  printf '  artefactos:  %d\n' "${#COPIED[@]}"
  printf '  criterios:   T=[%s] AC=[%s]\n' "${CRIT_T:-ninguno}" "${CRIT_AC:-ninguno}"
  printf '  indice:      %s\n' "$DEST/INDEX.md"
fi

exit 0
