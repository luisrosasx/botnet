# Drill de DNS pasiva (T-1307, A1-18)

**Pregunta que responde.** Un analista del ISP agrupa sus logs CGNAT por
destino, hace UNA consulta de DNS pasiva sobre el prefijo dominante y le
sale el nombre del operador: eso identifica a la flota entera y hace que
una politica por prefijo (QoS o ACL) apague a toda la cohorte. El drill
mide que fraccion de nodos sigue expuesta a esa unica consulta tras el
hardening (SLO F6: `operator_identified_pct <= 60`).

**Como se ejecuta.**

```bash
scripts/drill_dns.sh --nodes=200 --days=7 --isp-top=2
# fixture pre-hardening (un solo ASN): > 90 % — el drill detecta su razon de ser
scripts/drill_dns.sh --flows=testdata/dns_flows/flows_single_asn.csv --expect-min=90
```

**Datos versionados (`testdata/dns_flows/`).** `flows_hardened.csv` y
`flows_single_asn.csv`: flows observados del piloto como
`node_id,who,day,isp,dst_ip` (fleet = la flota; other = trafico real del
prefijo). `ptr.csv`: resolucion pasiva por prefijo /24 a dominio;
`asn.csv`: ASN por prefijo (dispersion >= 2 ASNs de T-0407).

**Regla del drill.** Un prefijo es del operador si su PTR cuelga del
dominio del operador (`--operator=botnet.example`) y es firma de flota si
su fondo (flows no-flota sobre el prefijo) es <= 20 %. Un nodo queda
identificado si en la ventana (`--days`) toco algun prefijo-unicidad del
operador. El informe sale por las dos mayores cohortes ISP y el global
`operator_identified_pct`; el exit code es la puerta (0 = pasa).

**Salida real (fixtures).** hardened: ISP-A 80 %, ISP-B 0 %, global
**48,0 % → PASS** (<= 60). single ASN: **100,0 %** — con puerta por
defecto da FAIL y con `--expect-min=90` PASS: el drill detecta su propia
razon de ser.

**Limitaciones.** El dataset es un doble: la DNS pasiva real (por
ej. PassiveDNS RT + ranks de popularidad) y los 7 dias de flows del
top-2 ISP sobre >= 200 nodos reales son del piloto (F6); el prefijo se
resuelve a /24 fijo (en produccion, el drill resolvera el prefijo que el
analista elija, y /24 es su eleccion mas barata). Umbral de fondo 20 % y
`isp-top` son parametricos.
