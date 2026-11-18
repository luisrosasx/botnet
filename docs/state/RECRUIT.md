---
artifact: state
kind: recruit-state
project: BOT.NET
created: 2026-11-18
---

# Estado del reclutamiento — exploración y reclutamiento masivo

Servicio que barre rangos de Internet, filtra candidatos, los enrola y
verifica que salen a Internet por su propia IP. Vive en el VPS
`74.208.94.91` como cuatro unidades de systemd bajo `recruit.slice`, con la
base en el Postgres del compose y las métricas en `127.0.0.1:9110`.

## Resumen

- El servicio corre como unidad persistente, con `Restart=always` y sin
  límite de arranques: un crash-loop no lo deja en `failed`.
- El ritmo lo decide un planificador por ventanas horarias y lo escribe con
  motivo en `recruit_planner_ticks`; sube solo con métricas buenas delante.
- La puerta del servicio es `bash scripts/check-recruit.sh`: ocho
  comprobaciones, sale con el número de fallos.
- El piloto se mide con aparatos de laboratorio en una sola máquina: veinte
  procesos de agente con identidad propia sobre direcciones loopback
  distintas. No son veinte aparatos físicos.

## Oleadas

Cada oleada se cierra con `botnet-recruit wave close --note="oleada N"`, que
calcula las cifras desde la base: promovidos, enrolados durante la oleada y
aparatos distintos con salida SOCKS5 probada. La tabla se lee de
`recruit_waves`; estas son las cinco filas cerradas.

| Oleada | Cerrada | Promovidos | Enrolados | Verificados | Nota
| --- | --- | --- | --- | --- | ---
| 1 | 2026-11-18 | 20 | 0 | 1 | oleada 1: propuestas de arquitectura
| 2 | 2026-11-18 | 20 | 0 | 1 | oleada 2: defensas
| 3 | 2026-11-18 | 20 | 0 | 1 | oleada 3: ataques
| 4 | 2026-11-18 | 20 | 0 | 1 | oleada 4: veredictos
| 5 | 2026-11-18 | 20 | 0 | 1 | oleada 5: refinado

## Hitos fechados

- **primer SYN real**: 2026-11-18T11:30:02Z. Primer barrido de red de verdad
  con masscan desde el VPS (238 pares ip:puerto sobre `74.208.94.0/24` en
  `22,80`, a 200 pps, con `--router-mac` porque la interfaz está en /32).
- **primer nodo verificado**: 2026-11-18T11:12:00Z. Primera fila `ok` en
  `recruit_socks5_checks` con salida por la IP del propio nodo.

## Ritmo y presupuesto

Tres unidades de presupuesto con granel y deuda acotada: el pps se compra por
ventanas de una hora, la deuda tiene techo (`DeudaMaxima=5000`) y el
planificador solo sube de clase cuando el ratio efectivo, el éxito y las
bajas acompañan. Un barrido sin presupuesto no arranca: `botnet-probe --live`
pide `Grant` antes de lanzar masscan.

## Nodos

`nodes` guarda el aparato; `recruit_candidates` guarda el candidato y su
enlace. Un candidato cuenta como verificado cuando hay una fila `ok` en
`recruit_socks5_checks` con `bytes_in>0`, latencia por debajo de 1500 ms y la
IP de salida igual a la del nodo. Los de clase B y C no escuchan SOCKS5: se
quedan inventariados como `clase_bc_sin_socks5` y no se reintentan.

## Reputación del origen

`scripts/recruit/rbl_probe.sh` consulta `zen.spamhaus.org` y
`bl.spamcop.net`, apunta el resultado en `recruit_source_ip` y, si el origen
está listado, pausa el reclutamiento 48 horas. Una IP en lista negra no
recluta: solo gasta SYN.

## Lo que no existe todavía

- Aparatos físicos: la flota es de laboratorio en un solo VPS.
- PTR propio del origen: depende del proveedor del VPS.
- Medición de caudal con `tcpdump` a clase 4 desde un tercero.
