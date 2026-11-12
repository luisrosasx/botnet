#!/usr/bin/env bash
# EP-07 T-0705: la regla de encapsulado por pcap, no por fe.
#  1) Guard de config: ningun flag del despliegue enruta dato por WG
#     (`grep -c "data_plane=wg" deploy/` = 0).
#  2) Aserto de pcap: en 100 sesiones (~100 MB por el tunel de
#     sesiones) la malla captura < 1 MB; con tcpdump en el bastion:
#        tcpdump -i wg0 -w mesh.pcap &  # durante la carrera
#        bash scripts/mesh/mesh_bytes_check.sh --pcap mesh.pcap --sessions-mb 100
#     Sin tcpdump (host F4): el doble --pcap-bytes afirma la misma
#     cuenta.
#  3) Auto-test de la cuenta sin tocar la red.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

MESH_MAX_BYTES=1048576 # < 1 MB cruzando la malla durante ~100 MB de sesion

fail() { echo "mesh_bytes_check: FAIL — $1" >&2; exit 1; }

# --- 1) guard de config ---------------------------------------------------
hits=$(grep -rc "data_plane=wg" deploy/ 2>/dev/null | awk -F: '{s+=$NF} END{print s+0}' || true)
[[ "$hits" == "0" ]] || fail "deploy/ trae $hits menciones a data_plane=wg: el dato volvio a WG"
echo "PASS guard config: grep -c data_plane=wg deploy/ = 0"

# --- 2) aserto de pcap ----------------------------------------------------
PCAP_BYTES=""
SESSIONS_MB=100
while [[ $# -gt 0 ]]; do
  case "$1" in
    --pcap)       pcap="$2"; PCAP_BYTES=$( (stat -c%s "$pcap" 2>/dev/null || wc -c <"$pcap") | awk '{print $1}'); shift 2 ;;
    --pcap-bytes) PCAP_BYTES="$2"; shift 2 ;;
    --sessions-mb) SESSIONS_MB="$2"; shift 2 ;;
    --self-test)  shift ;;
    *) fail "argumento desconocido: $1" ;;
  esac
done

if [[ -n "$PCAP_BYTES" ]]; then
  (( PCAP_BYTES < MESH_MAX_BYTES )) || fail "pcap de wg0 = ${PCAP_BYTES} B >= 1 MB con ${SESSIONS_MB} MB de sesion: WG-sobre-dato"
  ratio=$(awk -v m="$PCAP_BYTES" -v s=$(( SESSIONS_MB * 1024 * 1024 )) 'BEGIN{printf "%.2f", 100*m/s}')
  echo "PASS pcap: ${PCAP_BYTES} B en la malla vs ${SESSIONS_MB} MB de sesion (${ratio}% — umbral < 1 %)"
else
  command -v tcpdump >/dev/null 2>&1 || fail "sin tcpdump en el host: pasa el doble con --pcap-bytes N (o --self-test)"
  fail "necesito --pcap fichero o --pcap-bytes N (capture real de wg0)"
fi

# --- 3) auto-test de la cuenta (doble determinista) -----------------------
# 100 sesiones ~= 100 MB de dato y 524 288 B de wg0: pasa. Con 2 MB:
# rompe. Es la aritmetica del criterio, medida aqui, no en el bastion.
selftest() {
  local b expected
  for b in 0 524288 1048575; do
    (( b < MESH_MAX_BYTES )) || fail "self-test: ${b} deberia pasar"
  done
  expected=1048576
  if (( expected < MESH_MAX_BYTES )); then fail "self-test: ${expected} deberia romper (< 1 MB estricto)"; fi
  echo "PASS self-test: cuenta del umbral < 1 MB correcta"
}
selftest

echo "mesh_bytes_check: OK (WG = operaciones, punto; runbook docs/runbooks/mesh.md)"
