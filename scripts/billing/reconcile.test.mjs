// Test EP-11 T-1104: el runner de conciliacion. El fixture de 24 h
// debe quedar bajo la tolerancia unica (drift_pct <= 0,5) y el de
// desvio 0,7 % debe dar alarma con reconciled=false y cobro NO
// bloqueado (el proceso sale 0 con --expect-alarm: HTTP 200).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { reconcileDay, TOLERANCE_PCT } from "./reconcile.mjs";

const dir = path.join(path.dirname(fileURLToPath(import.meta.url)), "fixtures");

test("24 h: drift <= tolerancia y dia reconciliado", () => {
  const doc = JSON.parse(readFileSync(path.join(dir, "reconcile_24h.json"), "utf8"));
  const rep = reconcileDay(doc);
  assert.ok(rep.driftPct <= TOLERANCE_PCT, `drift_pct=${rep.driftPct}`);
  assert.equal(rep.reconciled, true);
});

test("desvio 0,7 %: alarma, reconciled=false y cobro no bloqueado", () => {
  const doc = JSON.parse(readFileSync(path.join(dir, "reconcile_desvio.json"), "utf8"));
  const rep = reconcileDay(doc);
  assert.ok(rep.driftPct > TOLERANCE_PCT, `drift_pct=${rep.driftPct}`);
  assert.equal(rep.reconciled, false);
});
