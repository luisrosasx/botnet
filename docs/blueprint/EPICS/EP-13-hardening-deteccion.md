---
artifact: epic
id: EP-13
title: Hardening de detección
owner_layer: agent
closes: A1-01, A1-03, A1-06, A1-07, A1-08, A1-15, A1-17
estimate_days: 6
risk: medium
phase: F2
---

## Objetivo

Cerrar la brecha entre «el nodo late» y «el ISP no sabe que late»: destino compartible y cadencia con ventana, ticket TLS persistido para que los reinicios no publiquen handshakes completos, una sola cadencia visible, densidad medida como métrica de destino, huella de bundle escalonada y la puerta F6 del DNS pasiva (identificar al operador en ≤ 60 % de 200 nodos).

## Alcance

- Persistir session ticket + `node_id` (~1 KB) y presupuesto de reinicios con backoff 3/h → 1/h (A1-03).
- CNAME del destino compartible, ALPN h2 en 443 y cadencia por ventana (60–150 s de intervalo efectivo entre controlframes) (A1-01).
- Una sola cadencia por nodo: todo control sobre el mux; UDP periódico propio prohibido en clase B/C (A1-07).
- Densidad: tope de ≤ 8 inquilinos con scoring en Redis y quema por densidad (A1-06/A1-17, pata de detección).
- Renombres deterministas por clase (ldflags) y mtimes escalonados (A1-08).
- Histeria de presión de memoria: pausa mínima 10 min + cooldown 5 min para que el destino no vea conecta/desconecta en ciclos de minutos (A1-15).
- Drill de DNS pasiva: 7 días de flows del top-2 de ISP del piloto sobre ≥ 200 nodos; el operador se identifica en ≤ 60 % (A1-18, puerta F6).

## Fuera de alcance

- El ≥ 2 ASNs y el SNI rotado son de EP-04 (los reutiliza la puerta); el relleno anti-longitud-fija también. La pila TCP/IP del router de 2009 y la rDNS de charca quedan como riesgo aceptado del veredicto (no hay root que cambie el kernel): esta épica las mide, no las cura.

## Hallazgos que cierra

- A1-03 (S2): sin ráfagas de handshakes completos al revivir — ticket y node_id persistidos, backoff de reinicios.
- A1-01 (S2): destino compartible vía CNAME — «un NAS hablando con su nube» con nube compartible.
- A1-07 (S2): una sola cadencia — la defensa de boquilla del UDP periódico se jubila.
- A1-06 (S2) y A1-17 (S1, pata detección): densidad del destino topada, puntuada y quemada.
- A1-08 (S3): mtimes escalonados y renombres — el bundle deja de compartir firma.
- A1-15 (S2): sin oscilación conecta/desconecta por watermark — pausa con histeria.

## Criterios de aceptación de la épica

1. Con flapping simulado (netem) y 4 auto-kill en 1 h: handshakes TLS completos por revive = 0 cuando el ticket es válido (capture `SSLKEYLOGFILE` + filtro de handshakes) y los intervalos de revive son monótonos con backoff (log asertado).
2. `dig +short CNAME rdv.example` responde el dominio compartible del cliente; captura de 1 h: intervalos entre frames de control dentro de la ventana 60–150 s en ≥ 95 % de los casos y UDP con cadencia propia = 0 paquetes/h en clase B/C.
3. `stat -c %Y /data/botd/* | sort -u | wc -l` = número de ficheros del bundle (mtimes todos distintos) en clases A/B; `botctl status` muestra el nombre renombrado y el manifest lo coincide.
4. Métrica `botnet_tenant_count` por nodo visible en `/metrics`; nodo con densidad > 8 → su score de pool decae y deja de ser seleccionado en < 24 h (test con fixture de density); pausa por watermark: ≤ 1 admisión rechazada por memoria en cualquier ventana de 10 min.
5. `scripts/drill_dns.sh --nodes=200 --days=7 --isp-top=2` (fixture F6) → imprime `operator_identified_pct ≤ 60` o el pipeline falla (puerta F6).

## Dependencias

EP-03 entrega el estado persistente donde vive el ticket; EP-04 entrega cadencia, SNI y ASNs que esta épica endurece; EP-06/EP-09 publican la densidad que se puntúa; EP-12 publica el SLO que la puerta F6 consume.

## Definición de hecho

Un ISP con logs CGNAT de 7 días y una consulta de DNS pasiva agrupa como mucho al 60 % de la cohorte; los reinicios no gritan handshakes; el bundle no comparte mtime; y un nodo con cara de datacenter sale del pool antes de hundir la prima residencial.
