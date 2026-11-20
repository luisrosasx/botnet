---
artifact: epic
id: EP-35
title: Despliegue del panel como unidad y runbook
owner_layer: panel
closes: unidad activa con Restart=always y runbook con comandos ejecutables (bash -n en 0)
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que el panel corra como servicio persistente en el VPS bajo recruit.slice, con limites escritos y un runbook que otro pueda seguir.

## Alcance

deploy/systemd/botnet-panel.service, limites de CPU y memoria, docs/runbooks/panel.md con arranque, pausa, rollback y desinstalacion.

## Depende de

EP-30, EP-31, EP-34

## Cierre

unidad activa con Restart=always y runbook con comandos ejecutables (bash -n en 0)
