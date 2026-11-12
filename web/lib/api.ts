// lib/api.ts — el panel y las paginas publicas leen y escriben SIEMPRE
// contra la API real (EP-11: "todo contra la API real por fetch").
// BOTNET_API_URL es server-side: la credencial del dueño nunca viaja.
export const API_URL =
  process.env.BOTNET_API_URL ?? "http://127.0.0.1:8080";

export type TransparencyNode = {
  slug: string;
  node_id: string;
  status: string;
  country: string;
  device_class: string;
  model: string;
  monthly_gb_cap: number;
  bandwidth_cap_kbps: number;
  gb_month: number;
  consent_date: string;
  leave_code: string;
};

export type ApiError = { status: number; message: string };

export async function apiGet<T>(path: string): Promise<T | ApiError> {
  try {
    // Accept explicito: /t/{slug} sirve HTML por defecto y solo JSON
    // a quien pide application/json; el resto de rutas ya eran JSON.
    const res = await fetch(`${API_URL}${path}`, {
      cache: "no-store",
      headers: { Accept: "application/json" },
    });
    const text = await res.text();
    if (!res.ok) {
      return { status: res.status, message: text };
    }
    return JSON.parse(text) as T;
  } catch (err) {
    return { status: 0, message: String(err) };
  }
}

export async function apiSend<T>(
  method: "POST" | "PATCH" | "DELETE",
  path: string,
  opts: { body?: unknown; apiKey?: string; adminToken?: string } = {},
): Promise<T | ApiError> {
  const headers: Record<string, string> = {};
  if (opts.apiKey) headers["Authorization"] = `Bearer ${opts.apiKey}`;
  else if (opts.adminToken) headers["Authorization"] = `Bearer ${opts.adminToken}`;
  if (opts.body !== undefined) headers["Content-Type"] = "application/json";
  try {
    const res = await fetch(`${API_URL}${path}`, {
      method,
      headers,
      cache: "no-store",
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    });
    const text = await res.text();
    if (!res.ok) {
      return { status: res.status, message: text };
    }
    return (text ? JSON.parse(text) : {}) as T;
  } catch (err) {
    return { status: 0, message: String(err) };
  }
}

export function isApiError(v: unknown): v is ApiError {
  return (
    typeof v === "object" &&
    v !== null &&
    typeof (v as ApiError).status === "number" &&
    typeof (v as ApiError).message === "string"
  );
}

export function fmtMbps(kbps: number): string {
  return (kbps / 1000).toLocaleString("es-ES", { maximumFractionDigits: 2 });
}

export function fmtGB(bytesGB: number): string {
  return bytesGB.toLocaleString("es-ES", { maximumFractionDigits: 2 });
}
