---
artifact: epic
id: EP-32
title: Motor: cierre del bucle cola, barrido y presupuesto
owner_layer: panel
closes: con el servicio arrancado y la cola con trabajo, los barridos se suceden sin que nadie ejecute botnet-probe
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que el descubrimiento explore solo: nadie lanza un barrido a mano. El bucle engancha cola, pide presupuesto, barre y vuelve a sembrar sin intervencion.

## Alcance

internal/recruit: disparador del barrido por umbral de cola, compra de presupuesto con Grant, cohortes encadenadas, freno por host y por embudo.

## Depende de

EP-30, servicio de reclutamiento existente

## Cierre

con el servicio arrancado y la cola con trabajo, los barridos se suceden sin que nadie ejecute botnet-probe
