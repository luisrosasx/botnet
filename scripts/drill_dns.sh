#!/usr/bin/env bash
# drill_dns.sh — drill de DNS pasiva con flows del piloto (T-1307, A1-18):
# agrupa los flows observados por prefijo destino, resuelve cada prefijo por
# DNS pasiva (dataset versionado en testdata/dns_flows/ — aqui no se llama a
# DNS real: limitacion anotada en el runbook) y responde a la unica pregunta
# que importa: icuantos nodos identifica al operador UNA consulta? Un prefijo
# es "del operador" si su PTR cuelga de su dominio, y es firma de flota si
# ademas no carga trafico ajeno (fondo <= 20 %).
#
# Puerta F6: operator_identified_pct <= 60 (se tolera detectar al nodo, no
# identificar a la flota con una consulta). --expect-min N es el modo
# regresion del pre-hardening: con un fixture de un solo ASN el mismo drill
# debe dar > 90 % — el drill detecta su propia razon de ser.
#
# Uso:
#   scripts/drill_dns.sh --nodes=200 --days=7 --isp-top=2
#     [--flows=testdata/dns_flows/flows_hardened.csv] [--ptr=testdata/dns_flows/ptr.csv]
#     [--threshold=60] [--expect-min=N] [--operator=botnet.example]
# Exit: 0 pasa; 1 no pasa; 2 uso.
set -euo pipefail

die() { echo "drill_dns: $*" >&2; exit 2; }

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FLOWS="$ROOT/testdata/dns_flows/flows_hardened.csv"
PTR="$ROOT/testdata/dns_flows/ptr.csv"
NODES=200
DAYS=7
ISP_TOP=2
THRESHOLD=60
EXPECT_MIN=""
OPERATOR="botnet.example"

while [ $# -gt 0 ]; do
  case "$1" in
    --flows) shift; FLOWS="${1:?}" ;;
    --flows=*) FLOWS="${1#*=}" ;;
    --ptr) shift; PTR="${1:?}" ;;
    --ptr=*) PTR="${1#*=}" ;;
    --nodes) shift; NODES="${1:?}" ;;
    --nodes=*) NODES="${1#*=}" ;;
    --days) shift; DAYS="${1:?}" ;;
    --days=*) DAYS="${1#*=}" ;;
    --isp-top) shift; ISP_TOP="${1:?}" ;;
    --isp-top=*) ISP_TOP="${1#*=}" ;;
    --threshold) shift; THRESHOLD="${1:?}" ;;
    --threshold=*) THRESHOLD="${1#*=}" ;;
    --expect-min) shift; EXPECT_MIN="${1:?}" ;;
    --expect-min=*) EXPECT_MIN="${1#*=}" ;;
    --operator) shift; OPERATOR="${1:?}" ;;
    --operator=*) OPERATOR="${1#*=}" ;;
    --*) die "flag desconocida $1" ;;
    *) die "argumento desconocido $1" ;;
  esac
  shift
done

[ -f "$FLOWS" ] || die "flows inexistente: $FLOWS"
[ -f "$PTR" ] || die "ptr inexistente: $PTR"
[ "$NODES" -ge 1 ] && [ "$DAYS" -ge 1 ] && [ "$ISP_TOP" -ge 1 ] || die "nodes/days/isp-top deben ser >= 1"

awk -F, \
  -v nodes="$NODES" -v days="$DAYS" -v isptop="$ISP_TOP" -v thr="$THRESHOLD" \
  -v emin="$EXPECT_MIN" -v op="$OPERATOR" '
  NR==FNR { if (FNR==1) next; split($0,a,","); dom[a[1]]=a[2]; next }   # ptr.csv
  FNR==1 { next }                                                      # header flows
  {
    nd=$1; who=$2; day=$3+0; isp=$4; dst=$5
    if (day<1 || day>days) next
    m=split(dst,o,"."); if (m<4) next
    p=o[1] "." o[2] "." o[3]
    flows[p]++
    if (who!="fleet") { other[p]++; next }      # trafico ajeno = fondo
    if (!(nd in nodeisp)) {
      if (ncand>=nodes) next                    # drill sobre los primeros N
      ncand++; nodeisp[nd]=isp
    }
    touched[nd SUBSEP p]=1
  }
  END {
    for (p in flows) {
      d=dom[p]; owned=(d!="" && substr(d,length(d)-length(op)+1)==op)
      bg=(other[p]+0)/flows[p]
      uniq[p]=(owned && bg<=0.2) ? 1 : 0        # prefijo del operador SIN fondo
    }
    for (nd in nodeisp) {
      id=0
      for (p in uniq) if (uniq[p] && ((nd,p) in touched)) { id=1; break }
      ident[nd]=id
      i=nodeisp[nd]
      nisp[i]++; idisp[i]+=id
    }
    # cohortes por ISP: top-isptop por numero de nodos
    ntot=0; nsel=0; topc=""
    for (k=1; k<=isptop; k++) {
      best=""; bn=0
      for (i in nisp) if (nisp[i]>bn && index(";" topc ";", ";" i ";")==0) { bn=nisp[i]; best=i }
      if (best=="") break
      topc=topc (topc=="" ? "" : ";") best
      printf "isp=%s nodes=%d identified=%d pct=%.1f\n", best, nisp[best], idisp[best], 100*idisp[best]/nisp[best]
      nodes_top+=nisp[best]; id_top+=idisp[best]
    }
    if (nodes_top==0) { print "operator_identified_pct: sin nodos en la ventana"; exit 2 }
    pct=100*id_top/nodes_top
    if (emin!="") { pass=(pct>=emin+0); rule="regresion: pct >= " emin }
    else { pass=(pct<=thr+0); rule="puerta F6: pct <= " thr }
    printf "operator_identified_pct=%.1f nodes=%d rule=\"%s\" result=%s\n", pct, nodes_top, rule, (pass ? "PASS" : "FAIL")
    exit (pass ? 0 : 1)
  }' "$PTR" "$FLOWS"
