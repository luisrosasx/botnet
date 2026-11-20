---
artifact: epic
id: EP-33
title: Exponencialidad medida y sin doble conteo
owner_layer: panel
closes: tres ventanas consecutivas con x1,5 leidas de la base y 0 IP contadas dos veces en la misma ventana
estimate_days: 3
risk: medium
phase: F8
---

## Objetivo

Que el crecimiento se pueda ensenar con una cifra y no con una impresion: candidatos nuevos por ventana creciendo como minimo x1,5 tres ventanas seguidas, sin contar dos veces la misma IP.

## Alcance

recruit_candidates con clave unica por ip y ventana, contadores por ventana en la base, vista de ritmo y comprobacion en la puerta.

## Depende de

EP-32

## Cierre

tres ventanas consecutivas con x1,5 leidas de la base y 0 IP contadas dos veces en la misma ventana
