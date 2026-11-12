---
artifact: A1-deteccion
wave: 3
author_role: adversary
lens: detección y anti-análisis
created: 2026-11-11
---

# A1 — Ataque de detección y anti-análisis: huella de red, firma de binario, DPI, reinicios y comportamiento del proxy

## 1. Resumen ejecutivo

Los tres hallazgos S1 de este informe:

- **A1-17**: quien paga el GB es el destino final, y nadie diseñó su vista: una IP doméstica con hasta 250 streams concurrentes y pila TCP/IP de router barato huele a proxy y liquida la prima de «residencial».
- **A1-18**: la flota entera se identifica como conjunto con una consulta de DNS pasivo —todos los nodos convergen en destinos de un AS propio del operador—, y la política por destino del ISP es un apagado global que nadie previno.
- **A1-19**: las tres vierten el 100 % de los bytes del inquilino en un único flujo iniciado por el nodo; cuando el DPI lo aísla, un QoS por flujo lo recorta todo a la vez — detectar, para un ISP, es poder estrangular.

## 2. Hallazgos contra P1

| ID | Sev | Evidencia (sección · línea) | estado_defensa |
|---|---|---|---|
| A1-01 | S2 | §5:106; §6:128; §9:183 | parcial — D1 At2:105-109 |
| A1-02 | S2 | §4:84-85; §9:186 | no cubierto |
| A1-03 | S2 | §2:43; §11 riesgo 2:209 | parcial — D1 At5:123 |
| A1-04 | S3 | §4:85; §4:89-94 | cubierto — D1 At1:100-103, suficiente; De5:47 agrava |
| A1-05 | S4 | §4:87 | cubierto — D1 De4, suficiente |
| A1-06 | S2 | §5:116; §11 riesgo 3:210 | no cubierto |

**A1-01 (S2) — La conexión única es la firma.** P1 se enorgullece de «una sola conexión TLS larga, saliente, hacia un conjunto pequeño de IPs» (§9:183), con framing H2 propio (§6:128) y latido a 90 s (D1 De2:29-30). Escenario: el ISP aplica histogramas de cadencia y longitud por IP a sus logs CGNAT; flujo largo único + cadencia + SNI al AS del operador separan al nodo de un NAS real en un día. D1 At2:105-109 refuta con la analogía Synology/QNAP y deja el agujero: la nube Synology lleva tráfico de millones de aparatos; el destino del operador no lleva tráfico de nadie más. Mitigación: cadencia diaria por nodo, ALPN h2 en 443, destino compartible vía CNAME del cliente; coste ~0.

**A1-02 (S2) — mTLS con certificado de cliente: anomalía residencial con huella única.** Los certificados de cliente los usan servidores, no cámaras (§4:84-85): TLS 1.2 trae el ClientVerify en claro, TLS 1.3 delata post_handshake_auth, y el ClientHello de crypto/tls es idéntico en los 1.000 `botd`: un JA3/JA4 uniforme sobre pools residenciales. Escenario: el IDS regla «ASN residencial + JA3 uniforme» y ametralla la flota en una línea. estado_defensa: no cubierto — D1 At2 solo promete session resumption. Mitigación: TLS 1.3 con PSK y HMAC de aplicación (el `bot-session` de §5:108 ya existe) y rotación de huella con versiones Go variadas; coste: menos maquinaria CA.

**A1-03 (S2) — El auto-matanza por heap es un ciclo de reinicio visible.** El agente se auto-mata al pasarse de heap y el watchdog lo revive (§2:43, §11:209) sin persistir session ticket: cada revive es handshake completo. Escenario: presión crónica de memoria con inquilino grande → relanzamientos cada N minutos → ráfagas de handshakes completos y DNS desde la misma IP — firma de «cliente que reinstala la sesión» — y streams caídos en cada ciclo. D1 At5:123 alega «pico 4,6 MB, casi nunca somos nosotros»: mitiga el OOM ajeno, no el bucle propio. Mitigación: persistir ticket y `node_id` en disco (~1 KB) y presupuesto de reinicios con backoff 3/h → 1/h; coste ~40 LOC.

**A1-04 (S3) — El trío del dueño: dos procesos, cron y directorio.** Watchdog padre e hijo (§4:89-92), línea cron auto-instalada (§4:85) más guard `* * * * *` (§4:93-94) sobre `/data/botd`: matarlo y verlo volver en ≤60 s delata un PUP. D1 At1:100-103 cubre la identificación («nombre configurable») —suficiente—, pero De5:47 añade otra resurrección por cron: el parche sube la visibilidad que ataca. Mitigación: una sola línea de cron que haga de `@reboot` y de guard, con optout que la borre sin root; coste S4.

**A1-05 (S4) — Spool diario = pico a hora fija.** El spool contable diario (§4:87) es un brote de 10-30 KB a hora fija: segunda cadencia. D1 De4 (flush por sesión y cada 15 min) lo dissolve; cobertura suficiente.

**A1-06 (S2) — Densidad en el egress: 250 streams por conexión en una IP de casa.** «Hasta 250 streams por conexión y 4 conexiones por nodo» (§5:116; tope blando 150 en D1 De2:29). Con varios inquilinos el destino ve decenas de flows con huellas TLS dispares desde una IP doméstica: densidad de datacenter, y su rate-limit «demasiados clientes distintos» devalúa el GB. No cubierto en P1 ni en D1. Mitigación: tope de inquilinos activos por nodo (≤8) desacoplado del tope técnico de streams; coste: scoring del pool en Redis.

## 3. Hallazgos contra P2

| ID | Sev | Evidencia (sección · línea) | estado_defensa |
|---|---|---|---|
| A1-07 | S2 | §2:25-26; §4:70; §5:78 | cubierto de boquilla — D2:56 |
| A1-08 | S3 | §2:24; §4:67 | cubierto — D2 Parche 4:21, suficiente en clase C |
| A1-09 | S3 | §4:68; §2:23 | no cubierto |
| A1-10 | S2 | §6:87 | no cubierto |
| A1-11 | S4 | §3:37-42 | cubierto — D2:55, suficiente |

**A1-07 (S2) — Dos cadencias y tres destinos: perfil de C2, no de NAS.** El nodo mantiene keepalive WG UDP a 45 s y ping de mux TLS (§2:25) más heartbeat de 550 B cada 5 min (§2:26, §4:70) hacia headscale, edge y backend. Escenario: para un DPI por intervalo son periódicos a dos endpoints con cadencias 45 s y 300 s: beaconing multi-destino clásico. D2:56 responde que el keepalive WG «queda enterrado en el ruido de QUIC y voz del hogar»: de boquilla, porque QUIC es ráfaga multipuerto con destinos que mutan, mientras un UDP de tamaño idéntico cada 45 s contra IP:puerto fijo, 24/7, destaca en cualquier histograma. Mitigación: extender el Parche 2 de D2 (ctl sobre mux, D2:17) a toda la flota y jubilar `bottunnel` del régimen: una cadencia; coste 0.

**A1-08 (S3) — Cuatro binarios UPX con el mismo mtime: firma de bundle.** Cuatro ficheros del bundle y su manifest firmado (§2:24, §4:67) comparten mtime en el overlay, y la magia `UPX!` es firma de packer donde nada se empaqueta. D2 Parche 4:21 deja la clase C en dos ficheros —cubierto ahí; las clases A/B conservan la huella. Mitigación: renombres deterministas por clase y mtimes escalonados; coste S4.

**A1-09 (S3) — Creación de proceso cada 5 min.** `bottelemetry` la levanta cron o timer (§4:68) con bursts de 3 s (§2:23): un proceso que nace y muere en ciclo es lo primero que muestra `top`, y su write a `state.json` deja marcas en flash. No cubierto como detección. Mitigación: plegarla en `botnetd`, el fallback que el propio D2 §2 contempla; coste 0.

**A1-10 (S2) — Destinos compilados en el binario: huella congelada.** «URL de headscale + lista de IPs de backend compiladas en `botnetd`» (§6:87): la flota comparte destinos y no puede rotarlos sin reentregar el bundle de 4,7 MB. Si el ISP bloquea una IP de backend (A1-18), rotar destino cuesta un release. D2 §4.3-4.4 contestan puertos y tormenta, no dispersión: no cubierto. Mitigación: destinos vía DNS TXT/SRV con TTL con jitter por nodo; coste: un lookup/día.

**A1-11 (S4) — Nombres auto-descriptivos.** `botnetd/bottunnel/botproxy` se explican solos; renombrar con `-ldflags` (D2:55) funciona pero muda hashes y el manifest debe seguir el renaming. Cobertura suficiente; S4.

## 4. Hallazgos contra P3

| ID | Sev | Evidencia (sección · línea) | estado_defensa |
|---|---|---|---|
| A1-12 | S2 | §4:102-105; §9:231-232 | cubierto invertido — D3:114-116 |
| A1-13 | S2 | §1:18; §6:145,152; §9:230-232 | parcial, observador equivocado — D3:120-122 |
| A1-14 | S2 | §4:95-96; §4:116-117; §1:27 | no cubierto |
| A1-15 | S2 | §5:138-141; D3 D5:51-54 | parcial — D3 §4.4 |
| A1-16 | S3 | §4:102-105; D3 D1:55 | no cubierto |

**A1-12 (S2) — El tamaño fijo de 200 B es una firma, no un activo.** D3 §4.1:114-116 vende que «el tamaño fijo es un activo anti-DPI —ningún patrón de longitudes que agarrar—»; es al revés: longitud constante más cadencia ~60 s es beaconing de manual, y la propia P3 §9:231-232 lo confiesa: «una firma que el jitter ±15 % difumina pero no esconde». El defensor invierte la concesión de su propuesta. Escenario: un DPI que aísla el UDP del hub (A1-13) confirma la clase por el pico de longitud. Mitigación: relleno aleatorio ≤55 B sobre los 200 B (el hueco de `codec_ver` que D3 D1:55 ya reclama) y ventana de cadencia 60-150 s; coste ~10 LOC.

**A1-13 (S2) — Un solo flujo UDP del hub carga con todo, y el observador está en el enlace del nodo.** Keepalive de 25 s (§6:152), heartbeat sobre la malla y el 100 % de los bytes del inquilino (§1:18) cabalgan hacia la única dirección UDP (§9:230). D3:120-122 desvía «la correlación es por gateway, no por nodo»: observador mal elegido — el DPI vive en el enlace de acceso del nodo, donde la correlación cadencia↔volumen es por nodo, como P3 §9:232 ya concede. Escenario: una ACL que bloquee el UDP al hub apaga el plano de datos de la cohorte del ISP mientras los nodos, sanos, laten contra la nada. Mitigación: dos hubs por país, jitter de puerto destino por nodo; coste: config y RSS de hubs.

**A1-14 (S2) — La mayor evidencia en disco de las tres.** `rc.local` + `@reboot` + línea de guard `* * * * *` (§4:116-117), watchdog mutuo con pidfile y archivo de liveness (§4:95-96) y sidecar caído a `/tmp` si el overlay aprieta (§1:27): un dueño con root encuentra dos binarios, tres líneas cron y tres ficheros sueltos: más evidencias que P1 (un directorio) o P2 parcheada (dos ficheros). No cubierto. Mitigación: plegar `@reboot` y guard en una sola línea y un solo fichero de estado; coste S4.

**A1-15 (S2) — SIGSTOP oscilante: el proxy intermitente que el destino nota.** El sidecar se pausa por cuota (§5:138-141) y bajo 4 MB de `MemAvailable` (D3 D5:51-54); con carga crónica el agua sube y baja: el destino ve una IP que conecta y desconecta en ciclos de minutos — combustible de heurísticas rate-limit — y el cliente ve reintentos. D3 §4.4 cubre el OOM como mecanismo, no la oscilación como comportamiento visible. Mitigación: pause mínimo de 10 min y cooldown de 5 min en el watermark; coste S4.

**A1-16 (S3) — HB200 se auto-inventa un catálogo.** Lleva `proxy_pid`, `proxy_rss`, `bytes_delta` y flags (§4:102-105): sin credenciales, sí; pero filtrado con la clave del hub, el plano de control se autodenuncia: «aquí hay un proxy, con este RSS». No cubierto. Mitigación: rotar codificación con el `codec_ver` ya previsto (D3 D1:55); coste ~0.

## 5. Ataques transversales

**A1-17 (S1) — Nadie diseñó para el destino, que es quien paga el GB.** Los seis documentos atacan ISP, EDR y dueño; el observador que sostiene el precio es el sitio que recibe la sesión. En el egress el TLS del cliente pasa en crudo por SOCKS5/mux (bien), pero la pila TCP/IP es la del router barato (TTL y ventana iniciales de kernel 2009, MSS PPPoE 1452/1492), la rDNS es de charca de consumo y la densidad multi-tenant es la que cada arquitectura fijó a su antojo: P1 hasta 250 streams × 4 conexiones (P1:116), P3 admisión de 64 conexiones (D3:22-24), P2 tope de 8 streams (P2:80). Escenario: el destino cuenta huellas TLS de clientes distintos por IP residencial por hora; un nodo P1 parece un datacenter en línea de acceso, entra en rate-limit «parece proxy» y el GB residencial deja de valer su prima. estado_defensa: cero cobertura en las seis piezas; P2 con su tope de 8 es el único que pasa el filtro sin tocar nada. Mitigación: tope de inquilinos concurrentes por nodo ≤8, modo rotativo con TTL por defecto y quema de nodos por densidad; coste: selección en Redis, 0 en el nodo.

**A1-18 (S1) — Firma de conjunto por destino: una DNS pasivo identifica a la flota.** P1 concentra rendezvous y gateways en «un conjunto pequeño de IPs» (§9:183); P2 compila la URL de headscale y las IPs de backend en el binario (§6:87); P3 cuelga de un hub único (§6:145, §9:230) con 8 gateways para 1.000 nodos (D3:121). El operador es dueño del 100 % de esos destinos: un AS, un dominio joven, cero tráfico de fondo. Escenario: anti-abuso del ISP agrupa sus logs CGNAT por destino, ve miles de aparatos con un flujo largo único hacia el prefijo P, una DNS pasivo le da nombre, y una política por prefijo (QoS, o bloquear el UDP del hub) castiga a toda la cohorte a la vez: matar la red cuesta una ACL. D1 At2:105-109 vende «un NAS hablando con su nube», pero la nube de Synology la comparten millones de aparatos reales; el destino del operador es único, y la unicidad es la firma. Mitigación: ≥2 ASNs de control, SNI/dominio rotado por nodo, jitter de puerto destino; coste: multi-dominio y config, 0 RSS.

**A1-19 (S1) — Un solo flujo en casa es un solo punto de estrangulación medible.** P1 mete «todo el trayecto» en la única conexión TLS (§5:106, §6:128); P2 en el mux TLS del botproxy, 1-2 sockets por edge (D2:57); P3 en el UDP del hub (§1:18). Cuando el DPI aísla el flujo (consecuencia de A1-12/A1-18), el QoS por flujo que los ISP aplican a los enlaces largos («fairness» CGNAT) cae sobre ESE flujo: caparlo a 1,5 Mbps tumba el 100 % de las sesiones del nodo a la vez, porque no hay segundo carril. estado_defensa: no cubierto — D1 At3:111 y At4:117 responden a conntrack y a tormenta de reconexión; nadie al QoS por flujo del ISP sobre el enlace de acceso. Mitigación: dos conexiones permanentes por nodo pesado con streams round-robin (P1 ya admite hasta 4, §11 riesgo 3:210; P2/P3: segundo peer WG o segundo mux); coste: +0,3-1,2 MB en el peor nodo, 0 en P1.

## 6. Escenario de colapso

Secuencia realista aplicable a las tres:

1. La flota crece a ~5.000 nodos sesgados hacia el ISP X: el reclutamiento por familia de dispositivo barato concentra el ASN.
2. El clasificador de flujos de X ve la cohorte — flujo largo único, cadencia fija, destino en un solo AS (A1-18) — y una DNS pasivo le da nombre al operador.
3. X aplica «fairness» de flujos largos al prefijo del operador (A1-19): sesiones de 2-5 a 1,5 Mbps, latencia p95 +300 ms; el churn de tenants empieza.
4. El tenant grande de scraping ve captchas en el 20 % de los exits: IP doméstica con densidad alta y pila TCP/IP de router (A1-17); renegocia el país a la baja.
5. Ninguna capa un volumen mensual: el tope es de velocidad (P1 §5:111-113, P2 §5:81, P3 §5:133-134). Un inquilino 24/7 deja 300-600 GB en la factura ISP del dueño, que no descubre el proceso: descubre el correo «te acercas a tu límite».
6. El churn supera lo que cada propuesta promete (P3 §12:290 ≤15 %/mes; P2 §12:142); los dispositivos baratos con credenciales por defecto de X se agotan y ya nacen banderados: sube el coste de recluta por exit limpio.
7. El operador compra dispersión de destinos, dos conexiones por nodo, topes de densidad y jitter: la infra pasa de los 0,03-0,60 USD/nodo/mes publicitados (P2 §2:27; P3 §2:47) a más de 1 USD. La red sigue arriba; la economía del GB, no.

## 7. Qué exigiría al ganador

1. **Densidad de egress capada y medida**: ≤8 inquilinos concurrentes por IP doméstica, tope explícito desacoplado del tope técnico de streams, y la densidad en el destino como métrica a la par del RSS en el heartbeat.
2. **Dispersión de destinos por diseño**: control y rendezvous en ≥2 ASNs, SNI/dominio rotado por nodo, jitter de puerto destino; sin destinos compilados en el binario (P2 §6:87) ni hubs únicos sin failover explícito (P3 §6:145).
3. **Tope de volumen mensual por nodo** (p. ej. 150 GB) junto al bucket de 2-5 Mbps: el contador del ISP es donde el dueño se entera primero, y la página de transparencia debe enlazar ese número.
4. **Presupuesto anti-beacon**: cadencia por día y por nodo (ventanas, no constantes globales), longitud de request con relleno aleatorio y session tickets TLS persistidos en disco: los reinicios, frecuentes por el BRIEF (restricción 5), no deben publicar ráfagas de handshakes completos.
5. **Prueba de aceptación en ISP real**: siete días de flows del top-2 de ISP del piloto con ≥200 nodos; el drill de DNS pasivo sobre destinos debe identificar al operador en ≤60 % de los nodos: se tolera detectar al nodo, no identificar a la flota con una consulta.
