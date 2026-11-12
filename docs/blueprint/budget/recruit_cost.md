# Coste de recluta por exit limpio (EP-10 T-1006)

Cierra A2-19: el día uno de cada mes (churn) tiene precio unitario en
la hoja. Reclutar no es gratis ni invisible — y el churn del 15 % a
100.000 nodos **no es un incidente**: son 15.000 reintrusiones
presupuestadas como partida mensual. La aritmética vive en
`scripts/budget/recruit_cost.mjs` (`node scripts/budget/recruit_cost.mjs`;
auditoría `node --test scripts/budget/recruit_cost.test.mjs`).

## El precio unitario del exit limpio

| Pata | USD | De dónde sale |
|---|---:|---|
| Probe | 0,0056 | 40 queries Shodan (0,00012 c/u) + sintaxis masscan (0,00002) |
| Stager | 0,0200 | ~3,5 MB entregados con TLS (ancho de banda + CDN) |
| Enrollado | 0,0100 | CSR, emisión CA offline, `node_secret`, fila en Postgres |
| Quema por densidad | 0,0150 | disparador A1-17: densidad de nodos BOT.NET por ASN/ciudad sobre el umbral → el reemplazo del nodo se paga aquí |
| **Exit limpio** | **0,0506** | |

La **quema por densidad (A1-17)** es parte del coste de reemplazo, no
un renglón aparte: cuando la densidad del barrido sobre una ciudad/ASN
dispara el umbral, el nodo siguiente entra con la prima de quemado ya
presupuestada en estos 0,0150 USD.

## El mes de churn, como partida

- Churn 15 % × 100.000 nodos = **15.000 reintrusiones/mes**.
- 15.000 × 0,0506 = **759 USD/mes** de reclutamiento.
- Infraestructura de la hoja del plano de datos (T-1005,
  `total_sheet_per_node` × nodos a 100k): **17.650 USD/mes**.
- Ratio: **4,30 % ≤ 10 %** — el aserto del pipeline: si el churn o el
  coste unitario suben y la ratio pasa del 10 %, `make budget` falla
  con la ratio impresa.

## Reglas

- La base de la ratio **no se inventa**: se importa
  `dataPlane()` de `budget.mjs` (T-1005). Cambiar la hoja del plano de
  datos mueve automáticamente esta ratio.
- Con churn 150 % (colapso del modelo) el assert cae en el test — la
  ratio de 10 % no es decorativa.
