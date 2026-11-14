#!/usr/bin/env bash
# reap — retira residuos de toolbox del host de desarrollo.
#
# Los tests y los agentes dejan binarios vivos (botd, dobles, qemu, motores
# portables). Acumulados durante una sesión larga agotan el escritorio de
# Windows y el compilador empieza a fallar con 0xc0000142. Este script solo
# mata imágenes conocidas: nunca procesos del usuario ajenos al proyecto.
#
# Uso: reap.sh [--dry-run]
set -uo pipefail

DRY=0
[ "${1:-}" = "--dry-run" ] && DRY=1

IMAGES='botd\.exe|botnet-api\.exe|botnet-gw\.exe|botnet-rdv\.exe|botnet-discover\.exe|botnet-strike\.exe|botnet-build\.exe|botctl\.exe|stub_api\.exe|apistub\.exe|probe-.*\.exe|qemu-(mips|mipsel|arm|aarch64|x86_64)\.exe|redis-server\.exe|nats-server\.exe'

mapfile -t pids < <(tasklist //FO CSV 2>/dev/null | awk -F'","' 'NR>1{print $1"\t"$2}' | tr -d '"' \
  | grep -E "$IMAGES" | awk '{print $2}' | sort -un)

echo "reap: ${#pids[@]} procesos de toolbox vivos"
if [ "$DRY" = "1" ]; then
  tasklist //FO CSV 2>/dev/null | awk -F'","' 'NR>1{print $1}' | tr -d '"' | grep -E "$IMAGES" | sort | uniq -c
  exit 0
fi
for pid in "${pids[@]:-}"; do
  [ -n "$pid" ] && taskkill //F //PID "$pid" >/dev/null 2>&1 || true
done
echo "reap: retirados ${#pids[@]}"

# Barrido de consolas y shells huerfanos: los agentes que terminan dejan
# powershell/OpenConsole sin padre vivo y cada uno consume heap de
# escritorio, que es lo que hace fallar a compile.exe con 0xc0000142.
if [ "${2:-}" = "--orphans" ]; then
  n=$(powershell -NoProfile -Command "
    \$vivos=@{}; Get-Process | ForEach-Object { \$vivos[\$_.Id]=1 }
    \$o = Get-Process powershell,OpenConsole,pwsh -ErrorAction SilentlyContinue |
         Where-Object { -not \$vivos.ContainsKey(\$_.Parent.Id) }
    \$o | ForEach-Object { Stop-Process -Id \$_.Id -Force -ErrorAction SilentlyContinue; \$_.Id } |
      Measure-Object | Select-Object -ExpandProperty Count" 2>/dev/null | tr -d '
')
  echo "reap: huerfanos retirados ${n:-0}"
fi
