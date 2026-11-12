// aggregate.test.mjs — T-1206: la aritmética agregada se audita sola
// en F0. Corre con `node --test scripts/budget/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  aggregate,
  claimWithin,
  loadFixture,
  EGRESS_TOL_MBPS,
} from './aggregate.mjs';

// AC1: 1.000 nodos × 0.15 × 2 Mbps = 300 Mbps ± 20 — la multiplicación
// honesta, no los «~3 Gbps» de P3.
test('egress honesto a 1.000 nodos = 300 ± 20 Mbps', () => {
  const r = aggregate({ nodes: 1000, concurrency: 0.15, mbps: 2 });
  assert.ok(
    Math.abs(r.egress_mbps - 300) <= 20,
    `egress_mbps=${r.egress_mbps}, want 300 ± 20`,
  );
  const honest = loadFixture('honest1k.json');
  assert.equal(claimWithin(honest.claimed_egress_mbps, r.egress_mbps), true);
});

// AC2: el fixture con el error del factor 10 (entrada 3.000) hace
// fallar la aserción — la regresión anti-factor-diez queda ciega ante
// un juez solo si este test existiera; aquí no.
test('fixture factor-10 (3.000) repe la regresión', () => {
  const f = loadFixture('factor10.json');
  const truth = aggregate(f.input);
  assert.equal(truth.egress_mbps, 300, 'la verdad sigue siendo 300');
  assert.equal(
    claimWithin(f.claimed_egress_mbps, truth.egress_mbps),
    false,
    `claim=${f.claimed_egress_mbps} fuera de ±${EGRESS_TOL_MBPS} de la verdad`,
  );
  // Y si alguien «arregla» el factor 10 multiplicando de más, la hoja
  // entera se va de margen: 3.000 vs 300 es un 900 % de desvío.
  assert.ok(f.claimed_egress_mbps / truth.egress_mbps >= 9);
});

// AC3: instancias de gateway derivadas (250 nodos/ud): 1.000 → 4 uds;
// 100.000 → 400 uds — extrapolar no repite el error (assert 100k).
test('gateways derivados: 1k→4 uds, 100k→400 uds', () => {
  const small = aggregate({ nodes: 1000, concurrency: 0.15, mbps: 2 });
  const big = aggregate({ nodes: 100000, concurrency: 0.15, mbps: 2 });
  assert.equal(small.gw_units, 4);
  assert.equal(big.gw_units, 400);
  assert.equal(big.egress_mbps, 30000, '100k × 0.15 × 2 = 30.000 Mbps, no 300k');
  assert.equal(big.gw_units, small.gw_units * 100, 'la escala es lineal en unidades');
});

// cordura de entrada: la hoja no se deja alimentar basura.
test('aggregate rechaza entradas inválidas', () => {
  assert.throws(() => aggregate({ nodes: 0, concurrency: 0.15, mbps: 2 }));
  assert.throws(() => aggregate({ nodes: 1000, concurrency: 15, mbps: 2 }));
});
