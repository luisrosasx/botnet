---
artifact: blueprint
project: BOT.NET
scope: panel
created: 2026-11-18
---

# Blueprint — panel de dispositivos y motor de descubrimiento

Documento de diseño del panel de administración de los dispositivos y del
cierre del motor que los descubre. Se construye en cinco oleadas de swarm
(tres propuestas, tres defensas, tres ataques, tres veredictos y dos
refinadores) y cada hallazgo acaba en una task del backlog o en una decisión
escrita aquí.

Reglas de la casa que este documento NO cambia: el panel es de solo lectura,
todo vive en local, no hay Stripe ni GitHub, y ninguna cifra del panel es
fuente de verdad: la verdad es la base, el panel la enseña.

## 1. Problema y alcance

Hoy hay dispositivos enrolados y un servicio que los descubre, pero no hay
una sola pantalla donde verlos: la única forma de saber qué hay es `psql`.
Y el descubrimiento no explora solo: cada barrido se lanza a mano, así que la
flota no crece si nadie mira.

Alcance: un panel que lista todos los dispositivos con su IP, su IP de salida,
su estado y su última vez visto, y un motor que explora por sí mismo con
crecimiento exponencial medido. Fuera de alcance: facturación, PRs, acciones
sobre el dispositivo (revocar, cortar) y cualquier cosa que escriba en `nodes`
desde el panel.

## 2. Inventario de partida

- `nodes`: `node_id`, `slug`, `public_ip`, `status`, `device_class`, `arch`,
  `last_seen_at`, `optout_at`, `enrolled_at`.
- `node_heartbeats`: latido por nodo.
- `recruit_candidates`: embudo de descubrimiento con `created_at` y `state`.
- `internal/recruit`: planificador, presupuesto con `Grant`, oleadas.
- `cmd/botnet-probe`: barrido real (`--live`) y tope de SYN por /24.
- `web/`: Next ya montado, con `app/panel` y `lib/api.ts`.
- `internal/backend`: auth de administración por `API_ADMIN_TOKEN`.

## 3. Modelo del panel

<!-- w1/w3: consulta, paginación y contraste de recuento -->

## 4. API del panel

<!-- w1/w2: rutas, JSON, errores y qué pasa con la base caída -->

## 5. Interfaz

<!-- w1/w3: columnas, estados, vacío contra error, virtualización -->

## 6. Autenticación

<!-- w1/w2: token de solo lectura, comparación en tiempo constante -->

## 7. Motor de descubrimiento

<!-- w1/w2/w3: cierre del bucle cola → barrido → presupuesto -->

## 8. Exponencialidad medida

<!-- w3: definición de la métrica, ventanas y anti-doble-conteo -->

## 9. Reputación y ritmo

Origen único con sonda RBL y pausa de 48 h; tope de SYN por /24; subida de
clase solo con ratio efectivo, éxito y bajas delante.

## 10. Observabilidad

Métricas `botnet_panel_*` y `botnet_recruit_*`: edad del latido, filas
servidas, desfase entre base y panel, conexiones del stream, decisiones del
planificador con su motivo.

## 11. Riesgos y mitigaciones

<!-- w2/w3: ordenados por gravedad -->

## 12. Decisiones cerradas

<!-- w4: veredictos aceptados, con su injerto y su task -->

## Injertos

<!-- Los veredictos marcan aquí `injerto: V1..V3` sobre las secciones que
     cambian. El mínimo que exige la puerta es 12. -->
