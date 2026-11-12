// Ruta del panel (EP-11 T-1105): secciones usage | api-keys | quotas |
// invoices — el cuerpo es cliente y habla con la API real por fetch
// con la api key del tenant.
import PanelSection from "./panel-section";

export default async function PanelPage({
  params,
}: {
  params: Promise<{ section: string }>;
}) {
  const { section } = await params;
  return <PanelSection section={section} />;
}
