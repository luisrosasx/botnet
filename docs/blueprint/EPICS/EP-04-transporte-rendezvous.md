---
artifact: epic
id: EP-04
title: "Transporte: túnel H2, rendezvous, keepalive y cadencias"
owner_layer: transport
closes: A1-02, A1-10, A1-12, A1-18, A1-19, A2-04, injertos 1, 2, 3, 7
estimate_days: 10
risk: high
phase: F2
---

## Objetivo

Construir el mayor código propio del sistema: una conexión larga saliente TLS/HTTP2 sobre 443 con ALPN h2, un rendezvous sin estado `GET /rdv/assign`, una sola cadencia por nodo con keepalive de aplicación < 45 s, y la regla «nueva conexión antes que cola» hasta 4 conexiones. Es condición de aprobación: cierra los S1 A1-18 y A1-19 del veredicto.

## Alcance

- `internal/tunnel`: framing H2 propio, streams, backoff exponencial tope 5 min ± jitter, sesión resumida con ticket persistido.
- `cmd/botnet-rdv`: `GET /rdv/assign` sin estado (2 instancias) → `{gateway_addr, session_token, ttl_s}`; re-asignación tras 3 handshakes fallidos.
- `internal/hbcodec`: frame binario fijo con relleno aleatorio ≤ 55 B en el hueco de versión; JSON prohibido en el canal de control (mandato).
- Cadencia única: frame de aplicación cada 40 s ± jitter (siempre < 45 s); cada 3.º frame porta heartbeat completo (control cada 120 s); beacon ≤ 120 KB/nodo/día.
- Hasta 4 conexiones H2 por nodo cuando un stream debe encolar o se mide pérdida (1–2 %); SNI/dominio rotado por nodo, jitter de puerto, ≥ 2 ASNs de control.
- TLS 1.3 + PSK con certificado de cliente redundante jubilable (huella JA3 variada).

## Fuera de alcance

- El SOCKS5 y los streams de sesión (EP-06); la selección de gateway por localidad (EP-09); el fuzz de 72 h y el simulador de purge a 100k son puertas de EP-12 que consumen esta épica. La épica es la más densa del plan (9 tasks) porque arrastra cuatro injertos y dos S1.

## Hallazgos que cierra

- A1-10 (S2): rendezvous sin estado — rotar destino no cuesta reentregar 4,7 MB.
- A1-12 (S2): relleno ≤ 55 B — la longitud fija deja de ser firma.
- A1-18 (S1): ≥ 2 ASNs, SNI rotado, jitter de puerto — una DNS pasiva ya no agrupa la cohorte entera.
- A1-19 (S1): «nueva conexión antes que cola» hasta 4 conexiones — segundo carril contra el QoS por flujo.
- A2-04 / injerto 7: keepalive < 45 s sobre la conexión larga — el purge CGNAT nocturno deja de segar la TCP.
- A1-02 (S2) y A2-14: TLS 1.3 + PSK y HMAC de aplicación sin RTT; huella CA reducida.
- Injertos 1, 2, 3: rendezvous, regla de conexiones, codec con relleno.

## Criterios de aceptación de la épica

1. Loopback de 50 streams sobre UNA conexión: `ss -i 'dport :443' | grep -c ESTAB` = 1 con 10 MB transferidos sin error en 10 min; con stream encolando (netem 2 % de pérdida) sube a ≤ 4 conexiones y baja al cesar la pérdida.
2. `tshark -r capture.pcap -Y "len>0" -T fields -e frame.time_delta` sobre 1 h en reposo: intervalo máximo < 45 s en el 100 % de los frames; cadencia media 40 s ± 10 %.
3. `go test ./internal/hbcodec -run TestVectors` pasa con vectores de `testdata/`; 100 frames consecutivos presentan ≥ 3 longitudes distintas (relleno activo); simulación de 1 día: bytes de beacon ≤ 120 KB/nodo.
4. `curl -s https://rdv.example/rdv/assign` desde 2 instancias → respuestas con gateways distintos y ASN de `gateway_addr` ∈ ≥ 2 ASNs (aserción ip2asn en test); tras matar el gateway asignado, el nodo obtiene otro destino en < 3 reintentos.

## Dependencias

EP-03 entrega el contrato de enroll (`rendezvous[]`, `ttl_s`). EP-09 consume el casado rdv→gateway. EP-06 cabalga sus streams sobre el túnel. EP-12 ejecuta el fuzz de 72 h y el simulador de purge como puertas.

## Definición de hecho

Un nodo real bajo CGNAT con purge de 60 s mantiene su conexión larga viva toda la noche sin handshakes completos repetidos; la flota simulada de 100k bajo purge no infla el control > 5 %; y una política por prefijo de ISP ya no puede matar la cohorte sin llevarse por delante tráfico ajeno.
