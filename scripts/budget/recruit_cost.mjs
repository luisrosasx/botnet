// recruit_cost.mjs — T-1006 (cierra A2-19): el coste de reclutar un
// exit limpio es PARTIDA del mes, no incidente. Suma probe (Shodan/
// masscan), stager y enrollado; el churn del 15 % a 100.000 nodos son
// 15.000 reintrusiones al mes con precio unitario. La ratio contra la
// hoja del plano de datos (T-1005) esta asertada: si el recluta pasa
// del 10 % del coste de infraestructura, el pipeline make budget
// pierde con la ratio impresa.
//
// CLI:
//   node scripts/budget/recruit_cost.mjs [--nodes=100000] [--churn=0.15]
import { dataPlane } from './budget.mjs';

// Costes unitarios por exit limpio (USD).
export const PROBES_PER_EXIT = 40; // barrido hasta el target que entra
export const SHODAN_QUERY_USD = 0.00012; // 120 USD / 1M de queries
export const MASSCAN_TARGET_USD = 0.00002; // sintaxis de puertos + banner
export const STAGER_USD = 0.02; // ~3,5 MB de stager + TLS de entrega
export const ENROLL_USD = 0.01; // CSR, emision CA, node_secret, fila
export const DENSITY_BURN_USD = 0.015; // quema por densidad (A1-17): el
// disparador — densidad de nodos BOT.NET sobre el mismo ASN/ciudad por
// encima del umbral — cobra el reemplazo del nodo como parte del coste
// de reemplazo, no como incidente aparte.

export function exitCost() {
  const probe = PROBES_PER_EXIT * (SHODAN_QUERY_USD + MASSCAN_TARGET_USD);
  const replacement = STAGER_USD + ENROLL_USD + DENSITY_BURN_USD;
  return { probe, stager: STAGER_USD, enroll: ENROLL_USD, burn: DENSITY_BURN_USD, total: probe + replacement };
}

// recruit: el mes de churn presupuestado. nodes=100000, churn=0.15 →
// 15.000 reintrusiones; infra viene de la hoja T-1005 (no se inventa
// un segundo numero base).
export function recruit(nodes = 100000, churn = 0.15) {
  const unit = exitCost();
  const reintrusiones = Math.round(nodes * churn);
  const recruit_month_usd = reintrusiones * unit.total;
  const sheet = dataPlane(nodes);
  const infra_month_usd = sheet.total_sheet_per_node * nodes;
  const ratio = recruit_month_usd / infra_month_usd;
  return {
    nodes,
    churn,
    unit,
    reintrusiones,
    recruit_month_usd,
    infra_month_usd,
    ratio,
    ratio_max: 0.1,
  };
}

// checkRecruit: el aserto del criterio 2 — ratio ≤ 10 %; si no, la
// hoja cae con la ratio impresa (el pipeline make budget la ve).
export function checkRecruit(r) {
  if (!(r.ratio <= r.ratio_max)) {
    throw new Error(
      `reclutamiento ${(r.ratio * 100).toFixed(1)} % del coste de infraestructura > 10 %`,
    );
  }
  if (r.reintrusiones !== Math.round(r.nodes * r.churn)) {
    throw new Error('churn mal arithmeticado');
  }
  return true;
}

function main(argv) {
  const flags = Object.fromEntries(
    argv.filter((a) => a.startsWith('--')).map((a) => a.slice(2).split('=')),
  );
  const nodes = Number(flags.nodes ?? 100000);
  const churn = Number(flags.churn ?? 0.15);
  const r = recruit(nodes, churn);
  checkRecruit(r);
  const f = (v) => Number(v.toFixed(4)).toString();
  console.log(
    `exit limpio: probe ${f(r.unit.probe)} + stager ${f(r.unit.stager)} + enroll ${f(
      r.unit.enroll,
    )} + quema densidad ${f(r.unit.burn)} = ${f(r.unit.total)} USD`,
  );
  console.log(
    `churn ${Math.round(churn * 100)} % a ${nodes} nodos = ${r.reintrusiones} reintrusiones/mes = ${f(
      r.recruit_month_usd,
    )} USD/mes`,
  );
  console.log(
    `ratio vs hoja T-1005 (${f(Math.round(r.infra_month_usd))} USD/mes): ${(
      r.ratio * 100
    ).toFixed(2)} % (≤ 10 %)`,
  );
  console.log('recruit_cost: partida de churn OK');
}

if (
  process.argv[1] &&
  process.argv[1].endsWith('recruit_cost.mjs')
) {
  try {
    main(process.argv.slice(2));
  } catch (e) {
    console.error(`recruit_cost: ${e.message}`);
    process.exit(1);
  }
}
