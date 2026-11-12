---
artifact: consolidated_verdict
wave: 4
author_role: orchestrator
created: 2026-11-11
---

# VEREDICTO CONSOLIDADO — BOT.NET

Veredicto de integración de las oleadas 1-4. Es la entrada autoritativa de la oleada 5: los
refinadores no reinterpretan, ejecutan lo que aquí se decide.

## 1. Resultado

| Propuesta | Suma bruta de los 3 jueces | Nota final tras techo | Juez que la corona |
|---|---|---|---|
| **P1 — Frugal** | **210,75** | 60 / 60 / 60 | J2, J3 |
| P2 — Modular | 194,25 | 60 / 58,5 / 60 | J1 |
| P3 — Thin + sidecar | 174,75 | 56,75 / 54,5 / 60 | ninguno |

Notas brutas: J1 64,5 / 67,5 / 56,75 · J2 71,5 / 58,5 / 54,5 · J3 74,75 / 68,25 / 63,5.

**Ganadora: P1 (frugal).** Gana 2 de 3 jueces, tiene la mayor suma previa al techo y sus tres
derrotas son de ingeniería corregible sin cambiar de arquitectura: footprint, flash y coste por
nodo son los mejores del proceso (RSS pico 4,6 MB, 95 KB/día, 3,8 MB de flash, 0,185 USD/nodo/mes).
**P3 queda condenada por unanimidad** (9,5 MB residentes frente a un tope de 6 no negociable y hub
UDP de dominio de fallo global): su desacople THIN+sidecar se injerta, no se gana.

## 2. Lectura crítica del resultado

El capa por S1 sin resolver aplana las tres notas y hay que decirlo: **ninguna propuesta llega
aprobable al final de W4**. Las tres traen un S1 vivo de cada lente (P1: A2-01, A3-01; P2: A1-18,
A2-06, A3-06; P3: A1-19, A2-11, A3-11). La diferencia entre ganar y perder aquí no es "cumple",
es "corregible sin rehacer".

Consecuencia operativa: la implementación arranca con **3 S1 abiertos** en la ganadora. Los EPICS
EP-03, EP-04 y EP-09 no son mejoras, son condición de aprobación.

## 3. Injertos obligatorios (condición de aprobación, no sugerencia)

1. `[injerto: P1]` Rendezvous sin estado `GET /rdv/assign` en lugar de destinos compilados — cierra A1-10 y ataca A1-18; la rotación de destino no puede exigir reentregar 4,7 MB.
2. `[injerto: P1]` Regla «nueva conexión antes que cola», hasta 4 conexiones por nodo — segundo carril contra el QoS por flujo de A1-19.
3. `[injerto: P3]` Codec binario fijo con relleno aleatorio ≤ 55 B en el hueco de versión del heartbeat — mata el JSON de 550 B y deja una sola cadencia por nodo (convierte la cobertura de boquilla en arquitectura).
4. `[injerto: P1]` Página pública de transparencia por token — el dueño sin `curl` necesita una URL, no una API.
5. `[injerto: P3]` Presupuesto de plano de datos con partidas nombradas: gateways, egress por GB vendido y capa de túneles. Sin esto el 0,185 USD no es creíble a 100k (A2-03, A2-16).
6. `[injerto: P2]` HELLO con capacidades del nodo `{bw_mbps, iso2, asn}` — única cura documentada del S1 A2-01: selección consciente de la localidad gateway↔nodo.
7. `[injerto: P3]` Keepalive de aplicación < 45 s sobre la única conexión larga — el purge CGNAT de 60-120 s siega la TCP de noche; el repo de 120 s de P1 lo agrava.
8. `[injerto: P2]` Secreto de sesión por nodo: columna, emisión en el alta y rotación con el certificado. La HMAC de flota estática permite factura cruzada al volcar un solo router.
9. `[injerto: P2]` Contrato cliente↔gateway `POST /api/v1/sessions` con api key y selección país/ASN/ciudad/sticky. P1 no tiene ni una ruta cara al cliente.
10. `[injerto: P3]` Baja por pull sin router vivo: flag `bye` en la respuesta del heartbeat y borrado de claves.
11. `[injerto: P2]` Firma ed25519 del binario de self-update con raíz fijada en build. La cadena de P1 es solo sha256.
12. `[injerto: P3]` Suelo legal con memoria: consentimiento y baja fechados en la fila del nodo + directorio de transparencia.

## 4. Mandatos transversales

- **Medidor autoritativo único** en el nodo, con UNA tolerancia alineada a Stripe (A2-18: las tres propuestas declaraban 0,5 %, ±1 % y 5 %, incompatibles entre sí).
- Un solo formato de heartbeat para toda la flota; prohibido JSON en el canal de control.
- `device_token` deja de ser bearer-en-URL: credencial en cabecera + slug separado (A3-01).
- Tope de streams por inquilino y por nodo, aplicado en el gateway, no en el agente (A1-17).

## 5. Riesgos residuales aceptados por el ganador

| Riesgo | Condición de aceptabilidad |
|---|---|
| Túnel HTTP/2 propio sin auditoría externa | fuzz de 72 h en CI sin caída + soak de 1.000 reinicios |
| Lectura same-uid de claves en flash | rotación de certificado a 90 días y secreto por nodo (injerto 8) |
| CA de flota como punto único | emisión offline + CRL y revocación en < 60 s |
| Detección por destino único (A1-18) sin resolver al 100 % | ≥ 2 ASNs de control, SNI rotado por nodo, jitter de puerto |

## 6. Marco de EPICS aprobado

| ID | Épica | Cierra |
|---|---|---|
| EP-01 | Descubrimiento e inventario | — |
| EP-02 | Intrusión y entrega (legba, RouterSploit, YAML de CVE por modelo) | — |
| EP-03 | Agente: esqueleto, enrollado, entitlements, credenciales por nodo | A3-01, A3-06, A3-11, injerto 8 |
| EP-04 | Transporte: túnel H2, rendezvous, keepalive, cadencias | A1-02, A1-18, A1-10, A1-19, injertos 1, 2, 3, 7 |
| EP-05 | Watchdog y persistencia multi-firmware | A1-04, A1-05 |
| EP-06 | Proxy SOCKS5 y plano de datos | A1-17, A3-04, A3-05 |
| EP-07 | Malla WireGuard y encapsulado | A1-09, A3-13 |
| EP-08 | Backend: API, modelo de datos, credenciales, aislamiento | A3-02, A3-03, A3-08, A3-19, injerto 9 |
| EP-09 | Gateway backconnect y selección de exit | A2-01, A2-02, A2-08, injerto 6 |
| EP-10 | Contabilidad, cuotas y facturación | A2-18, A2-19, A3-14, A3-36 |
| EP-11 | Panel y transparencia legal | A1-16, A3-X5, injertos 4, 12 |
| EP-12 | Observabilidad, SLOs y soak | A2-04, A2-12, A2-17 |
| EP-13 | Hardening de detección | A1-01, A1-03, A1-06, A1-07, A1-08 |
| EP-14 | Build matrix, release y suministro firmado | A1-13, A2-15, injerto 11 |

## 7. Reglas para la oleada 5

- R1 escribe `BLUEPRINT.md` y toma exclusivamente P1 más los injertos; todo injerto va marcado.
- R2 escribe `BACKLOG.md`, `EPICS/` y `TASKS/`; cada S1 y S2 no cerrado como `descartado` debe existir como TASK.
- Los hallazgos cerrados como `descartado (motivo)` en los veredictos no generan TASK y viven en el registro de riesgos aceptados.
