---
artifact: epic
id: EP-07
title: Malla WireGuard y encapsulado
owner_layer: infra
closes: A1-09, A3-13, A3-X4, A2-11
estimate_days: 4
risk: low
phase: F4
---

## Objetivo

Montar la malla WireGuard/headscale como plano de operaciones best-effort — nunca de datos: por ella viajan la sonda iperf3, la sonda de geo del exit y el SSH de operaciones; jamás bytes de sesión, heartbeats, flushes ni updates. Un encapsulado por byte; prohibido WG-sobre-TLS-sobre-WG.

## Alcance

- headscale con ACLs por rango; nodos con `tun` entran a 100.64/10; clase C sin `tun` no pierde producto.
- Sonda iperf3 por la malla que siembra `bandwidth_cap_kbps` en `nodes`.
- Sonda de geo del exit con flag `geo_verified` (A3-X4): semanal, ~50 KB/nodo/mes.
- `leave_code` de 8 caracteres con rate-limit por IP `rate:leave:{ip}` (A3-13): la puerta legal no es numerable.
- Plegado de telemetría en `botd` en clase C (A1-09): ningún proceso que nace y muere cada 5 min.
- Dominio de fallo acotado (A2-11): la malla es de operaciones; cae headscale y el producto sigue porque ningún byte de sesión cabalga sobre WG.

## Fuera de alcance

- El plano de datos íntegro vive en el túnel EP-04: aquí se prohíbe, no se implementa. Nada de iperf3 en el nodo en reposo: las sondas son eventos de enrolado/operaciones. El presupuesto de la capa de túneles (partida nombrada que hereda la lección de A2-11) se escribe en EP-10.

## Hallazgos que cierra

- A1-09 (S3): plegado de `bottelemetry` en `botd` en clase C — sin creación de proceso cíclico ni marcas extra en flash.
- A3-13 (S2): código de baja de 8 caracteres + rate-limit por IP — 200k curls ya no dejan la flota en 503.
- A3-X4 (S3): sonda de geo activa con `geo_verified` — el país deja de ser autorreportado.
- A2-11 (S1 de P3, mandado al ganador): sin hub de datos único — el dominio de fallo de la malla no puede tumbar el producto.

## Criterios de aceptación de la épica

1. `headscale nodes list | grep -c managed` = número de nodos con `tun`; un nodo clase C sin `tun` mantiene `curl --socks5` funcionando (0 degradación de producto).
2. `tcpdump -i wg0 -w mesh.pcap` durante 100 sesiones (100 MB): bytes capturados en la malla < 1 MB — ningún byte de sesión cruza WG.
3. `ps aux | grep -c bottelemetry` = 0 en clase C durante 30 min; `crontab -l | wc -l` sigue siendo 1 (no se añade entrada de telemetría).
4. `for i in $(seq 1 60); do curl -s -o /dev/null -w '%{http_code}\n' -X POST "https://api/v1/nodes/$ID/leave?code=XXXXXXXX"; done | grep -c 429` ≥ 1 (rate-limit por IP a 60 req/min); código erróneo → `404`, y 1.000 IDs secuenciales barridos no tumbarían el panel (rate-limit activo).

## Dependencias

EP-03 entrega el enrolado (la sonda iperf3 siembra el tope tras el alta). EP-04: la prohibición de WG en el dato se verifica contra su túnel. EP-11 consume `geo_verified` y `leave_code` en la página de transparencia.

## Definición de hecho

La malla se puede apagar un martes a las 23:40 sin que ni un solo byte de sesión se entere; las sondas siembran tope de ancho y geo verificados; y la baja del dueño es un código de 8 caracteres a 60 requests/min por IP, no un ID enumerable.
