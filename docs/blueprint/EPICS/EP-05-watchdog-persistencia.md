---
artifact: epic
id: EP-05
title: Watchdog y persistencia multi-firmware
owner_layer: agent
closes: A1-04, A1-05
estimate_days: 5
risk: medium
phase: F3
---

## Objetivo

Que el agente sobreviva al peor firmware: sin systemd garantizado, dispositivos que reinician solos y un dueño same-uid que puede matar procesos. `botd --parent` relanza `botd --child` (reexec, sin segundo binario), pipe mutuo y pidfile `flock`; una sola línea de cron hace de `@reboot` y guard; arranque systemd → rc.local → `@reboot` según capacidad.

## Alcance

- Doble proceso por reexec del mismo binario con pipe mutuo y pidfile `flock` en `/data/botd`.
- Re-elección: muere el padre → el hijo se re-elige; muere el hijo → el padre lo relanza; muere todo → el cron guard revive al padre si el pidfile no responde.
- Una única línea de cron `* * * * *` (hace de `@reboot` y de guard); el opt-out la borra sin root (A1-04).
- Arranque best-effort: unit systemd, rc.local, sysv/`@reboot` según lo que ofrezca el firmware.
- Estado y spool en un solo directorio: `/data/botd` (0600) en clases A/B; spool a `/tmp` en clase C (A1-05: flush por sesión y cada 15 min disuelven el pico diario).

## Fuera de alcance

- El contenido del heartbeat (EP-04) y la contabilidad del spool (EP-10); el soak de 1.000 reinicios con `pkill` como puerta de fase lo ejecuta EP-12 sobre esta épica. El cifrado de clave bajo secreto de hardware es opcional y best-effort: si no existe `/data`, nada que persistir en clase C tmpfs.

## Hallazgos que cierra

- A1-04 (S3): el trío del dueño (dos procesos, cron, directorio) se reduce a una línea de cron y un directorio; la resurrección por cron deja de sumar visibilidad.
- A1-05 (S4, ya resuelto en W4): flush por sesión y cada 15 min + dedup `flush_id` — sin spool diario a hora fija.

## Criterios de aceptación de la épica

1. `pkill -f 'botd --child'` → el padre relanza en < 10 s (`pgrep -c -f botd` vuelve a 2); `pkill -f 'botd --parent'` → el hijo se re-elige en < 15 s sin procesos huérfanos.
2. `crontab -l | wc -l` = 1 con la línea guard activa; `kill -9` de ambos procesos → revived en ≤ 70 s (cron 60 s + margen), medido con marca de tiempo en log.
3. `kill -9 $(pgrep -f botd)` × 30 ciclos aleatorios: 30/30 recuperaciones, mediana de revival < 60 s (watchdog.log).
4. Tras reboot del QEMU: `botd` vivo en ≤ 90 s por la vía que ofrezca el firmware (systemd | rc.local | @reboot) y `ls -l /data/botd` muestra un único directorio con permisos 0600.

## Dependencias

EP-03 entrega el binario y `node.json` (el watchdog no introduce estado nuevo). EP-10 consume el spool de contabilidad. EP-12 usa esta épica para el soak de 1.000 reinicios (puerta F3).

## Definición de hecho

Matar al agente es inútil: cualquiera de las dos muertes (padre, hijo, ambos) se revierte en < 90 s sin intervención, con un solo pidfile vivo y sin dejar huérfanos tras 1.000 ciclos — el dueño puede apagar el router, pero no jubilar el proxy.
