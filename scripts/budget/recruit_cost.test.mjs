// recruit_cost.test.mjs — T-1006: el churn del 15 % a 100k son 15.000
// reintrusiones/mes con precio unitario, y el recluta no pasa del 10 %
// del coste de infraestructura de la hoja T-1005. Corre con
// `node --test scripts/budget/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

import { exitCost, recruit, checkRecruit } from './recruit_cost.mjs';

test('exit limpio presupuestado: probe + stager + enrollado (+ quema)', () => {
  const u = exitCost();
  assert.ok(u.probe > 0 && u.stager > 0 && u.enroll > 0);
  assert.ok(u.burn > 0, 'la quema por densidad (A1-17) es parte del reemplazo');
  assert.ok(u.total > 0 && u.total < 1, `unit=${u.total} USD, want centavos`);
});

// Criterio 1: churn 15 % a 100k = 15.000 reintrusiones/mes.
test('churn 15 % a 100k = 15.000 reintrusiones/mes', () => {
  const r = recruit(100000, 0.15);
  assert.equal(r.reintrusiones, 15000);
  assert.ok(Math.abs(r.recruit_month_usd - 15000 * r.unit.total) < 1e-9);
});

// Criterio 2: ratio ≤ 10 % del coste de infraestructura de la hoja.
test('recluta ≤ 10 % de la infraestructura de la hoja T-1005', () => {
  const r = recruit(100000, 0.15);
  assert.ok(r.infra_month_usd > 0, 'la infra sale de dataPlane(), no a mano');
  checkRecruit(r);
  assert.ok(r.ratio <= 0.1, `ratio ${r.ratio}`);
  // Con un churn 10x peor la hoja SI cae — el assert no es decorativo.
  const malo = recruit(100000, 1.5);
  assert.throws(() => checkRecruit(malo), /infraestructura/);
});
