---
artifact: epic
id: EP-06
title: Proxy SOCKS5 y plano de datos
owner_layer: proxy
closes: A1-17, A3-04, A3-05, A2-14, A3-12
estimate_days: 6
risk: medium
phase: F2
---

## Objetivo

Poner el SOCKS5 embebido (gost v3, build tags) en `127.0.0.1` del nodo: cada sesión del gateway es un stream H2 de la conexión existente; el nodo nunca escucha (CGNAT por construcción). El agente ejecuta topes técnicos (buffers, bucket 2–5 Mbps, tope de streams) y la auth local por HMAC sin RTT a Redis.

## Alcance

- SOCKS5 con auth interna en loopback; ningún puerto escuchante en interfaz externa.
- `bot-session: HMAC(node_secret, {client_id, tenant_id, session_id, node_id})` con TTL 30 s validado en el agente antes de aceptar bytes (A2-14, A3-12: sin Redis en el path, ventana corta anti-replay).
- Contadores por stream; el SOCKS5 interno no traspasa auth entre sesiones.
- Token bucket 2–5 Mbps en el agente (el dueño no nota el router lento); buffers fijos de 8 KiB: el tenant ruidoso satura streams primero, bytes después y heap al final.
- Admisión = mín(150 streams nominales, cuota concurrente del tenant, ≤ 8 tenants activos por nodo) — el tope de inquilinos nace aquí y lo aplica el gateway (EP-09).
- Corte por cuota en el plano de datos: GOAWAY con `closed_reason='quota'` libera stream y contadores al instante.

## Fuera de alcance

- La selección de exit y el pool (EP-09); el medidor autoritativo y su spool (EP-10); el filtro de destino del CONNECT es del gateway (EP-09, A3-X2). El riesgo A3-05 (dueño same-uid lee claves 0600) queda aceptado y documentado con laTASK de coste 0 (cifrado bajo secreto de hardware) — por eso esta épica lo «cierra» como riesgo documentado, no como código.

## Hallazgos que cierra

- A1-17 (S1): tope de ≤ 8 inquilinos concurrentes por nodo desacoplado del tope técnico de streams; la densidad vista por el destino deja de ser de datacenter.
- A3-04 (S2): separación capacidad de nodo vs cuota de tenant; la carrera del cache de 30 s la absorbe la tolerancia y la invalida NATS (lado cuota: EP-10).
- A3-05 (S2): lectura same-uid aceptada y documentada; exposición topada por rotación de 90 días.
- A2-14 (S3) y A3-12 (S2): HMAC validado localmente con TTL 30 s — sin RTT a Redis en la auth y sin replay fuera de ventana.

## Criterios de aceptación de la épica

1. `curl -s --socks5-hostname gw:port https://example.com` sobre el túnel → `200` y `ss -ltn` en el nodo no lista ningún puerto en 0.0.0.0 (el nodo no escucha).
2. 50 streams concurrentes sobre UNA conexión H2 durante 10 min en QEMU clase B: `ps -o rss=` ≤ 4,6 MB y latencia p95 de un stream testigo ≤ 2× baseline sin carga.
3. Token de sesión con HMAC forjado → 0 bytes transferidos y stream cerrado con `closed_reason` de auth; token válido de > 30 s → rechazado (replay fuera de ventana).
4. `iperf3 -c <tunnel>` medido a través del proxy: caudal ≤ 5,0 Mbps + 0,3; ssh al router bajo saturación: p95 de latencia interactiva < 300 ms (muestra de 100 pings).

## Dependencias

EP-04 entrega el túnel y los streams; EP-03 entrega `node_secret` para el HMAC; EP-09 aplica admisión y deny-list del lado gateway; EP-10 consume los contadores por sesión.

## Definición de hecho

Un cliente SOCKS5 real atraviesa el nodo sin que el nodo escuche nada, el dueño no nota el router bajo tope, ningún tenant puede forjar sesión con un token viejo, y un nodo con 9 tenants admite a 8 y niega al noveno con métrica visible.
