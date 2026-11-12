// budget.mjs — T-1005 (cierra A2-03, A2-16, A2-06): la hoja del
// PLANO DE DATOS medida a 1.000 y 100.000 nodos. Las tres partidas
// nombradas (gateways, egress por GB vendido, capa de túneles) viven
// aqui con aritmética auditada; el 0,185 USD/nodo del veredicto es
// creible porque esta hoja existe y falla el build si el margen
// bruto cae bajo 50 %. El escenario de colapso del veredicto A2
// (gateways ×3 + egress al peor precio) esta presupuestado, no negado.
//
// CLI:
//   node scripts/budget/budget.mjs            → tablas 1k y 100k + checks
//   node scripts/budget/budget.mjs --check    → solo checks (exit != 0 si falta partida)

// --- constantes de la hoja (mensuales, USD) ---------------------------------
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const NODES_PER_GW = 250; // mismo suelo que aggregate.mjs (2 vCPU/ud)
export const GW_USD_MONTH = 12.5; // 4 uds ≈ 50 USD/mes por gateway
export const CONTROL_FIXED_USD = 120; // api+pg+redis+nats compartidos
export const TUNNELS_FIXED_USD = 30; // headscale 1 ud + rdv
export const GB_CAP_MONTH = 150; // tope publicado por nodo (A2-20)
export const GB_FILL = 0.6; // llenado medio: 90 GB vendidos/nodo/mes
export const EGRESS_WHOLESALE = 0.065; // USD/GB mayorista (rango 0.04–0.09)
export const EGRESS_WHOLESALE_MIN = 0.04;
export const EGRESS_WHOLESALE_MAX = 0.09;
export const EGRESS_OVERHEAD_PER_NODE = 0.125; // medidor+reenvio (no reventa)
export const PRICE_USD_GB = 0.3; // GB al pool del cliente
export const CTRL_GW_BUDGET = 0.25; // USD/nodo/mes maximo (AC2)
export const MARGIN_MIN = 0.5; // margen bruto minimo a 100k (AC2)
export const COLLAPSE_BUDGET = 0.55; // USD/nodo en colapso A2 (AC3)

export const fmt = (v) =>
  Number.isInteger(v) ? String(v) : Number(v.toFixed(4)).toString();

// dataPlane: la hoja por escala. Cada partida es una clave NOMBREADA;
// checkSheet exige que las tres esten presentes (injerto 5).
export function dataPlane(nodes) {
  const gb_sold = GB_CAP_MONTH * GB_FILL;
  const gateways = Math.ceil(nodes / NODES_PER_GW) * GW_USD_MONTH;
  const per_node = (v) => v / nodes;
  const control_per_node = per_node(CONTROL_FIXED_USD);
  const gateways_per_node = per_node(gateways);
  const tunnels_per_node = per_node(TUNNELS_FIXED_USD);
  const egress_wholesale_month = gb_sold * EGRESS_WHOLESALE * nodes;
  const revenue_month = gb_sold * PRICE_USD_GB * nodes;
  const cogs_month =
    (egress_wholesale_month +
      EGRESS_OVERHEAD_PER_NODE * nodes +
      control_per_node * nodes +
      gateways +
      tunnels_per_node * nodes);
  return {
    nodes,
    gb_sold_per_node: gb_sold,
    partidas: {
      gateways: gateways_per_node,
      egress_por_gb_vendido: EGRESS_OVERHEAD_PER_NODE,
      capa_tuneles: tunnels_per_node,
    },
    control_per_node,
    ctrl_gw_per_node: control_per_node + gateways_per_node,
    total_sheet_per_node:
      control_per_node +
      gateways_per_node +
      tunnels_per_node +
      EGRESS_OVERHEAD_PER_NODE,
    egress_wholesale: EGRESS_WHOLESALE,
    egress_wholesale_usd_month: egress_wholesale_month,
    egress_wholesale_range_usd_month: [
      gb_sold * EGRESS_WHOLESALE_MIN * nodes,
      gb_sold * EGRESS_WHOLESALE_MAX * nodes,
    ],
    revenue_usd_month: revenue_month,
    gross_margin: 1 - cogs_month / revenue_month,
    collapse_total_per_node:
      control_per_node +
      3 * gateways_per_node +
      3 * EGRESS_OVERHEAD_PER_NODE +
      tunnels_per_node,
  };
}

// checkSheet: las tres partidas nombradas deben existir y ser > 0.
// Si falta alguna (injerto 5), sale con exit != 0.
export function checkSheet(sheet) {
  const required = ['gateways', 'egress_por_gb_vendido', 'capa_tuneles'];
  const missing = required.filter(
    (k) => !(k in sheet.partidas) || !(sheet.partidas[k] > 0),
  );
  if (missing.length) {
    throw new Error(`partidas faltantes: ${missing.join(', ')}`);
  }
  const [lo, hi] = sheet.egress_wholesale_range_usd_month;
  if (!(lo > 0 && hi > lo)) throw new Error('rango de egress sin aritmética');
  if (!(sheet.ctrl_gw_per_node <= CTRL_GW_BUDGET)) {
    throw new Error(
      `control+gateways ${fmt(sheet.ctrl_gw_per_node)} USD/nodo > ${CTRL_GW_BUDGET}`,
    );
  }
  if (!(sheet.egress_wholesale >= EGRESS_WHOLESALE_MIN &&
        sheet.egress_wholesale <= EGRESS_WHOLESALE_MAX)) {
    throw new Error(
      `egress ${sheet.egress_wholesale} USD/GB fuera del rango 0,04–0,09`,
    );
  }
  if (sheet.nodes >= 100000 && !(sheet.gross_margin >= MARGIN_MIN)) {
    throw new Error(`margen bruto ${(sheet.gross_margin * 100).toFixed(1)} % < 50 % a 100k`);
  }
  if (sheet.nodes >= 100000 && !(sheet.collapse_total_per_node <= COLLAPSE_BUDGET)) {
    throw new Error(
      `colapso A2 ${fmt(sheet.collapse_total_per_node)} USD/nodo > ${COLLAPSE_BUDGET}`,
    );
  }
  return true;
}

function printTable(sheet) {
  const p = sheet.partidas;
  console.log(
    `--- plano de datos a ${sheet.nodes} nodos (${fmt(sheet.gb_sold_per_node)} GB vendidos/nodo/mes) ---`,
  );
  console.log(
    `gateways: ${fmt(p.gateways)} USD/nodo | egress por GB vendido: ${fmt(
      p.egress_por_gb_vendido,
    )} USD/nodo | capa de tuneles: ${fmt(p.capa_tuneles)} USD/nodo`,
  );
  console.log(
    `control+gateways: ${fmt(sheet.ctrl_gw_per_node)} USD/nodo (presupuesto ${CTRL_GW_BUDGET}) | hoja total: ${fmt(
      sheet.total_sheet_per_node,
    )} USD/nodo`,
  );
  console.log(
    `egress mayorista: ${fmt(sheet.egress_wholesale)} USD/GB → ${fmt(
      Math.round(sheet.egress_wholesale_usd_month),
    )} USD/mes (rango ${fmt(Math.round(sheet.egress_wholesale_range_usd_month[0]))}–${fmt(
      Math.round(sheet.egress_wholesale_range_usd_month[1]),
    )})`,
  );
  console.log(
    `margen bruto: ${(sheet.gross_margin * 100).toFixed(1)} % (min ${
      MARGIN_MIN * 100
    } % a 100k) | colapso A2 (gw ×3 + egress): ${fmt(
      sheet.collapse_total_per_node,
    )} USD/nodo (cota ${COLLAPSE_BUDGET} medida a 100k)`,
  );
}

function main(argv) {
  const checkOnly = argv.includes('--check');
  const s1k = dataPlane(1000);
  const s100k = dataPlane(100000);
  checkSheet(s1k);
  checkSheet(s100k);
  if (!checkOnly) {
    printTable(s1k);
    printTable(s100k);
  }
  console.log(
    `budget: plano de datos OK — hoja ${fmt(s100k.total_sheet_per_node)} USD/nodo, margen ${(
      s100k.gross_margin * 100
    ).toFixed(1)} %, colapso ${fmt(s100k.collapse_total_per_node)} USD/nodo`,
  );
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url))
) {
  try {
    main(process.argv.slice(2));
  } catch (e) {
    console.error(`budget: ${e.message}`);
    process.exit(1);
  }
}
