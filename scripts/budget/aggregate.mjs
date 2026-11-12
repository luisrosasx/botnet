// aggregate.mjs — T-1206 (cierra A2-12): la hoja agregada del
// BLUEPRINT §2 recalculada en F0 con aritmética auditable. El error
// histórico del proceso (los «~3 Gbps» de P3 para 1.000 nodos: un
// factor 10 de más) queda ciego ante un juez ante este script: la
// multiplicación honesta es nodes × concurrency × mbps, y las
// instancias de gateway se derivan de 250 nodos/ud — extrapolar suma
// unidades, no ceros.
//
// CLI:
//   node scripts/budget/aggregate.mjs --nodes=1000 --concurrency=0.15 --mbps=2
//   → egress_mbps=300 gw_units=4 ...
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

export const NODES_PER_GW = 250; // 2 vCPU por ud (BLUEPRINT §2)
export const GATEWAY_USD_MONTH = 12.5; // 4 uds ≈ 50 USD/mes
export const EGRESS_TOL_MBPS = 20; // ±20: el margen del criterio AC1

const __dirname = path.dirname(fileURLToPath(import.meta.url));

export function loadFixture(name) {
  return JSON.parse(
    readFileSync(path.join(__dirname, 'fixtures', name), 'utf8'),
  );
}

// aggregate es la multiplicación honesta: sin factor 10, sin "~".
// concurrency es la fracción simultánea de nodos activos (15 %);
// mbps, el tope por nodo activo (2 Mbps).
export function aggregate({ nodes, concurrency, mbps, nodesPerGW = NODES_PER_GW }) {
  if (!(nodes > 0) || !(concurrency > 0) || !(mbps > 0)) {
    throw new Error('aggregate: nodes, concurrency y mbps deben ser > 0');
  }
  if (concurrency > 1) {
    throw new Error('aggregate: concurrency es fracción (0..1)');
  }
  const egress_mbps = nodes * concurrency * mbps;
  const gw_units = Math.ceil(nodes / nodesPerGW);
  return {
    nodes,
    concurrency,
    mbps,
    egress_mbps,
    gw_units,
    gateway_usd_month: gw_units * GATEWAY_USD_MONTH,
  };
}

// claimsOk aserta una reclamación de la hoja contra la multiplicación
// honesta: fuera de ±EGRESS_TOL_MBPS la hoja miente (factor 10).
export function claimWithin(claimed, computed, tol = EGRESS_TOL_MBPS) {
  return Math.abs(claimed - computed) <= tol;
}

// fmt: 300 → "300"; 299.5 → "299.5" (la hoja no finge precisión).
function fmt(v) {
  return Number.isInteger(v) ? String(v) : v.toFixed(1);
}

function main(argv) {
  const flags = Object.fromEntries(
    argv
      .filter((a) => a.startsWith('--'))
      .map((a) => a.slice(2).split('='))
      .map(([k, v]) => [k, v]),
  );
  const nodes = Number(flags.nodes ?? 1000);
  const concurrency = Number(flags.concurrency ?? 0.15);
  const mbps = Number(flags.mbps ?? 2);

  const r = aggregate({ nodes, concurrency, mbps });
  const row100k = aggregate({ nodes: 100000, concurrency, mbps });

  // Regresión anti-factor-diez contra el fixture versionado: si una
  // hoja reclamara 3.000 Mbps para 1.000 nodos, esto sale en rojo.
  const fixture = loadFixture('factor10.json');
  const fixtureTruth = aggregate(fixture.input);
  const fixtureHonest = claimWithin(fixture.claimed_egress_mbps, fixtureTruth.egress_mbps);

  console.log(`egress_mbps=${fmt(r.egress_mbps)}`);
  console.log(`gw_units=${r.gw_units}`);
  console.log(`gateway_usd_month=${fmt(r.gateway_usd_month)}`);
  console.log(`row_100k_egress_mbps=${fmt(row100k.egress_mbps)}`);
  console.log(`row_100k_gw_units=${row100k.gw_units}`);
  console.log(
    `factor10_fixture_claim=${fixture.claimed_egress_mbps} truth=${fmt(
      fixtureTruth.egress_mbps,
    )} honesto=${fixtureHonest}`,
  );

  // El fixture factor10 esta ENVEJECIDO a proposito (reclama 3.000):
  // honesto=false es el resultado esperado — la regresion anti-factor-
  // diez que vive en aggregate.test.mjs aserta que esa hoja fallaria.
  if (!(r.egress_mbps >= 0)) process.exit(1);
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url))) {
  main(process.argv.slice(2));
}
