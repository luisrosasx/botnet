# Hoja del plano de datos (EP-10 T-1005) — medida a 1.000 y 100.000 nodos

Cierra A2-03, A2-16 y A2-06: el «0,185 USD/nodo» del veredicto F0 es
creíble solo si la hoja del plano de datos existe, está medida a 100k y
falla el build si el margen bruto cae bajo 50 %. La aritmética vive en
`scripts/budget/budget.mjs` (CLI: `node scripts/budget/budget.mjs`;
auditoría: `node --test scripts/budget/budget.test.mjs`); esta hoja
declara las partidas NOMBRADAS y los topes.

## Las tres partidas (USD/nodo/mes, egress en USD/mes)

| Partida | 1.000 nodos | 100.000 nodos | Base |
|---|---:|---:|---|
| **Gateways** | 0,05 | 0,05 | 1 ud por cada 250 nodos × 12,5 USD/mes |
| **Egress por GB vendido** (medidor + reenvio, la reventa lo absorbe) | 0,125 | 0,125 | 90 GB vendidos/nodo/mes (tope 150, llenado 60 %) |
| **Capa de túneles** (headscale + rendezvous) | 0,03 | 0,0003 | 30 USD/mes fijos |
| Control (api/pg/redis/nats fijos) | 0,12 | 0,0012 | 120 USD/mes fijos |
| **Hoja total** | 0,325 | **0,1765** | ≈ el 0,185 del veredicto |

El egress mayorista se precio en la **banda 0,04–0,09 USD/GB** (la
hoja usa 0,065): a 100k son **585.000 USD/mes** (rango
360.000–810.000). ElGB se revende al pool a 0,30 USD/GB: la reventa
absorbe el egress y por eso la partida de la hoja es el overhead, no
el mayorista (R6).

## Asertos que caen en el build

- **control+gateways ≤ 0,25 USD/nodo/mes** — a 100k: 0,0512; a 1k:
  0,17. ✅ (margen del brief absorbido con 5× de holgura).
- **egress presente en USD/mes con rango explícito** — 0,065 USD/GB
  dentro de 0,04–0,09 ✅.
- **margen bruto ≥ 50 % a 100k** — 77,7 % (COGS = egress mayorista +
  hoja; ingreso = 90 GB × 0,30 USD) ✅.

## El escenario de colapso del veredicto (R6 / «pesadilla A2»)

Gateways ×3 (el P99 de 250 nodos/ud no se sostiene y cada nodo paga su
cuarto de gateway extra) y egress al peor precio de la banda:

> **Colapso A2: 0,5265 USD/nodo/mes a 100k (cota 0,55).** La pesadilla
> está presupuestada, no negada — y aun en colapso el margen bruto
> sigue sobre 50 % porque el GB se revende.

## Reglas de la hoja

- Si falta una de las tres partidas, `checkSheet` sale con exit ≠ 0
  (injerto 5: nada de hojas mudas).
- Los topes (0,25 / 0,04–0,09 / 50 % / 0,55) son constantes del script;
  moverlas exige mover esta hoja en la misma PR.
- El coste de recluta (T-1006) y el precio por clase (T-1008) se
  apoyan en `total_sheet_per_node` de esta hoja: el churn tiene precio
  porque la base lo tiene.
