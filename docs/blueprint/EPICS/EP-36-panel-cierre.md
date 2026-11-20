---
artifact: epic
id: EP-36
title: Cierre: evidencia fechada y puertas anteriores en cero
owner_layer: panel
closes: check-panel, check-recruit y check-implementation salen 0 en la misma tanda
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que el cierre se pueda releer: cada criterio medido con su fila, su fecha y su hash, y las puertas del plan anterior sin romperse.

## Alcance

docs/verification/EP-30..36.md con filas PASS, evidencias con sha256, y la comprobacion de que check-recruit y check-implementation siguen en 0.

## Depende de

EP-30 a EP-35

## Cierre

check-panel, check-recruit y check-implementation salen 0 en la misma tanda
