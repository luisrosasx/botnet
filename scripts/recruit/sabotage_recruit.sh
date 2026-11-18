#!/usr/bin/env bash
# Sabotajes del paraguas (T-2105 AC2): cada uno rompe UNA cosa, la puerta
# tiene que morder, y se revierte. Si alguno no muerde, hay un falso verde y
# el script lo dice.
#
# Uso: sabotage_recruit.sh [--only <nombre>]
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
set -a
# shellcheck source=/dev/null
[ -f /etc/botnet-recruit/env ] && . /etc/botnet-recruit/env
set +a
: "${DATABASE_URL:?DATABASE_URL requerido}"

ONLY="${2:-}"
PUERTA="bash scripts/check-recruit.sh"
q() { psql -tAc "$1" "$DATABASE_URL" 2>/dev/null | tr -d '\r'; }
TOTAL=0
FALLAN=0

# probar <nombre> <seccion> [esperado]: el sabotaje YA esta aplicado.
probar() {
  local nombre="$1" seccion="$2" esperado="${3:-}"
  TOTAL=$((TOTAL+1))
  limite=$(mktemp)
  $PUERTA --only "$seccion" >"$limite" 2>&1
  rc=$?
  if [ -n "$esperado" ]; then
    # Modo "no debe moverse": el sabotaje tiene que dejar el numero igual.
    if grep -q "$esperado" "$limite" && [ "$rc" -eq 0 ]; then
      printf 'ok   %-32s %s se mantiene en %s\n' "$nombre" "$seccion" "$esperado"
    else
      printf 'FAIL %-32s %s cambio o salio con rojo (rc=%d)\n' "$nombre" "$seccion" "$rc"
      FALLAN=$((FALLAN+1))
    fi
  else
    if [ "$rc" -ne 0 ]; then
      printf 'ok   %-32s muerde (rc=%d)\n' "$nombre" "$rc"
    else
      printf 'FAIL %-32s no muerde: es un falso verde\n' "$nombre"
      FALLAN=$((FALLAN+1))
    fi
  fi
  rm -f "$limite"
}

quiere() { [ -z "$ONLY" ] || [ "$ONLY" = "$1" ]; }

# 1. Nodo fantasma: tres aparatos caen y sus verificados siguen contando.
#    El CHECK `recruit_verified_requires_node` no deja poner node_id a NULL en
#    estado verified, asi que el aparato se cae por estado, no por enlace.
if quiere nodo_fantasma; then
  ids=$(q "SELECT string_agg(quote_literal(node_id::text), ',') FROM (
             SELECT DISTINCT node_id FROM recruit_candidates
              WHERE state='verified' AND node_id IS NOT NULL LIMIT 3) x")
  q "UPDATE nodes SET status='down' WHERE node_id IN ($ids)" >/dev/null
  probar nodo_fantasma nodos
  q "UPDATE nodes SET status='enrolled' WHERE node_id IN ($ids)" >/dev/null
fi

# 2. Cuenta por intentos: cinco ganadores del mismo candidato no son cinco
#    aparatos. El numero de nodos tiene que quedarse igual.
if quiere cuenta_por_intentos; then
  cid=$(q "SELECT candidate_id FROM recruit_socks5_checks WHERE ok ORDER BY check_id LIMIT 1")
  q "ALTER TABLE recruit_socks5_checks DROP CONSTRAINT IF EXISTS recruit_socks5_one_win_tmp" >/dev/null
  q "DROP INDEX IF EXISTS recruit_socks5_one_win" >/dev/null
  for i in 2 3 4 5; do
    q "INSERT INTO recruit_socks5_checks
         (candidate_id, node_id, target, http_status, bytes_in, latency_ms, egress_ip, ip_match, ok, attempt)
       SELECT candidate_id, node_id, target, 200, 4096, 120, egress_ip, true, true, $i
         FROM recruit_socks5_checks WHERE candidate_id=$cid AND ok LIMIT 1" >/dev/null
  done
  # Se compara la misma cifra antes y despues: si sube, se estan contando
  # intentos y no aparatos.
  antes=$(bash scripts/check-recruit.sh --only nodos | grep -oE 'con_evidencia_dura=[0-9]+' | head -1)
  TOTAL=$((TOTAL+1))
  bash scripts/check-recruit.sh --only nodos >/dev/null 2>&1
  rc=$?
  despues=$(bash scripts/check-recruit.sh --only nodos | grep -oE 'con_evidencia_dura=[0-9]+' | head -1)
  # El rojo de la seccion puede venir de otra comprobacion: aqui solo se
  # juzga si la cifra se mueve.
  if [ "$antes" = "$despues" ] && [ -n "$antes" ]; then
    printf 'ok   %-32s el conteo no se mueve (%s)
' cuenta_por_intentos "$antes"
  else
    printf 'FAIL %-32s el conteo cambio: %s -> %s (rc=%d)
' cuenta_por_intentos "${antes:-?}" "${despues:-?}" "$rc"
    FALLAN=$((FALLAN+1))
  fi
  q "DELETE FROM recruit_socks5_checks WHERE candidate_id=$cid AND attempt>1" >/dev/null
  q "CREATE UNIQUE INDEX IF NOT EXISTS recruit_socks5_one_win
       ON recruit_socks5_checks (candidate_id) WHERE ok" >/dev/null
fi

# 3. SOCKS5 sin flota: nadie sale por su propia IP.
if quiere socks5_sin_flota; then
  q "UPDATE recruit_socks5_checks SET egress_ip='0.0.0.0'" >/dev/null
  probar socks5_sin_flota socks5
  q "UPDATE recruit_socks5_checks s SET egress_ip = n.public_ip
        FROM nodes n WHERE n.node_id = s.node_id" >/dev/null
fi

# 4. Puerta vaciada: una fila PASS menos y el gate de campo se cae.
if quiere puerta_vaciada; then
  cp docs/verification/EP-15.md /tmp/ep15.sabotaje.md
  grep -v '| PASS |' docs/verification/EP-15.md > /tmp/ep15.nuevo.md
  mv /tmp/ep15.nuevo.md docs/verification/EP-15.md
  probar puerta_vaciada gates
  mv /tmp/ep15.sabotaje.md docs/verification/EP-15.md
fi

# 5. psql fuera del PATH: el paraguas distingue "falta herramienta" (2).
if quiere sin_psql; then
  mkdir -p /tmp/sin_psql
  env PATH=/tmp/sin_psql "$(command -v bash)" scripts/check-recruit.sh --only preflight >/dev/null 2>&1
  rc=$?
  TOTAL=$((TOTAL+1))
  if [ "$rc" -eq 2 ]; then
    printf 'ok   %-32s sale 2, no 1\n' sin_psql
  else
    printf 'FAIL %-32s salio %d (esperado 2)\n' sin_psql "$rc"
    FALLAN=$((FALLAN+1))
  fi
fi

# 6. Servicio con instancias paradas: el healthcheck no basta.
if quiere servicio_parado; then
  systemctl stop 'botnet-recruit-worker@banner' >/dev/null 2>&1
  sleep 2
  probar servicio_parado servicio
  systemctl start 'botnet-recruit-worker@banner' >/dev/null 2>&1
  sleep 2
fi

printf 'sabotajes=%d fallan=%d\n' "$TOTAL" "$FALLAN"
[ "$FALLAN" -eq 0 ]
