---
artifact: epic
id: EP-34
title: Puerta del panel con sabotajes
owner_layer: panel
closes: seis sabotajes muerden y la puerta vuelve a 0 al revertirlos
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que la puerta del panel muera cuando el panel miente: filas de menos, estado inventado, base caida tratada como vacio y lista que se mueve al leer.

## Alcance

scripts/check-panel.sh ya existe: anadir sabotajes y sus reversiones, mas el contrato de salida y el modo fixture.

## Depende de

EP-30, EP-31, EP-33

## Cierre

seis sabotajes muerden y la puerta vuelve a 0 al revertirlos
