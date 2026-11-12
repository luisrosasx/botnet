// Baja del dueño desde su URL (EP-11 T-1101/T-1102, method owner_url):
// el enlace leave_code={8} de la pagina de transparencia aterriza aqui
// y el servidor reenvia el POST a la API real. Sin JS obligatorio: un
// click (o un GET) drena el nodo.
import Link from "next/link";
import { API_URL } from "@/lib/api";

export const dynamic = "force-dynamic";

const CODE_RE = /^[A-Za-z0-9]{8}$/;

type LeaveResult = { node_id?: string; left?: boolean; error?: string };

export default async function LeavePage({
  params,
  searchParams,
}: {
  params: Promise<{ slug: string }>;
  searchParams: Promise<{ leave_code?: string }>;
}) {
  const { slug } = await params;
  const { leave_code } = await searchParams;
  const code = leave_code ?? "";

  const nodeRes = await fetch(`${API_URL}/t/${slug}`, { cache: "no-store" });
  if (!nodeRes.ok) {
    return (
      <main className="mx-auto max-w-xl p-8">
        <h1 className="text-2xl font-semibold">Baja BOT.NET</h1>
        <p className="mt-4 text-red-700">Slug desconocido: {slug}</p>
      </main>
    );
  }
  const node = (await nodeRes.json()) as { node_id: string };

  let ok = false;
  let message = "";
  if (!CODE_RE.test(code)) {
    message = "El enlace de baja exige un leave_code de 8 caracteres.";
  } else {
    const res = await fetch(
      `${API_URL}/v1/nodes/${node.node_id}/leave?code=${encodeURIComponent(code)}`,
      { method: "POST", cache: "no-store" },
    );
    const body = (await res.json().catch(() => ({}))) as LeaveResult;
    ok = res.ok;
    message = ok
      ? "Nodo en retirada: drena y se baja en menos de 24 h."
      : (body.error ?? `codigo rechazado (HTTP ${res.status})`);
  }

  return (
    <main className="mx-auto max-w-xl p-8">
      <h1 className="text-2xl font-semibold">Baja del nodo</h1>
      <p className={`mt-4 ${ok ? "text-emerald-700" : "text-red-700"}`}>
        {message}
      </p>
      <p className="mt-6 text-sm">
        <Link className="text-sky-700 underline" href={`/t/${slug}`}>
          Volver a la transparencia del nodo
        </Link>
      </p>
    </main>
  );
}
