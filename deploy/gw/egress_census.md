---
artifact: census
epic: EP-09
task: T-0908
created: 2026-11-14
---

# Censo de egress por gateway (A2-06 / herencia A2-16)

El egress por gateway se dimensiona **con números de provedor, no de fe**
(§10). Criterio de dimensionado del edge: **≥ 75 Mbps sostenidos con 250
nodos por gateway**. El número de referencia del primer censo F4 procede
del doble de soak de EP-09 (`scripts/soak/loss_soak.sh` →
`docs/verification/loss_soak_report.json`: ratio p95 por sesión 1,025 ≤
1,2 con netem loss 1,5 %; sesiones concurrentes por nodo ≈ 30) y de la
sonda iperf3 de la malla (`bandwidth_cap_kbps`, EP-07). Cada provedor se
remeide en producción con la sonda antes de cerrar el presupuesto de red.

| Provedor    | Gateway     | Nodos | Egress medio/nodo (sonda) | Egress gateway @250 | Criterio ≥75 |
|-------------|-------------|-------|---------------------------|---------------------|--------------|
| Movistar    | gw-es-mad   | 250   | 0,32 Mbps                 | 80,0 Mbps           | ✅           |
| Orange      | gw-es-vlc   | 250   | 0,33 Mbps                 | 82,5 Mbps           | ✅           |
| Jazztel     | gw-pt-lis   | 250   | 0,31 Mbps                 | 77,5 Mbps           | ✅           |
| WIGGG       | gw-pt-opo   | 250   | 0,36 Mbps                 | 90,0 Mbps           | ✅           |

Reglas del censo:

- **Unidad:** Mbps **sostenidos** (p95 por sesión independiente, no
  picos de iperf3). El ratio max/min del soak (≤1,2) es la condición de
  que el número por gateway no esconde head-of-line (T-0906/A2-02).
- **Origen de la cifra:** `SustainedEgressMbps(250, perNode)` de
  `cmd/botnet-gw/drain.go`, asertado ≥ 75 en `TestEgressCensusThreshold`.
- **Actualización:** tras cada cambio de flota objetivo (250 → 1.000 por
  gateway a 100k nodos) o de provedor; el drenaje (`gw:drain:{gw}`)
  permite remedir gateway a gateway sin cortar servicio (60 s por fila,
  AC1 de T-0908).
- **Presupuesto:** esta tabla es la partida nombrada de la sección 2 del
  blueprint para el edge del S1; si un provedor no llega a 75, el
  dimensionado baja a <250 nodos/gw y el rdv reparte más gateways antes
  de tocar topes de sesión.
