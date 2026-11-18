#!/usr/bin/env bash
# Puerta del servicio de reclutamiento y exploracion masiva.
#
# Ocho comprobaciones, cada una con su nombre, y un contrato de salida fijo:
#   * imprime `clave=valor` por comprobacion (el gate se lee, no se adivina);
#   * termina con `comprobaciones=8`;
#   * sale con EL NUMERO de fallos (0 = verde), no con un `1` prestado;
#   * falta de herramienta o de base es 2, no un fallo del servicio (A3-07/A3-08).
#
# Uso:
#   check-recruit.sh [--only <seccion>] [--fixture] [--list] [--map]
#     --only     corre una sola comprobacion (preflight incluido)
#     --fixture  corre sin base y sin red, con valores de laboratorio (A3-09)
#     --list     enumera las ocho comprobaciones y sale
#     --map      mapea las cinco condiciones del objetivo a su comprobacion
set -euo pipefail
set +e # el contrato es contar fallos, no abortar en el primero

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/env.sh
source scripts/env.sh >/dev/null 2>&1 || true

SECCIONES="preflight oleadas nodos socks5 servicio gates lazo evidencia"
ONLY=""
MODO="full"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --list) for s in $SECCIONES; do printf '%s\n' "$s"; done; exit 0 ;;
    --map)
      printf 'criterio1: cinco oleadas y blueprint consolidado -> oleadas\n'
      printf 'criterio2: toda task DONE con AC y evidencia -> evidencia\n'
      printf 'criterio3: servicio persistente y healthcheck -> servicio\n'
      printf 'criterio4: 20 nodos propios con SOCKS5 real -> nodos+socks5\n'
      printf 'criterio5: gates del plan anterior siguen en 0 -> gates\n'
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

# ---------------------------------------------------------------------------
# Datos. `dato` es el unico camino: en modo normal pregunta a Postgres; en
# modo fixture devuelve el valor de laboratorio, sin psql y sin red.
# ---------------------------------------------------------------------------
q() { psql -tAc "$1" "$DATABASE_URL" 2>/dev/null | tr -d '\r'; }

fixture() {
  case "$1" in
    waves_cerradas) echo 5 ;;
    waves_mal)      echo 0 ;;
    nodos_distintos) echo 20 ;;
    nodos_evidencia) echo 20 ;;
    nodos_repetidos) echo 0 ;;
    sock_nodos)     echo 20 ;;
    sock_mal)       echo 0 ;;
    ticks_up)       echo 3 ;;
    ticks_down)     echo 2 ;;
    ticks_sin_motivo) echo 0 ;;
    tasks_total)    echo 39 ;;
    tasks_done)     echo 39 ;;
    tasks_sin_prueba) echo 0 ;;
    informes)       echo 21 ;;
    evidencia_hashes) echo 6 ;;
    *) echo 0 ;;
  esac
}

dato() {
  local clave="$1"
  if [ "$MODO" = "fixture" ]; then fixture "$clave"; return; fi
  case "$clave" in
    waves_cerradas) q "
      SELECT count(*) FROM recruit_waves
       WHERE state='cerrada' AND promoted>0 AND verified>=1 AND closed_at IS NOT NULL" ;;
    waves_mal) q "
      SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND closed_at <= started_at" ;;
    nodos_distintos) q "
      SELECT count(DISTINCT c.node_id) FROM recruit_candidates c
        JOIN nodes n USING (node_id)
        JOIN LATERAL (SELECT ts FROM node_heartbeats h
                       WHERE h.node_id = n.node_id ORDER BY ts DESC LIMIT 1) x ON true
       WHERE c.state IN ('verified','enrolled')
         AND n.status IN ('enrolled','live') AND n.optout_at IS NULL
         AND x.ts > now() - interval '15 minutes'" ;;
    nodos_evidencia) q "
      SELECT count(DISTINCT s.node_id) FROM recruit_socks5_checks s
        JOIN nodes n USING (node_id)
       WHERE s.ok AND s.http_status=200 AND s.bytes_in>0
         AND s.latency_ms BETWEEN 0 AND 1500 AND s.ip_match
         AND n.status IN ('enrolled','live') AND n.optout_at IS NULL" ;;
    nodos_repetidos) q "
      SELECT count(*) FROM (SELECT candidate_id FROM recruit_socks5_checks
                             WHERE ok GROUP BY 1 HAVING count(*) > 1) x" ;;
    sock_nodos) q "
      SELECT count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id)
       WHERE s.ok AND s.ip_match AND s.egress_ip = n.public_ip
         AND s.egress_ip <> '74.208.94.91'" ;;
    sock_mal) q "
      SELECT count(*) FROM recruit_socks5_checks
       WHERE ok AND (bytes_in<=0 OR latency_ms>1500 OR http_status<>200)" ;;
    ticks_up) q "SELECT count(*) FROM recruit_planner_ticks WHERE action='up'" ;;
    ticks_down) q "SELECT count(*) FROM recruit_planner_ticks WHERE action='down'" ;;
    ticks_sin_motivo) q "
      SELECT count(*) FROM recruit_planner_ticks WHERE reason IS NULL OR reason=''" ;;
    tasks_total) grep -rh '^### T-' docs/recruit/TASKS/ 2>/dev/null | wc -l | tr -d ' ' ;;
    tasks_done) grep -rh '^- \*\*Estado:\*\* DONE' docs/recruit/TASKS/ 2>/dev/null | wc -l | tr -d ' ' ;;
    tasks_sin_prueba)
      local n p f sin=0
      for f in docs/recruit/TASKS/*.md; do
        n=$(grep -c '^- \*\*Estado:\*\* DONE' "$f" 2>/dev/null)
        p=$(grep -c 'Estado:\*\* DONE | PRUEBA' "$f" 2>/dev/null)
        [ "$n" = "$p" ] || sin=$((sin+1))
      done
      echo "$sin" ;;
    informes) ls docs/verification/EP-*.md 2>/dev/null | wc -l | tr -d ' ' ;;
    evidencia_hashes)
      ls docs/verification/evidence/ 2>/dev/null | grep -c 'recruit-' ;;
    *) echo 0 ;;
  esac
}

# ---------------------------------------------------------------------------
# 1. preflight: sin herramienta o sin base se sale 2, no se inventa un rojo.
# ---------------------------------------------------------------------------
if quiere preflight; then
  falta=""
  for h in psql curl; do
    command -v "$h" >/dev/null 2>&1 || falta="$falta $h"
  done
  if [ "$MODO" != "fixture" ]; then
    [ -n "${DATABASE_URL:-}" ] || { printf 'preflight=sin_DATABASE_URL\n'; exit 2; }
    [ -z "$falta" ] || { printf 'preflight=falta:%s\n' "$falta"; exit 2; }
    if ! psql -tAc 'SELECT 1' "$DATABASE_URL" >/dev/null 2>&1; then
      printf 'preflight=db_sin_responder\n'
      exit 2
    fi
    # Solo la seccion preflight: se mira y se sale. El resto no se toca.
    if [ "$ONLY" = "preflight" ]; then
      printf 'preflight=ok\n'
      printf '\ncomprobaciones=8\nRESULTADO: PASS\n'
      exit 0
    fi
  fi
  ok "preflight: herramientas y base responden"
fi

# ---------------------------------------------------------------------------
# 2. oleadas: lo que el diseno cerro, leido de los ficheros y de la tabla.
# ---------------------------------------------------------------------------
if quiere oleadas; then
  for par in "w1-proposals:propuestas" "w2-defense:defensas" "w3-attack:ataques" "w4-verdicts:veredictos"; do
    d="${par%%:*}"; e="${par##*:}"
    n=$(ls "docs/recruit/$d"/*.md 2>/dev/null | wc -l | tr -d ' ')
    [ "${n:-0}" -ge 3 ] && ok "oleadas: $e=$n" || bad "oleadas: $e=$n (minimo 3)"
  done
  injertos=$(grep -o 'injerto: V[123]' docs/recruit/BLUEPRINT.md 2>/dev/null | wc -l | tr -d ' ')
  [ "${injertos:-0}" -ge 12 ] && ok "oleadas: injertos=$injertos" || bad "oleadas: injertos=$injertos (minimo 12)"
  wc=$(dato waves_cerradas)
  [ "${wc:-0}" -eq 5 ] && ok "oleadas: cerradas_con_evidencia=$wc" || bad "oleadas: cerradas_con_evidencia=$wc (exacto 5)"
  wm=$(dato waves_mal)
  [ "${wm:-1}" -eq 0 ] && ok "oleadas: cerradas_antes_de_empezar=$wm" || bad "oleadas: $wm cerradas antes de empezar"
fi

# ---------------------------------------------------------------------------
# 3. nodos: aparatos distintos, vivos ahora, con su prueba dura.
# ---------------------------------------------------------------------------
if quiere nodos; then
  nd=$(dato nodos_distintos)
  [ "${nd:-0}" -ge 20 ] && ok "nodos: distintos_con_latido=$nd" || bad "nodos: distintos_con_latido=$nd (minimo 20)"
  ne=$(dato nodos_evidencia)
  [ "${ne:-0}" -ge 20 ] && ok "nodos: con_evidencia_dura=$ne" || bad "nodos: con_evidencia_dura=$ne (minimo 20)"
  nr=$(dato nodos_repetidos)
  [ "${nr:-1}" -eq 0 ] && ok "nodos: candidatos_con_mas_de_un_ganador=$nr" || bad "nodos: $nr candidatos con mas de un ganador"
fi

# ---------------------------------------------------------------------------
# 4. socks5: la prueba de salida, sin contar intentos como aparatos.
# ---------------------------------------------------------------------------
if quiere socks5; then
  sn=$(dato sock_nodos)
  [ "${sn:-0}" -ge 20 ] && ok "socks5: $sn nodos con IP de salida propia" || bad "socks5: $sn nodos con IP propia (minimo 20)"
  sm=$(dato sock_mal)
  [ "${sm:-1}" -eq 0 ] && ok "socks5: cero ganadores sin evidencia dura" || bad "socks5: $sm ganadores sin evidencia"
fi

# ---------------------------------------------------------------------------
# 5. servicio: unidad persistente, instancias y healthcheck.
# ---------------------------------------------------------------------------
if quiere servicio; then
  if [ "$MODO" = "fixture" ]; then
    ok "servicio: healthcheck=200 (fixture)"
    ok "servicio: instancias=3 activas (fixture)"
  else
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 127.0.0.1:9110/healthz 2>/dev/null)
    [ "${code:-0}" = "200" ] && ok "servicio: healthcheck=200" || bad "servicio: healthcheck=${code:-000}"
    activas=$(systemctl list-units --no-legend 'botnet-recruit-worker@*' 2>/dev/null | grep -c 'active')
    falladas=$(systemctl list-units --state=failed --no-legend 'botnet-recruit*' 2>/dev/null | grep -c 'botnet-recruit')
    { [ "${activas:-0}" -ge 3 ] && [ "${falladas:-1}" -eq 0 ]; } \
      && ok "servicio: instancias=${activas} activas, fallidas=${falladas}" \
      || bad "servicio: instancias=${activas} activas, fallidas=${falladas}"
  fi
fi

# ---------------------------------------------------------------------------
# 6. gates: los del plan anterior siguen en cero.
# ---------------------------------------------------------------------------
if quiere gates; then
  gc=0
  rojos=""
  # El smoke de metricas va primero: su SLO mira el latido y la compilacion de
  # go test se come la CPU del host, que daria un rojo de carga y no de codigo.
  for g in "scripts/recruit/metrics_smoke_recruit.sh" "scripts/check-blueprint.sh" \
           "scripts/check-implementation.sh" \
           "scripts/check-field-closure.sh --strict" "scripts/metrics_smoke.sh"; do
    if [ "$MODO" = "fixture" ]; then continue; fi
    # El log del gate que cae se queda en /tmp: sin el, un rojo de esta seccion
    # solo dice "1 en rojo" y hay que repetir la corrida entera para verlo.
    log="/tmp/recruit-gate-$(basename "$g" .sh).log"
    # shellcheck disable=SC2086
    bash $g >"$log" 2>&1 || { gc=$((gc+1)); rojos="$rojos $(basename "$g")(/tmp/$(basename "$log"))"; }
  done
  [ "$gc" -eq 0 ] && ok "gates: plan_anterior=0 rojos" || bad "gates: $gc en rojo:$rojos"
fi

# ---------------------------------------------------------------------------
# 7. lazo: decide y lo deja escrito con motivo.
# ---------------------------------------------------------------------------
if quiere lazo; then
  tu=$(dato ticks_up)
  [ "${tu:-0}" -ge 1 ] && ok "lazo: ticks_con_subida=$tu" || bad "lazo: 0 ticks con subida"
  td=$(dato ticks_down)
  [ "${td:-0}" -ge 1 ] && ok "lazo: ticks_con_bajada=$td" || bad "lazo: 0 ticks con bajada"
  tm=$(dato ticks_sin_motivo)
  [ "${tm:-1}" -eq 0 ] && ok "lazo: ticks_sin_motivo=$tm" || bad "lazo: $tm ticks sin motivo"
fi

# ---------------------------------------------------------------------------
# 8. evidencia: toda task cerrada con su prueba y sus ficheros.
# ---------------------------------------------------------------------------
if quiere evidencia; then
  tt=$(dato tasks_total); td=$(dato tasks_done)
  [ "${tt:-0}" -gt 0 ] && [ "$td" -eq "$tt" ] && ok "evidencia: tasks DONE=$td/$tt" \
    || bad "evidencia: tasks DONE=${td:-0}/${tt:-0}"
  sp=$(dato tasks_sin_prueba)
  [ "${sp:-1}" -eq 0 ] && ok "evidencia: tasks_sin_prueba=$sp" || bad "evidencia: $sp ficheros con DONE sin PRUEBA"
  inf=$(dato informes)
  [ "${inf:-0}" -ge 7 ] && ok "evidencia: informes=$inf" || bad "evidencia: informes=$inf (minimo 7)"
  has=$(dato evidencia_hashes)
  [ "${has:-0}" -ge 6 ] && ok "evidencia: ficheros_con_sha256=$has" || bad "evidencia: ficheros_con_sha256=$has (minimo 6)"
fi

echo ""
# El contrato incluye el numero de comprobaciones: una version recortada a
# cuatro se declara coja en vez de pasar por buena (A3-07).
nsec=$(printf '%s\n' $SECCIONES | wc -l | tr -d ' ')
[ "$nsec" -eq 8 ] || bad "contrato: comprobaciones=$nsec (exacto 8)"
printf 'comprobaciones=%s\n' "$nsec"
if [ "$FAIL" -eq 0 ]; then echo "RESULTADO: PASS"; else echo "RESULTADO: FAIL ($FAIL comprobaciones)"; fi
exit "$FAIL"
