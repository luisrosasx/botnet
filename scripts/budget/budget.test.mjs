// budget.test.mjs — T-1005: la hoja del plano de datos se audita sola.
// Corre con `node --test scripts/budget/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  dataPlane,
  checkSheet,
  CTRL_GW_BUDGET,
  MARGIN_MIN,
  COLLAPSE_BUDGET,
  EGRESS_WHOLESALE_MIN,
  EGRESS_WHOLESALE_MAX,
} from './budget.mjs';

// AC1: las 3 partidas nombradas existen a 1.000 y a 100.000 nodos, y
// la hoja se cae si falta alguna (injerto 5).
test('3 partidas nombradas a 1k y 100k; falta = exit', () => {
  for (const n of [1000, 100000]) {
    const s = dataPlane(n);
    assert.ok(s.partidas.gateways > 0);
    assert.ok(s.partidas.egress_por_gb_vendido > 0);
    assert.ok(s.partidas.capa_tuneles > 0);
    assert.equal(checkSheet(s), true);
  }
  const roto = dataPlane(100000);
  delete roto.partidas.capa_tuneles; // injerto 5: una partida borrada
  assert.throws(() => checkSheet(roto), /partidas faltantes/);
});

// AC2: asertos numericos de la hoja.
test('control+gateways ≤ 0,25; egress en rango; margen ≥ 50 % a 100k', () => {
  for (const n of [1000, 100000]) {
    const s = dataPlane(n);
    assert.ok(
      s.ctrl_gw_per_node <= CTRL_GW_BUDGET,
      `control+gw ${s.ctrl_gw_per_node} > ${CTRL_GW_BUDGET} a ${n}`,
    );
  }
  const s = dataPlane(100000);
  assert.ok(
    s.egress_wholesale >= EGRESS_WHOLESALE_MIN &&
      s.egress_wholesale <= EGRESS_WHOLESALE_MAX,
    'egress USD/GB fuera del rango 0,04–0,09',
  );
  const [lo, hi] = s.egress_wholesale_range_usd_month;
  assert.ok(lo > 0 && hi > lo, 'el egress debe presentarse en USD/mes con rango');
  assert.ok(s.gross_margin >= MARGIN_MIN, `margen ${s.gross_margin} < 50 %`);
});

// AC3: el colapso del veredicto (gateways ×3 + egress) queda
// presupuestado: ≤ 0,55 USD/nodo y la hoja lo nombra.
test('colapso A2 ≤ 0,55 USD/nodo a 100k', () => {
  const s = dataPlane(100000);
  assert.ok(
    s.collapse_total_per_node <= COLLAPSE_BUDGET,
    `colapso ${s.collapse_total_per_node} > ${COLLAPSE_BUDGET}`,
  );
  assert.ok(
    s.total_sheet_per_node < s.collapse_total_per_node,
    'el colapso debe ser peor que la linea base',
  );
});
