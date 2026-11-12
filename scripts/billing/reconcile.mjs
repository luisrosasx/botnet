// reconcile.mjs — conciliacion diaria de las tres patas (EP-11
// T-1104): `usage` (medidor autoritativo) ↔ sesiones del gateway ↔
// Stripe (factura del periodo, servida por el FakeStripe local).
// Imprime drift_pct con la tolerancia unica (0,5 %). Un desvio rojo
// es ALARMA con cobro no bloqueado (equivalente HTTP 200,
// reconciled=false): la conciliacion alarma, no es portero.
//
// Uso:
//   node scripts/billing/reconcile.mjs --fixture=scripts/billing/fixtures/reconcile_24h.json
//   node scripts/billing/reconcile.mjs --fixture=.../reconcile_desvio.json --expect-alarm
import { readFileSync } from "node:fs";
import process from "node:process";

export const TOLERANCE_PCT = 0.5;
export const GB = 1e9; // decimal, identico a accounting.GB

export function driftPct(a, b) {
  const ref = Math.max(a, b);
  if (ref === 0) return 0;
  return (Math.abs(a - b) / ref) * 100;
}

export function reconcileDay(day, tolPct = TOLERANCE_PCT) {
  const usage = day.usage_bytes;
  const sessions = day.sessions_bytes;
  const stripeBytes = day.stripe_gb * GB;
  const drift = Math.max(driftPct(usage, sessions), driftPct(usage, stripeBytes));
  return {
    day: day.day,
    usage,
    sessions,
    stripeBytes,
    driftPct: drift,
    tolPct: tolPct,
    reconciled: drift <= tolPct,
  };
}

function main(argv) {
  const fixArg = argv.find((a) => a.startsWith("--fixture="));
  if (!fixArg) {
    console.error("reconcile: --fixture=<json> requerido");
    return 2;
  }
  const path = fixArg.slice("--fixture=".length);
  let doc;
  try {
    doc = JSON.parse(readFileSync(path, "utf8"));
  } catch (err) {
    console.error(`reconcile: fixture ilegible (${path}): ${err.message}`);
    return 2;
  }
  const rep = reconcileDay(doc);
  const line = `day=${rep.day} usage=${rep.usage} sessions=${rep.sessions} stripe=${rep.stripeBytes} drift_pct=${rep.driftPct.toFixed(3)} tol_pct=${rep.tolPct.toFixed(2)} reconciled=${rep.reconciled} alarm=${rep.reconciled ? 0 : 1} cobro=no_bloqueado`;
  console.log(line);
  if (!rep.reconciled) {
    console.log("reconcile: ALERTA — desvio fuera de tolerancia; el cobro NO se bloquea (HTTP 200)");
  }
  const expectAlarm = argv.includes("--expect-alarm");
  if (expectAlarm) {
    if (rep.reconciled) {
      console.error("reconcile: el fixture deberia estar en alarma y no lo esta");
      return 1;
    }
    return 0;
  }
  return rep.reconciled ? 0 : 1;
}

if (process.argv[1] && process.argv[1].endsWith("reconcile.mjs")) {
  process.exit(main(process.argv.slice(2)));
}
