"use client";

// Panel del tenant (EP-11 T-1105): uso, api keys, cuotas y facturas
// contra la API real por fetch con `api key` (Bearer). La en claro
// vive en localStorage del navegador del tenant; sin secretos en el
// build (ver .env.example — 0 lineas sk_live).
import { useEffect, useState } from "react";
import Link from "next/link";

const API = process.env.NEXT_PUBLIC_BOTNET_API_URL ?? "http://127.0.0.1:8080";
const SECTIONS = ["usage", "api-keys", "quotas", "invoices"] as const;
type Section = (typeof SECTIONS)[number];

const TITLES: Record<Section, string> = {
  usage: "Uso en vivo e histórico",
  "api-keys": "API keys",
  quotas: "Cuotas del periodo",
  invoices: "Facturas del periodo",
};

function isSection(s: string): s is Section {
  return (SECTIONS as readonly string[]).includes(s);
}

async function apiFetch<T>(path: string, key: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API}${path}`, {
    ...init,
    headers: {
      ...(init?.headers ?? {}),
      ...(key ? { Authorization: `Bearer ${key}` } : {}),
      ...(init?.body ? { "Content-Type": "application/json" } : {}),
    },
    cache: "no-store",
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json() as Promise<T>;
}

export default function PanelSection({ section }: { section: string }) {
  const active: Section = isSection(section) ? section : "usage";
  const [apiKey, setApiKey] = useState("");
  const [data, setData] = useState<unknown>(null);
  const [error, setError] = useState("");
  const [label, setLabel] = useState("");

  useEffect(() => {
    const stored = window.localStorage.getItem("botnet.panel.apikey") ?? "";
    setApiKey(stored);
    if (!stored) return;
    const path =
      active === "usage"
        ? "/v1/panel/usage?days=30"
        : active === "api-keys"
          ? "/v1/panel/api-keys"
          : active === "quotas"
            ? "/v1/panel/quotas"
            : "/v1/panel/invoices?days=30";
    apiFetch(path, stored).then(setData).catch((e) => setError(String(e.message ?? e)));
  }, [active, apiKey]);

  async function saveKey(next: string) {
    window.localStorage.setItem("botnet.panel.apikey", next);
    setData(null);
    setError("");
    setApiKey(next);
  }

  async function createKey() {
    try {
      const out = await apiFetch<{ api_key: string }>("/v1/panel/api-keys", apiKey, {
        method: "POST",
        body: JSON.stringify({ label: label || "panel", mode: "rotating" }),
      });
      setLabel("");
      setError(out.api_key ? `Nueva clave (solo visible una vez): ${out.api_key}` : "");
      const list = await apiFetch("/v1/panel/api-keys", apiKey);
      setData(list);
    } catch (e) {
      setError(String(e));
    }
  }

  async function saveQuota(form: FormData) {
    try {
      await apiFetch("/v1/panel/quotas", apiKey, {
        method: "PATCH",
        body: JSON.stringify({
          gb_allowance: Number(form.get("gb_allowance")),
          concurrent_limit: Number(form.get("concurrent_limit")),
          mode: String(form.get("mode")),
        }),
      });
      setError("");
      const fresh = await apiFetch("/v1/panel/quotas", apiKey);
      setData(fresh);
    } catch (e) {
      setError(String(e));
    }
  }

  const record = (data ?? {}) as Record<string, unknown>;
  const history = Array.isArray(record.history) ? record.history : [];
  const invoices = Array.isArray(record.invoices) ? record.invoices : [];
  const keys = Array.isArray(data) ? data : [];

  return (
    <main className="mx-auto max-w-3xl p-8">
      <h1 className="text-2xl font-semibold">{TITLES[active]}</h1>
      <nav className="mt-2 flex gap-4 text-sm">
        {SECTIONS.map((s) => (
          <Link key={s} href={`/panel/${s}`} className={s === active ? "font-semibold text-sky-800" : "text-sky-700 underline"}>
            {s}
          </Link>
        ))}
      </nav>

      <div className="mt-4 flex gap-2">
        <input
          className="w-full rounded border border-slate-300 px-2 py-1 text-sm"
          placeholder="API key del tenant (Bearer)"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
        />
        <button className="rounded bg-slate-900 px-3 py-1 text-sm text-white" onClick={() => saveKey(apiKey)}>
          Usar
        </button>
      </div>

      {error && <p className="mt-2 text-sm text-amber-800">{error}</p>}
      {!apiKey && <p className="mt-2 text-sm text-slate-500">Pega tu api key para hablar con la API.</p>}

      {active === "usage" && (
        <section className="mt-6">
          <p className="text-sm text-slate-600">
            En vivo (Redis): {String(record.gb_used_live ?? "—")} GB de {String(record.gb_allowance ?? "—")} GB del
            periodo {String(record.period ?? "")}
          </p>
          <table className="mt-4 w-full text-sm">
            <thead>
              <tr className="border-b text-left">
                <th className="py-1">Día</th>
                <th>GB</th>
              </tr>
            </thead>
            <tbody>
              {history.map((h, i) => (
                <tr key={i} className="border-b border-slate-100">
                  <td className="py-1">{String((h as Record<string, unknown>).day)}</td>
                  <td>{Number((h as Record<string, unknown>).gb).toFixed(2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}

      {active === "api-keys" && (
        <section className="mt-6">
          <div className="flex gap-2">
            <input
              className="rounded border border-slate-300 px-2 py-1 text-sm"
              placeholder="label de la clave nueva"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
            <button className="rounded bg-slate-900 px-3 py-1 text-sm text-white" onClick={createKey}>
              Alta
            </button>
          </div>
          <ul className="mt-4 text-sm">
            {keys.map((k, i) => {
              const kk = k as Record<string, unknown>;
              return (
                <li key={i} className="border-b border-slate-100 py-1">
                  #{String(kk.api_key_id)} {String(kk.label)} — {String(kk.mode)} · {String(kk.status)} · creada{" "}
                  {String(kk.created_at)}
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {active === "quotas" && (
        <form
          className="mt-6 flex flex-col gap-2 text-sm"
          onSubmit={(e) => {
            e.preventDefault();
            void saveQuota(new FormData(e.currentTarget));
          }}
        >
          <label>
            gb_allowance{" "}
            <input className="ml-2 rounded border border-slate-300 px-2 py-1" name="gb_allowance" defaultValue={Number(record.gb_allowance ?? 50)} />
          </label>
          <label>
            concurrent_limit{" "}
            <input className="ml-2 rounded border border-slate-300 px-2 py-1" name="concurrent_limit" defaultValue={Number(record.concurrent_limit ?? 50)} />
          </label>
          <label>
            modo{" "}
            <select className="ml-2 rounded border border-slate-300 px-2 py-1" name="mode" defaultValue={String(record.mode ?? "rotating")}>
              <option value="rotating">rotativo</option>
              <option value="sticky">sticky</option>
            </select>
          </label>
          <button className="mt-2 w-24 rounded bg-slate-900 px-3 py-1 text-sm text-white">Guardar</button>
        </form>
      )}

      {active === "invoices" && (
        <section className="mt-6 text-sm">
          <p className="text-slate-600">Cliente Stripe: <span className="font-mono">{String(record.stripe_customer_id ?? "—")}</span></p>
          <table className="mt-4 w-full">
            <thead>
              <tr className="border-b text-left">
                <th className="py-1">Día</th>
                <th>GB</th>
                <th>Coste</th>
                <th>Reconciliada</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map((inv, i) => {
                const r = inv as Record<string, unknown>;
                return (
                  <tr key={i} className="border-b border-slate-100">
                    <td className="py-1">{String(r.day)}</td>
                    <td>{Number(r.gb).toFixed(2)}</td>
                    <td>{String(r.cost_minor)} {String(r.currency)}</td>
                    <td>{r.reconciled ? "sí (webhook firmado)" : "no"}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </section>
      )}
    </main>
  );
}
