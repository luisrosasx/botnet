---
artifact: V1-juicio-deteccion
wave: 4
author_role: judge
closes: detección y anti-análisis
created: 2026-11-11
---

# V1 — Veredicto del juez J1: lente de detección y anti-análisis

Sello del juez: la lente A1 no perdonó a ninguna de las tres — los tres S1 del informe (A1-17, A1-18, A1-19)
llegan vírgenes al cierre: ninguna defensa los tocó — y por eso el techo de 60 cae sobre las tres. Las
notas premátrica están en la sección 1; los techos, en la 2; el cierre de los diecinueve hallazgos, en la 3.

## 1. Matriz de puntuación

Criterios y pesos de `docs/blueprint/_process/WAVE-PLAN.md:26`. Nota 0-10 por celda; toda nota cita evidencia de disco.

| Criterio (peso) | P1 | P2 | P3 |
|---|---|---|---|
| Viabilidad en hardware pobre (20) | **9** — uniproceso upx ≤ 4 MB y pico recalculado ≤ 4,6 MB con 150 streams, 23 % de margen sobre el tope de 6 MB (D1-defense.md:60; P1-frugal.md:31-39) | **7** — PSS 4,2 reposo / 5,0 pico tras parches, clase C en 3,6 MB (D2-defense.md:29-31), pero las cifras son aritmética post-parche con boringtun sustituido a mitad de defensa (D2-defense.md:15), no smaps de F1 | **5** — 9,5 MB totales residentes contra 10-14 MB libres del router (D3-defense.md:63; P3-plano-desacoplado.md:40); el tope de 6 MB solo se salva releyendo el BRIEF a favor del agente (D3-defense.md:18-25) y wireguard-go userspace come CPU por cada Mbps (D3-defense.md:42-47) |
| Robustez frente a ataques (20) | **6** — tormenta y OOM bien cubiertos (D1-defense.md:117-123) y segundo carril gratis hasta 4 conexiones (P1-frugal.md:116,210), pero mTLS/JA3 uniforme (A1-02) y densidad (A1-06) sin parche, y el bucle de auto-matanza sin ticket persistido sigue abierto (A1-03) | **6.5** — OOM mata solo el plugin (D2-defense.md:59), circuit breaker (D2-defense.md:23), ctl-sobre-mux mantiene control si el túnel muere (D2-defense.md:17), y es la única que pasa el filtro de densidad con lo puesto: 8 streams (P2-modular.md:80; A1-deteccion.md:84). Penalizada por la defensa de boquilla de A1-07 (D2-defense.md:56) | **5** — el desacople tesis es real ante OOM (D3-defense.md:127-131), pero un solo UDP al hub es a la vez dominio de fallo global y firma (P3-plano-desacoplado.md:145,152; A1-deteccion.md:74), el beacon de 200 B fijos es beaconing de manual que la defensa vende al revés (D3-defense.md:114-116 vs P3-plano-desacoplado.md:231), y 4 de 6 hallazgos de su columna quedaron sin cubrir |
| Mantenibilidad a 12 meses (15) | **6.5** — un binario y una máquina de estados (P1-frugal.md:49-76), pero el mayor volumen de código propio del sistema: túnel TLS/H2 con framing a medida, handshake propio y rendezvous extra (P1-frugal.md:128-130,171-176; D3-defense.md:86-88); cada release reinicia el 100 % de la pila del nodo (D2-defense.md:44) | **6.5** — plugins con frontera clara e `ipc_ver` en el contrato (D2-defense.md:19), pero 4 procesos, manifest, firmas y matriz de 24 artefactos por release para una organización que aún no cobró (P2-modular.md:136; D3-defense.md:106-109) | **7** — binomio agente+proxy = 12 artefactos y cubre todo cambio posible (D3-defense.md:109); WG/headscale son componentes estándar frente a los protocolos propios de P1 (P3-plano-desacoplado.md:145); el único código exótico es el codec HB200 (P3-plano-desacoplado.md:102-105) |
| Coste operativo (15) | **8.5** — 185 USD/mes por 1.000 nodos = 0,185/nodo tras parches, 26 % bajo el tope de 0,25 (D1-defense.md:65); beacon 95 KB/día (D1-defense.md:63) | **7** — 0,025 USD/nodo de plano de control (P2-modular.md:27) pero 24 artefactos con egress por release y crash-loop por módulo (P2-modular.md:136; D1-defense.md:84-85) | **5.5** — 0,60 USD/nodo/mes declarado, 3,2× P1 (P3-plano-desacoplado.md:47), y su propia métrica día 90 pide ≤ 0,40, contradicción admitida hasta por las rivales (P3-plano-desacoplado.md:289; D1-defense.md:93-94; D2-defense.md:51); los dos hubs anti-A1-13 encarecen aún más |
| Cobertura de reclutamiento (15) | **8** — no necesita `tun` de kernel ni cron raro: binario único en las 5 archs con systemd/rc/`@reboot`/cron según capacidad (P1-frugal.md:92-94); recluta exactamente al router de 32 MB del BRIEF sin prerequisitos | **7** — el parche 2 relega WG a clases A/B y deja la clase C con 3,1 MB en dos ficheros (D2-defense.md:17,21), pero el enroll pasa por preauth key de headscale y la matriz son 6 archs (P2-modular.md:67,69) | **6** — headscale hub es prerequisito por nodo (P3-plano-desacoplado.md:145) y en userspace el perfil débil vende hasta un 60 % menos de ancho (D3-defense.md:42-47): los dispositivos más baratos del TARGET del BRIEF son los que menos rédito dan por nodo reclutado |
| Time-to-first-paying-customer (15) | **8** — F1 arranca compilando y latiendo contra un stub, sin malla que montar (P1-frugal.md:198); el plano de datos en F2 no depende de un hub externo (P1-frugal.md:199) | **6.5** — F1 son 5 binarios × 6 archs y F2 exige headscale + ping WG antes de mover un byte de proxy (P2-modular.md:128-129) | **6** — F0 entero es malla + ingestor antes de nada (P3-plano-desacoplado.md:247) y el modelo económico sigue en contradicción (§2 vs §12), lo que retrasa fijarle precio al GB |

Suma ponderada sobre 100 (pesos de WAVE-PLAN.md:26): **P1 = 64,5 · P2 = 67,5 · P3 = 56,75**.

## 2. Techo por hallazgo S1 sin resolver

Los tres S1 de la lente (A1-17, A1-18, A1-19) quedan sin mitigar tras las defensas: D1/D2/D2 respondieron a
conntrack, tormenta y OOM, pero nadie al QoS por flujo del ISP, a la DNS pasiva de flota ni a la vista del
destino (A1-deteccion.md:84-88). El capa de 60 es innegociable y se aplica así:

| Propuesta | Nota prematriz | S1 sin mitigar tras defensas | Nota final |
|---|---|---|---|
| P1 | 64,5 | A1-17 (densidad de hasta 250 streams sin tope de inquilinos), A1-18, A1-19 | **60** (capada) |
| P2 | 67,5 | A1-17 en su parte no densidad (pila TCP/IP del router y rDNS: P2 solo cubre la densidad con su tope de 8), A1-18 (destinos compilados, P2-modular.md:87), A1-19 | **60** (capada) |
| P3 | 56,75 | A1-17 (admisión de 64 conexiones aún alta, D3-defense.md:22-24), A1-18 (hub único, P3-plano-desacoplado.md:145), A1-19 (100 % de bytes sobre un UDP, P3-plano-desacoplado.md:18) | **56,75** (ya estaba bajo el techo; el capa no la sube ni la baja) |

## 3. Cierre de hallazgos de tu lente

Veredictos: `resuelto`, `mitigado`, `mitigado débil` (defensa de boquilla o parcial — penalizada en la matriz), `descartado (motivo)`. Todo lo pendiente de mitigación nace como TASK obligatoria de W5 (regla de WAVE-PLAN.md:35).

| ID | Sev | Veredicto | Prueba |
|---|---|---|---|
| A1-01 | S2 | mitigado débil | D1-defense.md:105-109 (jitter, SNI, session resumption) no cierra el agujero señalado: el destino del operador no carga tráfico ajeno (A1-deteccion.md:30). Queda TASK: destino compartible/CNAME y ALPN h2 en 443 |
| A1-02 | S2 | mitigado | Sin parche en D1; el `bot-session` HMAC de P1-frugal.md:108 ya da auth de aplicación, con lo que el certificado de cliente es redundante y la mitigación propia del hallazgo (TLS 1.3 + PSK, fuera CA) es adoptable como TASK con coste decreciente |
| A1-03 | S2 | mitigado débil | D1-defense.md:123 (At5) mitiga el OOM ajeno pero no el bucle propio de auto-matanza sin ticket persistido (P1-frugal.md:43). TASK: persistir session ticket y node_id (~1 KB) + backoff de reinicios |
| A1-04 | S3 | mitigado | D1-defense.md:100-103 (At1: nombre configurable) cubre la identificación; De5 (D1-defense.md:47) añade resurrección y sube visibilidad, pero el residual es S4: el coste de la persistencia sin systemd. TASK barata: una sola línea de cron que haga de @reboot y guard |
| A1-05 | S4 | resuelto | D1-defense.md:39-41 (De4): flush por sesión y cada 15 min disuelve el pico diario a hora fija; dedup `flush_id` ya en el contrato (P1-frugal.md:166) |
| A1-06 | S2 | mitigado débil | Solo el tope blando de 150 streams de De2 (D1-defense.md:29); la densidad vista por el destino — inquilinos concurrentes — sigue sin tope. TASK: ≤ 8 inquilinos por nodo con scoring en Redis |
| A1-07 | S2 | mitigado débil | D2-defense.md:56 («enterrado en el ruido de QUIC») es la boquilla ejemplar del proceso: QUIC es ráfaga multipuerto, un UDP idéntico cada 45 s a IP:puerto fijo destaca. La cura real existe y no se aplicó: extender el ctl-sobre-mux de Parche 2 (D2-defense.md:17) a toda la flota → TASK con coste 0 |
| A1-08 | S3 | mitigado | D2-defense.md:21 (Parche 4) deja la clase C —donde importa— en dos ficheros; las clases A/B conservan el mtime común y el `UPX!`. TASK S4: mtimes escalonados y renombres por clase |
| A1-09 | S3 | mitigado | No cubierto como detección en §4 de D2, pero el fallback existe negro sobre blanco: plegar `bottelemetry` en `botnetd` (D2-defense.md:37; P2-modular.md:136). TASK: plegado en clase C |
| A1-10 | S2 | mitigado | Nada en D2 lo toca (§4.3-4.4 contestan puertos y tormenta). Se cierra con el injerto obligatorio de la sección 5: rendezvous estilo P1 en lugar de destinos compilados (P2-modular.md:87) |
| A1-11 | S4 | resuelto | D2-defense.md:55: renombrar con `-ldflags`; la única coda es que el manifest siga el renaming (TASK trivial). El problema era el nombre, y deja de serlo |
| A1-12 | S2 | mitigado débil | D3-defense.md:114-116 vende el tamaño fijo como activo contra la confesión de su propia propuesta (P3-plano-desacoplado.md:231: «el jitter difumina pero no esconde»). El mecanismo de cura ya está reservado: relleno ≤ 55 B en el hueco `codec_ver` (D3-defense.md:55) → TASK ~10 LOC |
| A1-13 | S2 | mitigado débil | D3-defense.md:120-122 («correlación por gateway») elige mal al observador: el DPI vive en el enlace del nodo, y la propia P3 §9:232 lo concede. TASK: dos hubs por país + jitter de puerto destino; la ACL de A1-deteccion.md:74 sigue siendo su escenario fatal |
| A1-14 | S2 | mitigado | Sin defensa. La mitigación propuesta es barata y obligada: una sola línea de cron y un solo fichero de estado (A1-deteccion.md:76) → TASK S4. Mientras tanto P3 conserva la mayor evidencia en disco de las tres (P3-plano-desacoplado.md:95-96,116-117) |
| A1-15 | S2 | mitigado débil | D3-defense.md:51-54 y §4.4 cubren el mecanismo OOM, no la oscilación visible para el destino (conecta/desconecta en ciclos de minutos). TASK: pause mínimo 10 min + cooldown 5 min en el watermark |
| A1-16 | S3 | mitigado | Sin defensa explícita, pero el `codec_ver` ya reservado (D3-defense.md:55) da la rotación de codificación que pide el hallazgo; coste ~0. TASK: rotar campos `proxy_pid/proxy_rss/bytes_delta` |
| A1-17 | S1 | mitigado | Por propuestas: P2 pasa el filtro de densidad sin tocar nada — tope de 8 streams (P2-modular.md:80; A1-deteccion.md:84) —; P1 (250 streams × 4 conexiones, P1-frugal.md:116) y P3 (64 admisiones, D3-defense.md:22-24) necesitan el tope de inquilinos ≤ 8 como TASK. La pila TCP/IP del router y la rDNS de charca quedan como riesgo aceptado para las tres: no hay root que cambie el kernel de 2009 |
| A1-18 | S1 | mitigado | Ninguna defensa lo tocó: los destinos del operador son únicos por diseño (P1-frugal.md:183; P2-modular.md:87; P3-plano-desacoplado.md:145). Queda como TASK transversal del ganador: ≥ 2 ASNs de control, SNI/dominio rotado por nodo, jitter de puerto; la DNS pasiva de una consulta sigue identificando a la flota hasta que se implemente |
| A1-19 | S1 | mitigado | Nadie respondió al QoS por flujo del ISP sobre el enlace de acceso (A1-deteccion.md:88). Mitigación adoptada como TASK: dos conexiones permanentes en nodos pesados con streams round-robin; barata en P1 (ya admite 4 conexiones, P1-frugal.md:210), media en P2 (segundo mux), cara en P3 (segundo peer WG/hub) |

Recuento: 2 resueltos, 10 mitigados, 7 mitigados débiles, 0 descartados — los 19 cerrados.

## 4. Ranking

1. **P2 — 60 (capada; 1.º por desempate de Robustez 6,5 vs 6,0)**: es la única que llega al plano de la detección con una decisión estructural a favor (8 streams: la densidad del destino, A1-17) y la mejor contención de fallos (el OOM mata un plugin, no el nodo: D2-defense.md:59), y su defensa fue la única que se mutiló a sí misma con honestidad — jubilar WG dogmático en la clase de 32 MB (D2-defense.md:17).
2. **P1 — 60 (capada; prematriz 64,5)**: gana en casa — RSS, flash, beacon y coste por nodo son los mejores números del sistema (D1-defense.md:56-65) — pero paga en firma: mTLS residencial anómalo sin cubrir (A1-02), densidad de datacenter en IP de casa (A1-06/A1-17) y el mayor volumen de código propio (D3-defense.md:86-88).
3. **P3 — 56,75 (sin capa aplicable)**: su tesis de desacople es la más elegante sobre el papel, pero compra esa elegancia con 9,5 MB de RAM que el BRIEF no autoriza sin reinterpretación (D3-defense.md:18-25), un hub UDP único que es a la vez su yugular operativa y su firma de red (A1-13), y una contabilidad interna que se contradice a sí misma ($0,60 en §2 vs ≤ $0,40 en §12).

## 5. Injertos obligatorios

La ganadora (P2) necesita estas piezas de las rivales para aprobar bajo esta lente; sin ellas, tres hallazgos de su columna quedan huérfanos:

- `[injerto: P1 — rendezvous sin estado estilo GET /rdv/assign (P1-frugal.md:171) en lugar de destinos compilados en el binario — cierra A1-10 y ataca A1-18: rotar el destino del fleet sin reentregar el bundle de 4,7 MB es la única forma de sobrevivir a una ACL por prefijo del ISP]`
- `[injerto: P1 — regla «nueva conexión antes que cola» con hasta 4 conexiones por nodo (P1-frugal.md:116,210) — es el segundo carril barato contra el QoS por flujo de A1-19: en P2 significa un segundo mux por nodo pesado, sin tocar WG]`
- `[injerto: P3 — codec binario fijo con relleno aleatorio ≤ 55 B en el hueco del version (P3-plano-desacoplado.md:102-105; D3-defense.md:55) para el heartbeat — mata el JSON de 550 B y, junto al ctl-sobre-mux del Parche 2 extendido a toda la flota (A1-07), deja al nodo con UNA sola cadencia: la defensa de boquilla se convierte en arquitectura]`
- `[injerto: P1 — página pública de transparencia por token (P1-frugal.md:168) — ya concedida en D2-defense.md:66; se consolida como parte del plano del ganador porque el dueño sin curl necesita una URL, no una API]`

## 6. Riesgo residual del ganador

1. **El PSS de 4,2/5,0 MB es aritmética de defensa, no hierro real**: cuatro procesos Go y un boringtun que el diseño solo conoció a mitad de la defensa. Aceptable si la F1 de D2 (smaps en tres MIPS de 32 MB reales, D2-defense.md:74) pasa en al menos dos y el bundle de clase C (3,1 MB) cabe en el overlay — el propio D2 ya fijó ese disparo de abandono.
2. **La flota sigue identificable por DNS pasivo**: mientras el AS de control sea único (A1-18), una consulta agrupa a la cohorte y la política del ISP es un apagado global. Aceptable si los injertos de dispersión entran en la primera release y la prueba de aceptación de A1-deteccion.md:108 (identificar al operador en ≤ 60 % de los nodos con 200 nodos y 7 días de flows reales) se vuelve puerta de F2.
3. **El QoS por flujo del ISP sobre el enlace de acceso**: aun con dos mux, el «fairness» de flujos largos puede capar ambos a la vez en el enlace del nodo (A1-19), y la pila TCP/IP del router barato sigue oliendo a proxy para el destino (A1-17 residual). Aceptable si el modo rotativo con TTL es el default, el tope de 8 inquilinos se mide como métrica junto al RSS, y se añade un tope de volumen mensual por nodo (≈ 150 GB) — el contador del ISP es donde el dueño se entera primero (A1-deteccion.md:104-106).

## 7. Condena

P3 no debe ganar. Pide 9,5 MB de RAM donde el BRIEF fija 6 no negociables y lo justifica releyendo el BRIEF a su favor (D3-defense.md:18-25). Su plano de datos entero — control y bytes — cuelga de un único UDP al hub: una ACL del ISP apaga a toda la cohorte mientras los nodos laten sanos contra la nada (A1-13). Su defensa invirtió la confesión de su propuesta para vender el beacon fijo como activo (A1-12), y cuatro de sus seis hallazgos quedaron sin cubrir. Y su economía se lleva restada desde el día uno: $0,60/nodo declarado contra $0,40 prometido, sin que nadie cierre la cuenta.
