---
artifact: epic
id: EP-10
title: Contabilidad, cuotas y facturación
owner_layer: billing
closes: A2-18, A2-19, A2-20, A3-14, A3-36, A2-03, A2-07, A2-13, A2-16
estimate_days: 7
risk: medium
phase: F3
---

## Objetivo

Instaurar el medidor autoritativo único — el nodo es el reloj maestro — con UNA tolerancia ≤ 0,5 % alineada a Stripe (mandato A2-18; fuera el menú 0,5 %/±1 %/5 %). Gateway y NATS son auditoría, nunca base de factura. Alrededor del medidor: cuotas por GB con corte < 60 s, presupuesto de plano de datos con partidas nombradas (injerto 5), retención escalonada, coste de recluta y suelo de volumen por nodo.

## Alcance

- Medidor en el nodo: flush al cerrar sesión, periódico a 15 min, spool a disco cada 60 s; pérdida por crash ≤ 90 s; `flush_id` con dedup (`202 {dedup:true}`).
- `usage` diario (PK day+node_id, retención 12 meses) y `sessions` con retención 30 días — sin rollup por minuto para toda la flota (lección A2-07).
- Cuota: `quotas.gb_allowance`, `tenant:{t}:gb_used:{period}`, corte por `quota:{t}:state=exhausted` + NATS `quota.exhausted` (invalidación de la carrera del cache de 30 s, A3-04); `monthly_gb_cap` topado a 150 GB/nodo y publicado (A2-20).
- Conciliación diaria `usage` ↔ sesiones del gateway ↔ Stripe (webhook firmado, EP-11): desvío > 0,5 % alarma sin bloquear el cobro.
- Presupuesto de plano de datos a 100k con partidas nombradas: gateways, egress por GB vendido, capa de túneles (A2-03, A2-16; lección de factor 10 de A2-12).
- Coste de recluta por exit limpio con churn 15 % (A2-19) y precio de GB por clase de dispositivo (A2-13).
- `leaves` con `reprobe_blocked_until` ≥ 12 meses: el barrido no re-cluta al dueño que se bajó (A3-14).

## Fuera de alcance

- El cobro e2e con Stripe real es puerta de F5 y vive en EP-11 (webhook firmado, panel); aquí la contabilidad que concilia. El GOAWAY por cuota del plano de datos está en EP-06; aquí su disparador. La TASK de `bytes_delta` conciliado por hora es de EP-14 (canal de release).

## Hallazgos que cierra

- A2-18 (S2): medidor autoritativo con una sola tolerancia y reloj maestro nombrado.
- A2-03 / A2-16 (S2): el egress y el edge nombrados en la hoja — la victoria de 0,185 USD deja de estar «en disputa, no probada».
- A2-07 (S2): retención escalonada minuto/hora/día sin 144 M filas diarias.
- A2-13 (S2): GB por clase con tope sembrado por iperf3 — la clase débil se precia, no se regala.
- A2-19 (S3) y A2-20 (S3): coste de recluta y tope de volumen por nodo gobernan la economía.
- A3-14 (S2): opt-out con memoria — 12 meses de bloqueo de re-clutaje.
- A3-15 (S3, absorbido): fuente canónica el nodo, NATS auditoría, dedup por `flush_id`.
- A1-05 (S4, absorbido): sin spool diario a hora fija — flush por sesión/15 min con jitter.

## Criterios de aceptación de la épica

1. Transferencia conocida de 1 GB por una sesión de test: `|bytes_nodo − 1 GB| / 1 GB ≤ 0,5 %` (aserción del test de integración; `make metering-test` → `drift_pct=…(≤0.5)`).
2. `kill -9` del agente a mitad de sesión: bytes perdidos ≤ 90 s (diff entre contadores del gateway y el `usage` reconstruido); reenvío del mismo `flush_id` → `202 {dedup:true}` y suma de `usage` invariante.
3. Cuota agotada (fixture `gb_used > gb_allowance`): sesión nueva → `402` en < 60 s (cronómetro del test); GOAWAY con `closed_reason='quota'` libera el stream y sus contadores al instante.
4. `make budget` → imprime la hoja con 3 partidas nombradas a 1.000 y 100.000 nodos; asserts: control+gateways ≤ 0,25 USD/nodo/mes, egress presente en USD/mes, margen bruto ≥ 50 % a 100k; sin la hoja medida, el pipeline F0 falla (exit ≠ 0).
5. `psql -c "SELECT count(*) FROM usage WHERE day=current_date"` ≤ número de nodos vivos del día (diario, no por minuto); `psql -c "\d sessions"` refleja retención 30 días (política/pardot) y `usage` 12 meses.

## Dependencias

EP-06 entrega contadores por sesión y corte de plano de datos; EP-08 entrega `sessions`/`usage`/`quotas`/`leaves`; EP-09 entrega `gw_id` y el egress censado; EP-07 siembra `bandwidth_cap_kbps`. EP-11 consume conciliación y webhook.

## Definición de hecho

Un cobro disputado a 60 días se responde con el `usage` diario y las sesiones de 30 días sin una fila por minuto; un crash no pierde > 90 s de bytes; un duplicado de flush no duplica cargo; y la hoja de plano de datos a 100k sostiene un margen bruto ≥ 50 % o congela el plan.
