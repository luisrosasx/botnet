// Directorio de transparencia GET /t (EP-11 T-1107): la transparencia
// es un lugar publico con memoria — slugs de la flota sin credencial
// (el device_token jamas asoma) enlazando cada pagina /t/{slug}.
import Link from "next/link";
import { apiGet, isApiError, type TransparencyNode } from "@/lib/api";

export const dynamic = "force-dynamic";

type DirectoryNode = Pick<TransparencyNode, "slug" | "status" | "country" | "model" | "monthly_gb_cap">;

export default async function TransparencyDirectory() {
  const nodes = await apiGet<DirectoryNode[]>("/v1/nodes");
  return (
    <main className="mx-auto max-w-2xl p-8">
      <h1 className="text-2xl font-semibold">Directorio de transparencia</h1>
      <p className="mt-1 text-sm text-slate-500">
        Flota publicada: cada nodo es una URL compartible y ninguno expone su credencial.
      </p>
      {isApiError(nodes) ? (
        <p className="mt-4 text-red-700">
          La API no responde ({nodes.status}): {nodes.message}
        </p>
      ) : (
        <table className="mt-6 w-full text-sm">
          <thead>
            <tr className="border-b text-left">
              <th className="py-1">Nodo</th>
              <th>Estado</th>
              <th>País</th>
              <th>Modelo</th>
              <th>Tope</th>
            </tr>
          </thead>
          <tbody>
            {(nodes ?? []).map((n) => (
              <tr key={n.slug} className="border-b border-slate-100">
                <td className="py-1">
                  <Link className="font-mono text-sky-700 underline" href={`/t/${n.slug}`}>
                    /t/{n.slug}
                  </Link>
                </td>
                <td>{n.status}</td>
                <td>{n.country || "desconocido"}</td>
                <td>{n.model || "sin marca"}</td>
                <td>{n.monthly_gb_cap} GB/mes</td>
              </tr>
            ))}
            {(nodes ?? []).length === 0 && (
              <tr>
                <td colSpan={5} className="py-2 text-slate-500">
                  Sin nodos publicables en la flota.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}
    </main>
  );
}
