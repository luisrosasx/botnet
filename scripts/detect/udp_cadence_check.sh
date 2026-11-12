#!/usr/bin/env bash
# udp_cadence_check.sh — regla anti-perfil-C2 del UDP propio (T-1303 AC3):
# exit 0 solo si el UDP del agente (no-DNS) esta ausente o todos los
# huecos entre sus paquetes superan --min-gap (300 s por defecto). Un
# beacon UDP de 25-45 s a destino fijo — el "keepalive WG/bottunnel" del
# perfil C2 clasico — da exit 1.
#
# Fixture: pcap Ethernet/IPv4/UDP (DLT_EN10MB, microsegundos LE) generado
# con scripts/detect/gen_udp_fixtures.go, o .txt con un epoch (s) por
# linea. Si tcpdump esta instalado se usa (-tt + filtro 'udp and port not
# 53'); si no, el parser propio lee el pcap con od+awk (aqui no hay
# tcpdump: el parser es la ruta real en CI Git Bash).
#
# Uso:
#   scripts/detect/udp_cadence_check.sh --fixture=testdata/detect/udp_quiet.pcap [--min-gap=300]
# Exit: 0 PASS (sin cadencia propia); 1 FAIL (cadencia <= min-gap); 2 uso.
set -euo pipefail

die() { echo "udp_cadence_check: $*" >&2; exit 2; }

FIXTURE=""
MIN_GAP=300

while [ $# -gt 0 ]; do
  case "$1" in
    --fixture) shift; FIXTURE="${1:?}" ;;
    --fixture=*) FIXTURE="${1#*=}" ;;
    --min-gap) shift; MIN_GAP="${1:?}" ;;
    --min-gap=*) MIN_GAP="${1#*=}" ;;
    --*) die "flag desconocida $1" ;;
    *) die "argumento desconocido $1" ;;
  esac
  shift
done

[ -n "$FIXTURE" ] || die "falta --fixture=<pcap|txt>"
[ -f "$FIXTURE" ] || die "fixture inexistente: $FIXTURE"

# pcap_stamps: epochs (int, s) de cada paquete UDP no-DNS, en orden.
pcap_stamps() {
  od -A d -t u1 -v "$1" | awk '
    { off=$1; for (i=2; i<=NF; i++) b[off+i-2]=$i+0; end=off+NF-1 }
    END {
      if (b[0]!=212 || b[1]!=195 || b[2]!=178 || b[3]!=161) {
        print "parser: magic pcap LE (microsegundos) exigido" > "/dev/stderr"; exit 3
      }
      pos=24
      while (pos+16 <= end) {
        ts = b[pos] + b[pos+1]*256 + b[pos+2]*65536 + b[pos+3]*16777216
        incl = b[pos+8] + b[pos+9]*256 + b[pos+10]*65536 + b[pos+11]*16777216
        p = pos + 16 + 14                       # inicio de la cabecera IPv4
        if (b[p-2]==8 && b[p-1]==0) {           # ethertype IPv4
          ihl = b[p]%16                         # IHL: mitad BAJA del byte 0 (version es la alta)
          q = p + ihl*4                         # inicio de la cabecera UDP
          if (b[p+9]==17) {                     # proto UDP
            dport = b[q+2]*256 + b[q+3]
            if (dport != 53) printf "%d\n", ts  # DNS no cuenta: es ruido domestico
          }
        }
        pos += 16 + incl
      }
    }'
}

case "$FIXTURE" in
  *.txt) stamps=$(awk '{printf "%d\n", int($1)}' "$FIXTURE" | sort -n) ;;
  *.pcap)
    if command -v tcpdump >/dev/null 2>&1; then
      stamps=$(tcpdump -r "$FIXTURE" -nn -tt 'udp and port not 53' 2>/dev/null | awk '{print int($1)}' | sort -n)
    else
      stamps=$(pcap_stamps "$FIXTURE")
    fi ;;
  *) die "fixture debe ser .pcap o .txt: $FIXTURE" ;;
esac

n=0
[ -n "$stamps" ] && n=$(printf '%s\n' "$stamps" | wc -l | tr -d ' ')

min_gap=""
if [ "$n" -gt 1 ]; then
  min_gap=$(printf '%s\n' "$stamps" | awk 'NR>1{d=$1-p; if(min==""||d<min)min=d} {p=$1} END{if(min!="")print min}')
fi

if [ -z "$min_gap" ] || [ "$min_gap" -gt "$MIN_GAP" ]; then
  result=PASS
else
  result=FAIL
fi

echo "own_udp_pkts=$n min_gap_s=${min_gap:-n/a} rule=\"hueco UDP propio > ${MIN_GAP}s o ausente\" result=$result"
[ "$result" = PASS ]
