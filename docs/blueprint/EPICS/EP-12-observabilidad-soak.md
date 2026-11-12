---
artifact: epic
id: EP-12
title: Observabilidad, SLOs y soak
owner_layer: infra
closes: A2-04, A2-12, A2-17
estimate_days: 8
risk: high
phase: F2
---

## Objetivo

Convertir las puertas del plan en números que corren solos: métricas Prometheus por componente, trazas OpenTelemetry de la sesión (alloc → open → close → flush), heartbeat portador de `rss_kib`/streams/`loss_estimate`/`ver`, y los dos grandes bancos de prueba: fuzz de 72 h del framing H2 y soak de 1.000 reinicios, más el simulador de 100k nodos bajo purge TCP de 60 s.

## Alcance

- Métricas y trazas por componente (`botnet-api`, `botnet-gw`, `botnet-rdv`) con logs JSON y spool.
- SLOs medidos: P99 de apertura < 1,5 s a 250 nodos/gateway; P99 de RSS ≤ 6 MB en flota real; beacon ≤ 120 KB/nodo/día; corte por cuota < 60 s; revocación < 60 s; `streams_saturated` < 10 %.
- Simulación CGNAT: 100k nodos bajo purge TCP de 60 s — inflado de control < 5 % y cero sesiones vivas cortadas por el purge (A2-17, A2-04).
- Fuzz de 72 h del framing en CI sin caída (condición R1) con corpus en `testdata/`.
- Soak de 1.000 reinicios con `pkill` sin huérfanos y un solo pidfile vivo (condición R1/R7); extensión a 7 días en ISP real con pérdida como puerta F6.
- Hoja agregada recalculada y medida en F0: 1.000 × 15 % × 2 Mbps = 300 Mbps, no 3 Gbps (A2-12 — el factor 10 de P3 no se repite aquí).

## Fuera de alcance

- APM ni scraping en el nodo (descartados en el blueprint: RSS que no sobra, puerto escuchante en quien nunca escucha). El drill de DNS pasiva se ejecuta como script de EP-13; aquí vive el SLO que lo consume. El presupuesto a 100k con partidas nombradas es de EP-10; aquí su verificación bajo carga simulada.

## Hallazgos que cierra

- A2-04 (S3) y A2-17 (S2): keepalive < 45 s probado contra purge de 60 s a escala con criterios numéricos, no a fe.
- A2-12 (S2): aritmética agregada medida en F0 — el factor 10 se detecta con script, no con auditoría.
- Condiciones de riesgo R1 (fuzz + soak) y R7 (soak extendido) del veredicto.

## Criterios de aceptación de la épica

1. `make metrics-smoke` → `/metrics` responde en los tres binarios con `botnet_streams_active` y `botnet_rss_kib` presentes; una sesión de test deja ≥ 4 spans (alloc/open/close/flush) queryable por `session_id`.
2. Simulador `scripts/soak/cgnat_purge_100k.sh` (100k conexiones, purge 60 s, 24 h simulados): inflado de control < 5 % vs línea base y 0 sesiones vivas cortadas por el purge (aserción del informe JSON).
3. `make fuzz` → 72 h en CI con 0 caídas y goroutines finales ≤ baseline + 10 %; `make soak-1000` → 1.000 iteraciones `pkill` con 0 huérfanos y `pgrep -f botd | wc -l` = 2 al final de cada ciclo.
4. `node scripts/budget/aggregate.mjs --nodes 1000 --concurrency 0.15 --mbps 2` → imprime `egress_mbps=300` (±20 %); una regresión que reimprima 3.000 hace fallar el test (factor 10 a la vista).

## Dependencias

Consume EP-04 (fuzz de su framing, keepalive), EP-05 (soak de su watchdog), EP-09 (P99 de selección a 250/gw), EP-10 (presupuesto que se verifica). Sus umbrales son las puertas de salida de F2/F3/F6 del blueprint.

## Definición de hecho

Las seis SLO del blueprint se imprimen solas en un informe nightly; el fuzz de 72 h y el soak de 1.000 reinicios están verdes en CI; y el simulador de purge a 100k aprueba el keepalive con números, no con adjetivos.
