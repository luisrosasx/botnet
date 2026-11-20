---
artifact: epic
id: EP-31
title: Panel: base caida, paginacion y metricas propias
owner_layer: panel
closes: con la base parada el panel da 503 y la UI lo dice; con 5.000 filas responde por debajo de 500 ms
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que el panel distinga «no hay dispositivos» de «la base no responde» y aguante miles de filas sin mentir ni caerse.

## Alcance

503 con db_down en la API, estado de error en la UI distinto del vacio, paginacion por keyset con limite maximo, metricas botnet_panel_* (filas servidas, desfase base/panel, conexiones).

## Depende de

EP-30

## Cierre

con la base parada el panel da 503 y la UI lo dice; con 5.000 filas responde por debajo de 500 ms
