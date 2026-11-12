---
artifact: epic
id: EP-09
title: Gateway backconnect y selección de exit
owner_layer: gateway
closes: A2-01, A2-02, A2-06, A2-08, injerto 6
estimate_days: 8
risk: high
phase: F4
---

## Objetivo

Matar el S1 A2-01: pool por gateway con localidad declarada. El rendezvous casa cada nodo con el gateway geográficamente más cercano; el HELLO `{bw_mbps, iso2, asn, device_class}` (injerto 6) publica capacidades a Redis; el gateway selecciona solo de su pool — cero copias por byte y P99 < 1,5 s porque la selección nunca cruza shards. Sin pool coincidente: `307` al gateway cuyo pool coincide (+1 RTT de control, nunca un byte copiado).

## Alcance

- HELLO con capacidades → `node:{id}:caps` y `gw:pool:{gw_id}` en Redis.
- Casado rdv→gateway por país (ciudad/ASN si coinciden) y índice geo→gateways para el `307`.
- Selección sticky (`sticky:{t}:{client}:{key}` → node_id con TTL) y rotativa ponderada por `bw_mbps` medido y densidad.
- Topes de streams dinámicos por clase de dispositivo y cuota concurrente por tenant, aplicados en el gateway (A1-17, A2-08).
- Política de conexiones por pérdida medida (1–2 %) sobre las hasta 4 conexiones del nodo (A2-02, carril gateway).
- Deny-list de destino en el CONNECT: 10/8, 172.16/12, 192.168/16, 100.64/10 (A3-X2).
- Drenaje: `gw:drain:{gw_id}` detiene admisiones, GOAWAY drena, el rendezvous re-asigna; egress ≈ 75 Mbps por gateway censado por provedor (dimensionado edge, herencia A2-06).

## Fuera de alcance

- El túnel y la regla de 4 conexiones del lado agente (EP-04); el contrato `POST /api/v1/sessions` lo sirve el backend (EP-08) y el gateway lo ejecuta. La hoja de presupuesto de plano de datos a 100k se escribe en EP-10; aquí se entrega su medición de egress por gateway.

## Hallazgos que cierra

- A2-01 (S1): selección consciente de la localidad gateway↔pool — a 100k (≈400 gateways) el P99 no se fragmenta.
- A2-02 (S2): conexiones guiadas por pérdida medida; el head-of-line de la TCP única se combate con carriles, y su riesgo (R5) se mide en el soak.
- A2-08 (S2): tope de streams dinámico por clase MIPS y TTL corto en pool chico — el negocio deja de topar a 8 sesiones.
- A2-06 (S1 de P2, mandado al ganador): el edge dimensionado — instancias y egress por gateway nombrados y medidos.
- A3-X2 (S2, puerta en el gateway): el cliente no barre la LAN del dueño.

## Criterios de aceptación de la épica

1. Simulación de 1.000 nodos y 4 gateways con `make gw-load`: redirects `307` < 5 % de las selecciones; P99 de selección < 1,5 s medido por k6/vegeta (aserción en informe JSON `p99_ms < 1500`).
2. `redis-cli hgetall node:$ID:caps` responde `{bw_mbps, iso2, asn, device_class}` tras el primer HELLO; `smembers gw:pool:$GW` contiene al nodo y 0 nodos de otro país.
3. Sticky: 50 requests consecutivos del mismo client dentro del TTL → mismo `node_id` en ≥ 95 %; rotativo: 50 requests → ≥ 80 % de `node_id` distintos (script `scripts/gw/stickiness.sh`).
4. `echo CONNECT 192.168.1.1:80 | nc gw 1080` → rechazado (host unreachable); `100.64.0.1:80` rechazado; `8.8.8.8:53` admitido — 0 bytes de la LAN del dueño cruzan el túnel.
5. Drenaje de un gateway con 250 nodos: `gw:drain` → 0 sesiones huérfanas pasados 60 s (conteo `sessions WHERE ended_at IS NULL AND gw_id=$GW` = 0) y el rdv re-asigna el 100 % de los nodos en < 5 min.

## Dependencias

EP-04 (rdv y conexión múltiple), EP-06 (streams y topes del agente), EP-08 (contrato, apikey, Redis inventory). EP-10 consume `gw_id` en `sessions` y el egress censado; EP-12 fija el P99 como SLO.

## Definición de hecho

Un cliente en Madrid sale por un nodo casado al gateway de Madrid el 95 % de las veces sin ningún reenvío inter-gateway; un tenant no puede tumbar la LAN del dueño ni el pool de su clase; y drenar un gateway es invisible para el cliente salvo un RTT.
