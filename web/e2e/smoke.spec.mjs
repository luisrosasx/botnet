// smoke e2e del panel (EP-11 T-1105): paginas reales contra el
// backend y el front en marcha. Con E2E_WEB_URL/E2E_SLUG defined
// pide de verdad; sin ellas, test omitido con la limitacion anotada
// (el binario de CI monta el stack de deploy/docker-compose.yml).
// Runner: node --test (playwright no instalado en este host — mismo
// check que anotan EP-01/EP-04 para sus binarios ausentes).
import { test } from "node:test";
import assert from "node:assert/strict";

const WEB = process.env.E2E_WEB_URL ?? "";
const API = process.env.BOTNET_API_URL ?? process.env.NEXT_PUBLIC_BOTNET_API_URL ?? "http://127.0.0.1:8080";
const SLUG = process.env.E2E_SLUG ?? "";

async function get(url) {
  const res = await fetch(url, { cache: "no-store" });
  return { status: res.status, body: await res.text() };
}

test("root del panel responde", { skip: !WEB ? "E2E_WEB_URL ausente" : false }, async () => {
  const page = await get(`${WEB}/panel/usage`);
  assert.equal(page.status, 200);
  assert.match(page.body, /Uso en vivo/);
});

test("pagina de transparencia con <table> y leave_code", { skip: !WEB || !SLUG ? "E2E_WEB_URL/E2E_SLUG ausentes" : false }, async () => {
  const page = await get(`${WEB}/t/${SLUG}`);
  assert.equal(page.status, 200);
  assert.ok(page.body.includes("<table"), "falta la tabla del dueno");
  assert.match(page.body, /leave_code=[A-Za-z0-9]{8}/);
  assert.equal(page.body.includes("device_token"), false, "el device_token asoma en la pagina");
});

test("api real: /v1/panel/usage sin key es 403", { skip: !process.env.E2E_API_LIVE ? "E2E_API_LIVE ausente" : false }, async () => {
  const res = await fetch(`${API}/v1/panel/usage`, { cache: "no-store" });
  assert.equal(res.status, 403);
});
