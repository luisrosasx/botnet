// margin.test.mjs — T-1008: el precio por clase y el margen con el
// mix debil se auditan solos. Corre con `node --test scripts/budget/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  parseClasses,
  parseMix,
  blendedMargin,
  checkMargin,
  repartoPool,
  CLASSES_YAML,
  MARGIN_MIN,
  RATIO_CA_MIN,
  CORR_MIN,
} from './margin.mjs';

const classes = parseClasses(readFileSync(CLASSES_YAML, 'utf8'));

// AC1: ratio clase C ≥ 1,3x clase A — la clase debil se precia.
test('ratio C/A ≥ 1,3x desde classes.yaml', () => {
  const ratio = classes.price.C / classes.price.A;
  assert.ok(ratio >= RATIO_CA_MIN, `ratio ${ratio}`);
  assert.ok(
    classes.price.A < classes.price.B && classes.price.B < classes.price.C,
    'el escalado A<B<C es el que monetiza la clase debil',
  );
});

// AC2: margen bruto ≥ 50 % con el mix debil 70C,20B,10A.
test('mix debil 70C,20B,10A mantiene el margen ≥ 50 %', () => {
  const mix = parseMix('70C,20B,10A');
  const m = blendedMargin(classes, mix, 0.065, 0.1765);
  assert.ok(m >= MARGIN_MIN, `margen ${m}`);
  assert.throws(() => checkMargin(classes, mix, 0.25, 0.1765), /margen bruto/);
  // Con un mix inventado que no suma 100, ni se entra a la hoja.
  assert.throws(() => parseMix('70C,70B'), /mix/);
});

// AC3: el pool pondera por bw_mbps MEDIDO, no declarado: en la
// simulacion de reparto la correlacion con lo medido manda y es
// estrictamente mejor que la del valor declarado del router.
test('el reparto sigue bw_mbps medido, no declarado', () => {
  const rep = repartoPool(classes);
  assert.ok(rep.corrMedidoPeso >= CORR_MIN, `corr medido ${rep.corrMedidoPeso}`);
  assert.ok(
    rep.corrMedidoPeso > rep.corrDeclaradoPeso,
    'el peso del pool debe seguir lo medido por encima de lo declarado',
  );
});
