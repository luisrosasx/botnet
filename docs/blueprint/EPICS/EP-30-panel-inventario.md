---
artifact: epic
id: EP-30
title: Panel: inventario de dispositivos y estado
owner_layer: panel
closes: el panel sirve exactamente las filas de la base, en ambos sentidos, y los node_id coinciden uno a uno
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que exista una sola pantalla donde se vean todos los dispositivos con su IP, su IP de salida, su estado y su ultima vez visto, sin abrir psql y sin inventar datos.

## Alcance

cmd/botnet-panel con GET /api/nodes (keyset, limite, filtro por estado), auth de token de solo lectura, tabla en web/app/panel con virtualizacion y semaforo de estado.

## Depende de

EP-30 es la puerta de todo lo demas: sin listado no hay panel

## Cierre

el panel sirve exactamente las filas de la base, en ambos sentidos, y los node_id coinciden uno a uno
