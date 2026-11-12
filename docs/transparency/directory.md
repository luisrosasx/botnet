# Directorio de transparencia (política pública)

Versión 1.0 · 2026-02 · EP-11 T-1107

La transparencia de esta red no es una página suelta: es un lugar público
con memoria. Tres piezas la sostienen:

1. **El directorio `/t`** — lista los nodos de la flota publicados (slug,
   estado, país, modelo y tope mensual) sin exponer jamás el
   `device_token` de nadie. Cada fila enlaza la página del nodo.
2. **La página por nodo `/t/{slug}`** — contenido público: flota a la que
   pertenece, clase y modelo del aparato, tope de `150 GB/mes` (o el que
   ese nodo tenga pactado), consumo del mes y el consentimiento fechado
   con que el dueño entró. Desde ella se puede salir con
   `/t/{slug}/leave?leave_code=XXXXXXXX`.
3. **Las promesas de baja**, enlazadas desde cada página.

## Bloqueo de re-clutaje tras la baja (promesa versionada)

Cuando un dueño se baja (vía HTTP, panel, `owner_url` o `leave_code`),
su nodo queda marcado con `optout_at` y **no puede volver a ser reclutado
durante al menos 12 meses**: ninguna puerta de descubrimiento puede
re-clutarlo antes de `optout_at + 12 meses` sin un consentimiento nuevo
del dueño.

Esta promesa no es decorativa: la implementación es la guardia de la
T-1007 — `make reprobe-guard` (`scripts/discovery/reprobe_guard.sh`) —
que audita sobre el DDL real que existan 0 filas con
`enrolled_at > optout_at` dentro de la ventana de 12 meses y, para
demostrar que detecta, inyecta una violación y exige encontrarla antes
de dar el OK.

## Qué se publica y qué no

| Publicado en `/t` y `/t/{slug}` | Nunca público |
| --- | --- |
| slug (12 chars), estado, país, clase/modelo | `device_token` |
| tope `GB/mes` y consumo del mes | IP pública (se purga al cumplir el plazo RGPD) |
| consentimiento fechado y fecha de baja | quotas internas de facturación |

El directorio y las páginas se sirven por SSR desde `web/` contra la API
pública (`GET /v1/nodes` para el directorio y `GET /t/{slug}` por nodo,
esta última con limitación de 60 peticiones por minuto por IP para que
nadie escarbe la flota en bucle).
