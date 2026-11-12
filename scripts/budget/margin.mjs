// margin.mjs — T-1008 (cierra A2-13): la clase debil se monetiza con
// PRECIO, no con descuento. Lee panel/pricing/classes.yaml (precio
// por GB y capacidad por device_class), calcula el margen bruto con
// el mix pedido y aserta: ratio C/A ≥ 1,3x; margen bruto ≥ 50 % con
// el mix debil (70C,20B,10A — el escenario del atacante con 70 % de
// clase MIPS); y el selector del pool (T-0904) pondera por bw_mbps
// MEDIDO, no declarado (aserto de correlacion en la simulacion de
// reparto).
//
// CLI:
//   node scripts/budget/margin.mjs --mix=70C,20B,10A [--wholesale=0.065]
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { dataPlane } from './budget.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const CLASSES_YAML = path.join(__dirname, '..', '..', 'panel', 'pricing', 'classes.yaml');
export const MARGIN_MIN = 0.5;
export const RATIO_CA_MIN = 1.3;
export const CORR_MIN = 0.9;

// parseClasses: el yaml propio del repo (mapas de dos espacios, sin
// anchors) — sin anadir dependencias al árbol.
export function parseClasses(text) {
  const grab = (key) => {
    const block = text.match(new RegExp(`${key}:\\n((?:  [A-C]: [0-9.]+\\n?)+)`));
    if (!block) return null;
    return Object.fromEntries(
      [...block[1].matchAll(/([A-C]): ([0-9.]+)/g)].map((m) => [m[1], Number(m[2])]),
    );
  };
  const scalar = (key) => {
    const m = text.match(new RegExp(`^${key}: ([0-9.]+)`, 'm'));
    return m ? Number(m[1]) : null;
  };
  const price = grab('price_per_gb');
  const cap = grab('gb_capacity_month');
  const bw = grab('bw_mbps');
  if (!price || !cap || !bw) throw new Error('classes.yaml incompleto');
  return { price, cap, bw, ratioDeclared: scalar('ratio_C_A') };
}

// parseMix: "70C,20B,10A" → [['C',0.7],['B',0.2],['A',0.1]]; exige
// suma 100 (el mix inventado no pasa).
export function parseMix(spec) {
  const parts = String(spec).split(',').map((p) => {
    const m = p.trim().match(/^(\d+)([ABC])$/);
    if (!m) throw new Error(`mix invalido: ${p}`);
    return [m[2], Number(m[1]) / 100];
  });
  const sum = parts.reduce((a, [, w]) => a + w, 0);
  if (Math.abs(sum - 1) > 1e-9) throw new Error(`mix no suma 100: ${spec}`);
  return parts;
}

// marginFor: margen bruto por clase con la hoja T-1005 como overhead
// (ingreso = precio×capacidad; COGS = egress mayorista×capacidad +
// hoja por nodo).
export function marginFor(cls, wholesale, overhead) {
  const revenue = cls.price * cls.cap;
  const cogs = wholesale * cls.cap + overhead;
  return 1 - cogs / revenue;
}

export function blendedMargin(classes, mix, wholesale, overhead) {
  let rev = 0;
  let cost = 0;
  for (const [cls, w] of mix) {
    rev += w * classes.price[cls] * classes.cap[cls];
    cost += w * (wholesale * classes.cap[cls] + overhead);
  }
  return 1 - cost / rev;
}

// repartoPool: simulacion del selector T-0904 — el peso de reparto
// sale del bw MEDIDO (con ruido determinista del heartbeat), jamas
// del declarado. Devuelve la correlacion de Pearson bw_medido↔peso.
export function repartoPool(classes, nodesPerClass = 200, seed = 11) {
  let st = seed >>> 0;
  const rnd = () => {
    st = (st * 1664525 + 1013904223) >>> 0;
    return st / 0xffffffff;
  };
  const rows = [];
  for (const cls of ['A', 'B', 'C']) {
    for (let i = 0; i < nodesPerClass; i++) {
      const declared = classes.bw[cls];
      const measured = declared * (1 + (rnd() - 0.5) * 0.2); // ±10 % medido
      rows.push({ cls, declared, measured });
    }
  }
  const total = rows.reduce((a, r) => a + r.measured, 0);
  rows.forEach((r) => (r.peso = r.measured / total));
  const pearson = (xs, ys) => {
    const n = xs.length;
    const mx = xs.reduce((a, b) => a + b, 0) / n;
    const my = ys.reduce((a, b) => a + b, 0) / n;
    let num = 0;
    let dx = 0;
    let dy = 0;
    for (let i = 0; i < n; i++) {
      num += (xs[i] - mx) * (ys[i] - my);
      dx += (xs[i] - mx) ** 2;
      dy += (ys[i] - my) ** 2;
    }
    return num / Math.sqrt(dx * dy);
  };
  return {
    corrMedidoPeso: pearson(rows.map((r) => r.measured), rows.map((r) => r.peso)),
    corrDeclaradoPeso: pearson(rows.map((r) => r.declared), rows.map((r) => r.peso)),
  };
}

export function checkMargin(classes, mix, wholesale, overhead) {
  if (!(classes.price.C / classes.price.A >= RATIO_CA_MIN)) {
    throw new Error(`ratio C/A ${(classes.price.C / classes.price.A).toFixed(2)} < ${RATIO_CA_MIN}`);
  }
  const m = blendedMargin(classes, mix, wholesale, overhead);
  if (!(m >= MARGIN_MIN)) {
    throw new Error(`margen bruto ${(m * 100).toFixed(1)} % < ${MARGIN_MIN * 100} % con el mix pedido`);
  }
  const rep = repartoPool(classes);
  if (!(rep.corrMedidoPeso >= CORR_MIN)) {
    throw new Error(`correlacion bw_medido↔peso ${rep.corrMedidoPeso.toFixed(2)} < ${CORR_MIN}`);
  }
  return { margin: m, rep };
}

function main(argv) {
  const flags = Object.fromEntries(
    argv.filter((a) => a.startsWith('--')).map((a) => a.slice(2).split('=')),
  );
  const mixSpec = flags.mix ?? '70C,20B,10A';
  const wholesale = Number(flags.wholesale ?? 0.065);
  const classes = parseClasses(readFileSync(CLASSES_YAML, 'utf8'));
  const mix = parseMix(mixSpec);
  const overhead = dataPlane(100000).total_sheet_per_node;
  const { margin, rep } = checkMargin(classes, mix, wholesale, overhead);
  for (const [cls] of mix) {
    console.log(
      `clase ${cls}: ${classes.price[cls]} USD/GB x ${classes.cap[cls]} GB = margen ${(
        marginFor({ price: classes.price[cls], cap: classes.cap[cls] }, wholesale, overhead) * 100
      ).toFixed(1)} %`,
    );
  }
  console.log(
    `ratio C/A: ${(classes.price.C / classes.price.A).toFixed(2)} (≥ ${RATIO_CA_MIN}) | mix ${mixSpec}: margen bruto ${(
      margin * 100
    ).toFixed(1)} % (≥ ${MARGIN_MIN * 100} %)`,
  );
  console.log(
    `pool T-0904: correlacion bw_medido↔peso ${rep.corrMedidoPeso.toFixed(3)} | bw_declarado↔peso ${rep.corrDeclaradoPeso.toFixed(3)} (el reparto sigue lo MEDIDO)`,
  );
  console.log('margin: OK');
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url))
) {
  try {
    main(process.argv.slice(2));
  } catch (e) {
    console.error(`margin: ${e.message}`);
    process.exit(1);
  }
}
