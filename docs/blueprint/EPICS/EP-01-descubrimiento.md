---
artifact: epic
id: EP-01
title: Descubrimiento e inventario
owner_layer: infra
closes: —
estimate_days: 4
risk: low
phase: F1
---

## Objetivo

Alimentar el embudo de reclutamiento: una tabla `discovered` con candidatos reales (routers, cámaras, NAS) puntuados por modelo/ASN/país, alimentada por Shodan API y barrido propio con masscan, y clasificada con zgrab2. Sin inventario no hay intrusión (EP-02) ni alta (EP-03).

## Alcance

- Migración inicial `discovered` (ip, port, service, vendor, model, asn, country, last_seen_at, UNIQUE (ip, port)).
- Ingesta Shodan API por lotes con upsert; barrido masscan propio sobre top-puertos (23, 80, 8080, 554, 32210) de ASNs candidatos.
- Clasificación de banner con zgrab2 (router / cámara / NAS / ruido).
- Consulta de selección de candidatos que respeta `leaves.reprobe_blocked_until` (no re-clutar al dueño que se bajó) y dedupe por /24.

## Fuera de alcance

- Intrusión y entrega (EP-02); el YAML de CVEs vive en `scripts/intrusion/`, no aquí.
- Ningún código en el nodo: descubrimiento 100 % servidor. No hay que justificar menos de 6 tasks porque todas son reales: 6 tasks cubren esquema, 2 fuentes de ingesta, clasificación, informe y consulta de selección.

## Hallazgos que cierra

Ninguno del veredicto (marco sección 6: `—`). Aporta cobertura indirecta a A3-14 (el barrido no re-cluta sin consentimiento) vía `reprobe_blocked_until` y a A2-19 (el coste de recluta nace de un barrido medible).

## Criterios de aceptación de la épica

1. `psql -c "\d discovered"` → tabla existe con `UNIQUE (ip, port)` y columnas `service, model, asn, country, last_seen_at`.
2. `time make shodan-ingest DRY=1 FIXTURE=testdata/shodan_1k.json` → 1.000 resultados upsertados en < 60 s, 0 filas duplicadas (`SELECT count(*) - count(DISTINCT (ip,port)) FROM discovered;` = 0).
3. `scripts/discovery/inventory_report.sh` → informe con top-20 ASN y conteo de candidatos; 1M IPs barridas por masscan en < 25 min a < 5 Mbps sostenidos (medición `iftop` en runbook).
4. `scripts/discovery/select_candidates.sh LIMIT=100` → devuelve > 0 candidatos y 0 con `optout_at` en los últimos 12 meses (fixture leaves).

## Dependencias

Sin dependencias internas: es la primera épica. Su salida (candidatos clasificados) es la entrada de EP-02.

## Definición de hecho

Un operador puede lanzar el pipeline `shodan-ingest → masscan-sweep → zgrab2-grab → inventory_report → select_candidates` sin intervención manual, y la tabla `discovered` responde a la pregunta «¿cuántos candidatos limpios por país/ASN me quedan?» en < 5 s.
