// Pagina publica del dueño GET /t/{slug} (EP-11 T-1101): una URL, no
// una API — el dueño sin curl abre este HTML, que el servidor renderiza
// desde la JSON de botnet-api. La credencial (device_token) jamas asoma.
import { notFound } from "next/navigation";
import Link from "next/link";
import {
  apiGet,
  isApiError,
  fmtGB,
  fmtMbps,
  type TransparencyNode,
} from "@/lib/api";

export const dynamic = "force-dynamic";

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <tr className="border-b border-slate-200 last:border-0">
      <th scope="row" className="py-2 pr-4 text-left font-medium text-slate-600">
        {label}
      </th>
      <td className="py-2 text-slate-900">{value}</td>
    </tr>
  );
}

export default async function TransparencyPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const node = await apiGet<TransparencyNode>(`/t/${slug}`);
  if (isApiError(node)) {
    if (node.status === 404) notFound();
    return (
      <main className="mx-auto max-w-xl p-8">
        <h1 className="text-2xl font-semibold">Transparencia BOT.NET</h1>
        <p className="mt-4 text-red-700">
          La API no responde ({node.status}): {node.message}
        </p>
      </main>
    );
  }

  return (
    <main className="mx-auto max-w-xl p-8">
      <h1 className="text-2xl font-semibold">
        Nodo <span className="font-mono">{node.slug}</span>
      </h1>
      <p className="mt-1 text-sm text-slate-500">
        Transparencia del dueño — sin cuentas, sin apps, sin credenciales en
        la URL.
      </p>
      <table className="mt-6 w-full text-sm">
        <tbody>
          <Row label="Flota" value={node.status} />
          <Row label="Modelo" value={node.model || "sin marca"} />
          <Row label="Clase" value={node.device_class} />
          <Row label="País" value={node.country || "desconocido"} />
          <Row label="Tope de ancho" value={`${fmtMbps(node.bandwidth_cap_kbps)} Mbps`} />
          <Row label="Tope mensual" value={`${node.monthly_gb_cap} GB/mes`} />
          <Row label="GB del mes" value={`${fmtGB(node.gb_month)} GB`} />
          <Row label="Consentimiento" value={node.consent_date} />
          <Row
            label="Baja"
            value={
              <a
                className="text-sky-700 underline"
                href={`/t/${node.slug}/leave?leave_code=${node.leave_code}`}
              >
                Darse de baja (leave_code={node.leave_code})
              </a>
            }
          />
        </tbody>
      </table>
      <p className="mt-6 text-sm">
        <Link className="text-sky-700 underline" href="/t">
          Volver al directorio de transparencia
        </Link>
      </p>
    </main>
  );
}
