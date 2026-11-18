#!/usr/bin/env bash
# Sonda de reputacion del origen (T-2006). Comprueba las dos listas negras que
# el blueprint vigila, guarda el resultado y, si el origen esta listado, pausa
# el reclutamiento 48 h: escanear desde una IP en lista negra no recluta.
#
# Uso: rbl_probe.sh [--ip 74.208.94.91] [--no-pause]
# Salida: 0 limpio, 1 listado (y pausado), 2 error de uso o de herramienta.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
set -a
# shellcheck source=/dev/null
[ -f /etc/botnet-recruit/env ] && . /etc/botnet-recruit/env
set +a

IP="${RECRUIT_SOURCE_IP:-74.208.94.91}"
PAUSAR=1
while [ "$#" -gt 0 ]; do
  case "$1" in
    --ip) IP="${2:-}"; shift 2 ;;
    --no-pause) PAUSAR=0; shift ;;
    *) printf 'uso: rbl_probe.sh [--ip <ipv4>] [--no-pause]\n' >&2; exit 2 ;;
  esac
done

for h in dig psql; do
  command -v "$h" >/dev/null 2>&1 || { printf 'rbl_probe: falta %s\n' "$h" >&2; exit 2; }
done
: "${DATABASE_URL:?DATABASE_URL requerido}"

# Octetos al reves: 74.208.94.91 -> 91.94.208.74
REV=$(printf '%s' "$IP" | awk -F. '{print $4"."$3"."$2"."$1}')
LISTAS="zen.spamhaus.org bl.spamcop.net"

listado=0
for d in $LISTAS; do
  resp=$(dig +short +time=3 +tries=2 "${REV}.${d}" A 2>/dev/null | tr -d '\r')
  if [ -n "$resp" ]; then
    printf 'listado: %s -> %s\n' "$d" "$(printf '%s' "$resp" | tr '\n' ' ')"
    listado=1
  else
    printf 'limpio:  %s\n' "$d"
  fi
done

PTR=$(dig +short -x "$IP" 2>/dev/null | tr -d '\r' | head -1)
ABUSE=""
if [ -n "$PTR" ]; then
  dominio=$(printf '%s' "$PTR" | sed 's/\.$//' | awk -F. '{n=NF; if (n < 2) print $1; else printf "%s.%s", $(n-1), $n}')
  ABUSE="abuse@$dominio"
fi

if [ "$PAUSAR" -eq 1 ] && [ "$listado" -eq 1 ]; then
  psql -tAc "
    INSERT INTO recruit_source_ip (ip, ptr, abuse_contact, rbl_listed, rbl_checked_at, paused_until)
    VALUES ('$IP', NULLIF('$PTR',''), NULLIF('$ABUSE',''), true, now(), now() + interval '48 hours')
    ON CONFLICT (ip) DO UPDATE
      SET rbl_listed = true, rbl_checked_at = now(),
          ptr = COALESCE(NULLIF(EXCLUDED.ptr,''), recruit_source_ip.ptr),
          abuse_contact = COALESCE(NULLIF(EXCLUDED.abuse_contact,''), recruit_source_ip.abuse_contact),
          paused_until = now() + interval '48 hours', updated_at = now()" "$DATABASE_URL" >/dev/null
  psql -tAc "UPDATE recruit_control SET paused = true, updated_at = now() WHERE id = 1" "$DATABASE_URL" >/dev/null
  printf 'pausa: 48 h (origen listado)\n'
  exit 1
fi

psql -tAc "
  INSERT INTO recruit_source_ip (ip, ptr, abuse_contact, rbl_listed, rbl_checked_at)
  VALUES ('$IP', NULLIF('$PTR',''), NULLIF('$ABUSE',''), $([ "$listado" -eq 1 ] && echo true || echo false), now())
  ON CONFLICT (ip) DO UPDATE
    SET rbl_listed = EXCLUDED.rbl_listed, rbl_checked_at = now(),
        ptr = COALESCE(NULLIF(EXCLUDED.ptr,''), recruit_source_ip.ptr),
        abuse_contact = COALESCE(NULLIF(EXCLUDED.abuse_contact,''), recruit_source_ip.abuse_contact),
        updated_at = now()" "$DATABASE_URL" >/dev/null
printf 'origin=%s ptr=%s abuse=%s listado=%s\n' "$IP" "${PTR:--}" "${ABUSE:--}" "$listado"
[ "$listado" -eq 0 ]
