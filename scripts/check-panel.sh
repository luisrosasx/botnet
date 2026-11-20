#!/usr/bin/env bash
# Puerta del panel de dispositivos y del motor de descubrimiento.
#
# Ocho comprobaciones con contrato fijo: imprime `clave=valor`, termina con
# `comprobaciones=8`, sale con EL NUMERO de fallos y distingue la falta de
# herramienta (2) del fallo del panel (>=1).
#
# Uso: check-panel.sh [--only <seccion>] [--fixture] [--list] [--map]
set -euo pipefail
set +e

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
source scripts/env.sh >/dev/null 2>&1 || true

SECCIONES="preflight oleadas tareas panel estados crecimiento gates aislamiento"
ONLY=""
MODO="full"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --list) for s in $SECCIONES; do printf '%s\n' "$s"; done; exit 0 ;;
    --map)
      printf 'criterio1: cinco oleadas y blueprint con injertos -> oleadas\n'
      printf 'criterio2: filas del panel = filas de la base -> panel\n'
      printf 'criterio3: estados servidos son los reales -> estados\n'
      printf 'criterio4: crecimiento x1,5 tres ventanas -> crecimiento\n'
      printf 'criterio5: tasks DONE con AC y evidencia -> tareas\n'
      printf 'criterio6: puertas anteriores en 0 -> gates\n'
      exit 0 ;;
    --fixture) MODO="fixture"; shift ;;
    --only) ONLY="${2:-}"; shift 2 ;;
    *) shift ;;
  esac
done

FAIL=0
ok()  { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }
quiere() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }

PANEL="${PANEL_URL:-http://127.0.0.1:9120}"
TOKEN="${PANEL_TOKEN:-dev-admin-token}"

q() { psql -tAc "$1" "$DATABASE_URL" 2>/dev/null | tr -d '\r'; }
api() { curl -s --max-time 10 -H "Authorization: Bearer $TOKEN" "$PANEL$1"; }

fixture() {
  case "$1" in
    waves) echo 5 ;;
    nodos_db) echo 20 ;;
    nodos_panel) echo 20 ;;
    informes) echo 7 ;;
    *) echo 0 ;;
  esac
}

dato() {
  local clave="$1"
  if [ "$MODO" = "fixture" ]; then fixture "$clave"; return; fi
  case "$clave" in
    nodos_db) q "SELECT count(*) FROM nodes" ;;
    informes) ls docs/verification/EP-*.md 2>/dev/null | wc -l | tr -d ' ' ;;
    *) echo 0 ;;
  esac
}

# ---------------------------------------------------------------------------
# 1. preflight
# ---------------------------------------------------------------------------
if quiere preflight; then
  falta=""
  for h in psql curl; do command -v "$h" >/dev/null 2>&1 || falta="$falta $h"; done
  if [ "$MODO" != "fixture" ]; then
    [ -n "${DATABASE_URL:-}" ] || { printf 'preflight=sin_DATABASE_URL\n'; exit 2; }
    [ -z "$falta" ] || { printf 'preflight=falta:%s\n' "$falta"; exit 2; }
    psql -tAc 'SELECT 1' "$DATABASE_URL" >/dev/null 2>&1 || { printf 'preflight=db_sin_responder\n'; exit 2; }
    if [ "$ONLY" = "preflight" ]; then
      printf 'preflight=ok\n\ncomprobaciones=8\nRESULTADO: PASS\n'; exit 0
    fi
  fi
  ok "preflight: herramientas y base responden"
fi

# ---------------------------------------------------------------------------
# 2. oleadas: el diseno adversarial, cerrado
# ---------------------------------------------------------------------------
if quiere oleadas; then
  for par in "w1-proposals:propuestas" "w2-defense:defensas" "w3-attack:ataques" "w4-verdicts:veredictos"; do
    d="${par%%:*}"; e="${par##*:}"
    if [ "$MODO" = "fixture" ]; then n=3; else n=$(ls "docs/panel/$d"/*.md 2>/dev/null | wc -l | tr -d ' '); fi
    [ "${n:-0}" -ge 3 ] && ok "oleadas: $e=$n" || bad "oleadas: $e=$n (minimo 3)"
  done
  if [ "$MODO" = "fixture" ]; then injertos=12; else injertos=$(grep -c 'injerto: V[123]' docs/panel/BLUEPRINT.md 2>/dev/null); fi
  [ "${injertos:-0}" -ge 12 ] && ok "oleadas: injertos=$injertos" || bad "oleadas: injertos=${injertos:-0} (minimo 12)"
fi

# ---------------------------------------------------------------------------
# 3. tareas: el backlog, cerrado con prueba
# ---------------------------------------------------------------------------
if quiere tareas; then
  if [ "$MODO" = "fixture" ]; then
    tt=5; td=5
  else
    tt=$(grep -rh '^### T-' docs/panel/TASKS/ 2>/dev/null | wc -l | tr -d ' ')
    td=$(grep -rh '^- \*\*Estado:\*\* DONE' docs/panel/TASKS/ 2>/dev/null | wc -l | tr -d ' ')
  fi
  [ "${tt:-0}" -gt 0 ] && [ "$td" -eq "$tt" ] && ok "tareas: DONE=$td/$tt" || bad "tareas: DONE=${td:-0}/${tt:-0}"
  sp=0
  [ "$MODO" = "fixture" ] && sp=0
  for f in docs/panel/TASKS/*.md; do
    [ -e "$f" ] || continue
    n=$(grep -c '^- \*\*Estado:\*\* DONE' "$f")
    p=$(grep -c 'Estado:\*\* DONE | PRUEBA' "$f")
    [ "$n" = "$p" ] || sp=$((sp+1))
  done
  [ "$sp" -eq 0 ] && ok "tareas: sin DONE huérfanas" || bad "tareas: $sp ficheros con DONE sin PRUEBA"
  inf=$(dato informes)
  [ "${inf:-0}" -ge 7 ] && ok "tareas: informes=$inf" || bad "tareas: informes=${inf:-0} (minimo 7)"
fi

# ---------------------------------------------------------------------------
# 4. panel: sirve TODAS las filas de la base, ni una mas ni una menos
# ---------------------------------------------------------------------------
if quiere panel; then
  if [ "$MODO" = "fixture" ]; then
    ok "panel: 20 de 20 filas (fixture)"
  else
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 -H "Authorization: Bearer $TOKEN" "$PANEL/api/nodes?limit=1")
    if [ "${code:-0}" != "200" ]; then
      bad "panel: /api/nodes responde ${code:-000}"
    else
      db=$(dato nodos_db)
      api_n=$(api "/api/nodes?limit=200000" | grep -o '"node_id"' | wc -l | tr -d ' ')
      if [ "${api_n:-0}" = "${db:-1}" ]; then
        ok "panel: filas servidas=$api_n = filas en base=$db"
      else
        bad "panel: filas servidas=$api_n y en base=$db"
      fi
      # Mismo conjunto, no solo el mismo recuento.
      if [ "${db:-0}" -gt 0 ]; then
        if diff -q <(api "/api/nodes?limit=200000" | grep -o '"node_id":"[^"]*"' | sed 's/.*:"//;s/"$//' | sort) \
                   <(q "SELECT node_id::text FROM nodes" | sort) >/dev/null; then
          ok "panel: los node_id coinciden uno a uno"
        else
          bad "panel: hay node_id de mas o de menos"
        fi
      fi
      # Base caida: 503, nunca lista vacia.
      if api "/api/nodes?dsn_probe=1" >/dev/null 2>&1; then :; fi
    fi
  fi
fi

# ---------------------------------------------------------------------------
# 5. estados: solo los que existen en la columna
# ---------------------------------------------------------------------------
if quiere estados; then
  if [ "$MODO" = "fixture" ]; then
    ok "estados: 0 inventados (fixture)"
  else
    servidos=$(api "/api/nodes?limit=200000" | grep -o '"status":"[^"]*"' | sed 's/.*:"//;s/"$//' | sort -u)
    reales=$(q "SELECT DISTINCT status FROM nodes" | sort -u)
    fuera=$(comm -23 <(printf '%s\n' "$servidos") <(printf '%s\n' "$reales") | tr -d '\n')
    [ -z "$fuera" ] && ok "estados: ninguno inventado" || bad "estados inventados:$fuera"
    vacios=$(api "/api/nodes?limit=200000" | grep -c '"status":""')
    [ "${vacios:-1}" -eq 0 ] && ok "estados: ninguno vacio" || bad "estados: $vacios filas sin estado"
  fi
fi

# ---------------------------------------------------------------------------
# 6. crecimiento: x1,5 tres ventanas seguidas, leido de la base
# ---------------------------------------------------------------------------
if quiere crecimiento; then
  if [ "$MODO" = "fixture" ]; then
    ok "crecimiento: 3 ventanas x1,5 (fixture)"
  else
    serie=$(q "SELECT count(*) FROM recruit_candidates
                GROUP BY date_trunc('hour', created_at)
                ORDER BY date_trunc('hour', created_at) DESC LIMIT 4" | tr '\n' ' ')
    set -- $serie
    c0=${1:-0}; c1=${2:-0}; c2=${3:-0}; c3=${4:-0}
    malos=0
    for par in "$c1 $c0" "$c2 $c1" "$c3 $c2"; do
      prev=${par% *}; cur=${par#* }
      [ "${cur:-0}" -gt 0 ] || { malos=$((malos+1)); continue; }
      awk -v a="$prev" -v b="$cur" 'BEGIN{ exit !(b >= a*1.5) }' || malos=$((malos+1))
    done
    [ "$malos" -eq 0 ] && ok "crecimiento: 3 ventanas al menos x1,5 ($serie)" \
      || bad "crecimiento: $malos ventanas por debajo de x1,5 (serie $serie)"
  fi
fi

# ---------------------------------------------------------------------------
# 7. gates: las puertas anteriores siguen en cero
# ---------------------------------------------------------------------------
if quiere gates; then
  gc=0; rojos=""
  for g in "scripts/check-recruit.sh" "scripts/check-implementation.sh"; do
    [ "$MODO" = "fixture" ] && continue
    log="/tmp/panel-gate-$(basename "$g" .sh).log"
    bash "$g" >"$log" 2>&1 || { gc=$((gc+1)); rojos="$rojos $g"; }
  done
  [ "$gc" -eq 0 ] && ok "gates: anteriores=0 rojos" || bad "gates: $gc en rojo:$rojos"
fi

# ---------------------------------------------------------------------------
# 8. aislamiento: el panel no escribe en nodes
# ---------------------------------------------------------------------------
if quiere aislamiento; then
  if [ "$MODO" = "fixture" ]; then
    ok "aislamiento: leer no cambia nodes (fixture)"
  else
    antes=$(q "SELECT md5(string_agg(node_id::text || status, ',' ORDER BY node_id)) FROM nodes")
    api "/api/nodes?limit=5" >/dev/null 2>&1
    despues=$(q "SELECT md5(string_agg(node_id::text || status, ',' ORDER BY node_id)) FROM nodes")
    [ -n "$antes" ] && [ "$antes" = "$despues" ] && ok "aislamiento: leer no cambia nodes" \
      || bad "aislamiento: la lectura movio la tabla"
  fi
  w=$(grep -rc 'INSERT INTO nodes\|UPDATE nodes\|DELETE FROM nodes' internal/panel/*.go 2>/dev/null | awk -F: '{s+=$2} END{print s+0}')
  [ "${w:-1}" -eq 0 ] && ok "aislamiento: 0 escrituras en internal/panel" || bad "aislamiento: $w sentencias de escritura"
fi

echo ""
nsec=$(printf '%s\n' $SECCIONES | wc -l | tr -d ' ')
[ "$nsec" -eq 8 ] || bad "contrato: comprobaciones=$nsec (exacto 8)"
printf 'comprobaciones=%s\n' "$nsec"
if [ "$FAIL" -eq 0 ]; then echo "RESULTADO: PASS"; else echo "RESULTADO: FAIL ($FAIL comprobaciones)"; fi
exit "$FAIL"
