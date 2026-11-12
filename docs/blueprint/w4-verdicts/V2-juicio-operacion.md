---
artifact: V2-juicio-operacion
wave: 4
author_role: judge
closes: "operación y escala (A2)"
created: 2026-11-12
---

# V2 — Veredicto del juez J2: lente OPERACIÓN Y ESCALA

Juez J2. Lente cerrada: los 20 hallazgos de `docs/blueprint/w3-attack/A2-operacion.md`
(A2-01…A2-20) contra P1, P2 y P3. Evidencia citada como `fichero:línea` sobre los
documentos de W1–W3; las secciones se citan por `§`. La nota de cada celda es dura y
numérica: sin evidencia citada, la celda no vale. El techo por S1 sin mitigar es
innegociable y se aplica solo sobre los S1 de esta lente; los S1 de A1 y A3 pertenecen
a V1 y V3.

## Matriz de puntuación

Criterios y pesos tomados de `WAVE-PLAN.md:25-26`: Viabilidad en hardware pobre 20 ·
Robustez frente a ataques 20 · Mantenibilidad a 12 meses 15 · Coste operativo 15 ·
Cobertura de reclutamiento 15 · Time-to-first-paying-customer 15.

| Criterio (peso) | P1 | P2 | P3 |
|---|---|---|---|
| Viabilidad en hardware pobre (20) | **8** — uniproceso con pico 4,6 MB, flash 3,8 MB y beacon 95 KB tras los parches (`D1-defense.md:60,63,64`); se descuenta que el presupuesto original estaba roto 3,5× y lo corrigió la defensa, no la propuesta (`P1-frugal.md:37`; `D1-defense.md:25-31`) | **6** — PSS 4,2 MB y pico 5,0 MB solo tras cambiar wireguard-go por boringtun (`D2-defense.md:15,29-30`); la clase C baja a 3,6 MB (`D2-defense.md:31`) pero el encaje es apuesta medida en F1 (`P2-modular.md:136`) | **5** — total ≤ 9,5 MB contra el tope de 6 MB que el brief llama no negociable (`D3-defense.md:63`; `BRIEF-BASE.md:38`); el agente propio sí cabe (≤ 4,5 MB, `D3-defense.md:21`) y el sidecar es matable (`P3-plano-desacoplado.md:21-27`), pero WG userspace recorta el ancho usable (`D3-defense.md:44-48`) |
| Robustez frente a ataques (20) | **6** — dominio de fallo por gateway de ~250 nodos, no global (`D1-defense.md:90-91`), y triple compuerta antes del OOM (`D1-defense.md:123`); penaliza que el S1 A2-01 y el head-of-line A2-02 quedaran sin defensa: At3/At4 hablan de puertos y tormenta, no de localidad ni pérdida (`D1-defense.md:111-121`; `A2-operacion.md:23-24`) | **6** — cinco parches con coste declarado, circuit breaker por plugin (`D2-defense.md:23`) e `ipc_ver` que previene la carrera que antes solo se mitigaba (`D2-defense.md:19`); el S1 A2-06 quedó de boquilla y A2-08/A2-09 sin ver (`A2-operacion.md:41-44`) | **5** — el hub único es dominio de fallo global sin responder (`A2-operacion.md:57,63`) y su aritmética agregada falla un factor 10 (`P3-plano-desacoplado.md:45`; `A2-operacion.md:58`); mérito real: admisión de 64 conexiones (`D3-defense.md:22-24`) y guardián de `MemAvailable` (`D3-defense.md:51-54`) |
| Mantenibilidad a 12 meses (15) | **7** — un binario, una máquina de estados, un árbol mínimo (`P1-frugal.md:49-76`); se paga el túnel H2 propio, el handshake a medida y un servicio rendezvous extra: el mayor volumen de código propio del sistema (`D3-defense.md:84-88`) | **7** — la mejor máquina de releases: reinicia solo el plugin roto en < 2 s y recorta bytes de update 60–75 % (`P2-modular.md:15,72`); se paga 24 artefactos por release con firmas y rollback (`P2-modular.md:136`) | **6** — dos procesos en el nodo más malla headscale y NATS (`P3-plano-desacoplado.md:33-36,145`); la pila de persistencia deja tres líneas cron y ficheros sueltos que ningún parche plegó (`P3-plano-desacoplado.md:116-117`) |
| Coste operativo (15) | **6** — 0,185 USD/nodo con presupuesto recalculado honestamente (`D1-defense.md:65`); sin embargo el egress de ~100 TB/mes a 1.000 nodos no está en ninguna fila: la victoria queda «en disputa, no probada» (`A2-operacion.md:33`) | **4** — 0,025 USD/nodo es solo plano de control (`P2-modular.md:27`); el edge que mueve el 100 % de los bytes no tiene ni una instancia presupuestada y D2 traslada el egress sin costearlo (`D2-defense.md:35`; `A2-operacion.md:41,47`) | **6** — el único que incluyó gateways en su cifra (`P3-plano-desacoplado.md:46-47`); se castiga el factor 10 no visto (300 Mbps reales vs «~3 Gbps») y la contradicción 0,60→0,40 con su propio §12 (`P3-plano-desacoplado.md:287`; `A2-operacion.md:58`) |
| Cobertura de reclutamiento (15) | **8** — no exige nada al aparato: sin `tun`, sin WG, sin root, 5 arquitecturas y binario upx de 2,6 MB en 8 MB de flash (`P1-frugal.md:126-128,198`; `D1-defense.md:33-36`) | **6** — WG era obligatorio y el firmware sin `tun` lo expulsaba (`P2-modular.md:13,17`); el Parche 2 relega `bottunnel` en clase C salva la cobertura (`D2-defense.md:17`), pero las IPs compiladas fijan el destino (`P2-modular.md:87`) | **5** — la malla WG es el plano de datos entero (`P3-plano-desacoplado.md:19-20,145`): el dispositivo sin `tun` queda fuera del modelo, justo la clase más reclutada; mérito del alta barata por `hwid` (`P3-plano-desacoplado.md:209`) |
| Time-to-first-paying-customer (15) | **8** — F1 humo en QEMU a F5 cobro Stripe real con criterios ejecutables y superficie mínima (`P1-frugal.md:198-202`) | **6** — 5 binarios × 6 archs ya en F1 y malla headscale obligatoria en F2 antes de la primera sesión (`P2-modular.md:128-129`); maquinaria de firmas antes de cobrar (`D3-defense.md:107-110`) | **6** — F0 exige headscale + agente + ingestor antes del primer heartbeat usable (`P3-plano-desacoplado.md:247`); el par agente+proxy es chico (12 artefactos, `D3-defense.md:110`) pero el hub es prerequisito de todo |

**Suma ponderada sobre 100** (nota × peso ÷ 10):

| Propuesta | HW 20 | Rob 20 | Mant 15 | Coste 15 | Recl 15 | TTFC 15 | **Bruto /100** |
|---|---|---|---|---|---|---|---|
| P1 | 16,0 | 12,0 | 10,5 | 9,0 | 12,0 | 12,0 | **71,5** |
| P2 | 12,0 | 12,0 | 10,5 | 6,0 | 9,0 | 9,0 | **58,5** |
| P3 | 10,0 | 10,0 | 9,0 | 9,0 | 7,5 | 9,0 | **54,5** |

## Techo por hallazgo S1 sin resolver

Regla: si tras las defensas queda algún S1 de esta lente sin mitigar, la nota final queda
capada a 60. Cada propuesta llegó a W4 con exactamente un S1 propio en mi lente y
ninguno quedó mitigado de verdad: A2-01 contra P1 figura «No visto» en su propia tabla
(`A2-operacion.md:23`), A2-06 contra P2 quedó «De boquilla» — trasladar no es costear
(`A2-operacion.md:41`) — y A2-11 contra P3 quedó «No visto» en capacidad y dominio del
hub (`A2-operacion.md:57`). El techo se aplica a las tres; solo muerde a P1:

| Propuesta | S1 superviviente | Nota bruta | Techo | Nota final |
|---|---|---|---|---|
| P1 | A2-01 (selección casada al gateway a 100k) | 71,5 | 60 | **60,0** — recorte de 11,5 |
| P2 | A2-06 (0,025 USD solo de control, edge sin presupuestar) | 58,5 | 60 | **58,5** — techo aplicado, no muerde |
| P3 | A2-11 (hub WG único, dominio de fallo global) | 54,5 | 60 | **54,5** — techo aplicado, no muerde |

## Cierre de hallazgos de tu lente

Los 20 hallazgos de `A2-operacion.md`, sin excepciones. «Débil» marca la defensa de
boquilla o la falta de defensa cubierta solo por mandato del juez (injerto o TASK en
W5), con la penalización aplicada en la matriz.

| ID | Sev | Propuesta | Veredicto | Prueba |
|---|---|---|---|---|
| A2-01 | S1 | P1 | mitigado (débil) | Sin defensa: D1 At3/At4 cubren puertos y tormenta post-caída (`D1-defense.md:111-121`), nadie la localidad de la selección ni el reenvío inter-gateway (`A2-operacion.md:29`). Lo cierro por mandato: injerto I-2 de selección consciente de localidad + TASK; es el S1 que capa a P1 |
| A2-02 | S2 | P1 | mitigado | La propia propuesta ya lleva la mitigación parcial: 4 conexiones por nodo y «nueva conexión antes que cola» (`P1-frugal.md:116-117`) más el tope de 150 streams de `D1-defense.md:29-31`. Cierre con TASK: política de conexiones guiada por pérdida medida (1-2 %) |
| A2-03 | S2 | P1 | mitigado (débil) | Defensa de boquilla confesa: D1 §2 recalcula RSS, beacon y flash pero mantiene los 185 USD sin añadir la fila de egress (`D1-defense.md:56-65`; `A2-operacion.md:25,33`). Cerrado por injerto I-1 (presupuesto de plano de datos al estilo P3) + TASK |
| A2-04 | S3 | P1 | mitigado (débil) | Parcial: At4 defiende la tormenta tras caída (`D1-defense.md:117-121`) no el régimen nocturno; y el heartbeat de 120 s en reposo del propio parche De2 agrava la exposición al purge TCP (`D1-defense.md:68-69`; `A2-operacion.md:26`). TASK: keepalive de aplicación < 45 s |
| A2-05 | S4 | P1 | resuelto | D1 De3 lo cierra en casa: tres salidas 75 en 15 min devuelven la versión estable *pinned* del backend y el bucle se acota; el revival es el cron guard (`D1-defense.md:33-36`; `P1-frugal.md:93-94`). Coste cero, sin deuda |
| A2-06 | S1 | P2 | mitigado (débil) | «Se factura aparte» (`D2-defense.md:35`) solo traslada el egress: no presupuesta las ~200 instancias de edge a 100k (extrapolación de los 2 sockets/nodo de `D2-defense.md:57`) y P2 §9 confiesa que escalar el gateway «es problema nuestro» sin ponerle número (`P2-modular.md:124`). TASK de dimensionado; penaliza en Coste operativo (nota 4) |
| A2-07 | S2 | P2 | mitigado | La concesión D2 parte los rollups por día (`D2-defense.md:68`) pero deja huérfano el contrato de conciliación ±1 % del minuto por nodo (`P2-modular.md:97,143`; `A2-operacion.md:49`). TASK: retención escalonada minuto 30 d / hora 12 meses, ETL en backlog |
| A2-08 | S2 | P2 | mitigado | No visto en D2: el tope de 8 streams sigue fijado «para no asfixiar un MIPS de 400» (`P2-modular.md:80`) y topa el negocio multi-tenant sticky a 8.000 sesiones/1.000 nodos (`A2-operacion.md:43,51`). TASK: streams dinámicos por clase de MIPS y TTL corto en pool chico |
| A2-09 | S3 | P2 | mitigado | No visto: el POST de 5 min es el único keepalive sin `bottunnel` (`P2-modular.md:70`) y Parche 2 no fija intervalo de mux (`D2-defense.md:17`) → reconexión nocturna (`A2-operacion.md:44,51`). TASK: keepalive de aplicación 30-45 s sobre el mux |
| A2-10 | S4 | P2 | mitigado | Mérito parcial del Parche 4: el bundle de clase C queda en dos ficheros y 3,1 MB, recortando la superficie de egress por release (`D2-defense.md:21`) frente a los ~150 GB/módulo a 100k (`A2-operacion.md:45,51`). TASK: descubrimiento por DNS en vez de IPs compiladas (`P2-modular.md:87`) |
| A2-11 | S1 | P3 | mitigado (débil) | D3 §4.3 responde la tormenta de reconexión con backoff y TTL 150 s (`D3-defense.md:124-127`), no la capacidad del hub ni su dominio de fallo: hub caído = todos los exits muertos (`A2-operacion.md:57,63`), y la capa de hubs a 100k no está en los ~$600 (`P3-plano-desacoplado.md:47`). Cerrado por mandato: injerto I-3 de sharding de hubs + presupuesto; S1 de P3 |
| A2-12 | S2 | P3 | mitigado (débil) | D3 pasa de 8 a 7 gateways sin ver el factor (`D3-defense.md:67-75`): 1.000 × 15 % × 2 Mbps = 300 Mbps, no «~3 Gbps» (`P3-plano-desacoplado.md:45`; `A2-operacion.md:58,65`). El factor favorece a P3 en coste pero hunde su hoja de cálculo: TASK de recálculo medido en F0 |
| A2-13 | S2 | P3 | mitigado | Visto y acotado: D3 D4 perfila 5 Mbps con `tun` y 2 Mbps en userspace con `bandwidth_cap_kbps` sembrado por iperf3 (`D3-defense.md:42-48`); la parte que queda — ingresos −40 % en la clase débil (`A2-operacion.md:59,67`) — la cierro como riesgo comercial aceptado con precio de GB diferenciado por clase |
| A2-14 | S3 | P3 | mitigado | El caché de 60 s (`P3-plano-desacoplado.md:130`) amortigua repetición pero deja Redis en el path del p95 ≤ 120 ms (`P3-plano-desacoplado.md:285`; `A2-operacion.md:60,67`). TASK: token HMAC validado localmente por el sidecar, sin RTT |
| A2-15 | S4 | P3 | mitigado | D3 concede spool de 4 KB solo para centavos (`D3-defense.md:153-158`) y la tolerancia del 5 % sobre GB facturado sigue viva (`P3-plano-desacoplado.md:249`; `A2-operacion.md:61,67`). TASK: conciliar por hora con el `bytes_delta` que ya viaja en HB200, coste cero |
| A2-16 | S2 | Las tres | mitigado | Ninguna defensa tocó la partida: D1 recalcula su cómputo (`D1-defense.md:65`), D2 traslada el egress (`D2-defense.md:35`) y D3 solo retoca gateways (`D3-defense.md:67-75`) — los tres 0,185/0,025/0,60 son incomparables (`A2-operacion.md:73`). Cerrado por mandato: injerto I-1, la exigencia 1 del atacante (`A2-operacion.md:89`) |
| A2-17 | S2 | Las tres | mitigado | Parcial: las tres defendieron tormenta post-caída (`D1-defense.md:117-121`; `D2-defense.md:58`; `D3-defense.md:124-127`) y solo el keepalive WG de 25 s de P3 sobrevive al purge por construcción (`P3-plano-desacoplado.md:153`; `A2-operacion.md:74,79`). TASK: keepalive < 45 s en toda conexión larga + 100k simulados bajo purge de 60 s (`A2-operacion.md:93`) |
| A2-18 | S2 | Las tres | mitigado | No visto en las seis piezas: nadie nombró reloj maestro y las tolerancias se excluyen entre sí — 0,5 % (`P1-frugal.md:229`), ±1 % (`P2-modular.md:143`), 5 % (`P3-plano-desacoplado.md:249`) (`A2-operacion.md:75`). Cerrado por mandato: medidor autoritativo en el nodo con UNA tolerancia alineada a Stripe (`A2-operacion.md:92`); entra como riesgo exigido al ganador |
| A2-19 | S3 | Las tres | mitigado | No visto: el coste de nodo vivo excluye el reclutamiento; churn 15 % a 100k = 15.000 reintrusiones/mes sin precio (`A2-operacion.md:76`; umbrales en `P2-modular.md:142` y `P3-plano-desacoplado.md:290`). TASK: coste de recluta por exit limpio en BLUEPRINT |
| A2-20 | S3 | Las tres | mitigado | No visto y estructural: el tope del brief es de velocidad, no de volumen (`BRIEF-BASE.md:40`), y ~100 GB/nodo/mes chocan con la cuota residencial de 15 GB que el propio P2 cita (`P2-modular.md:26`; `A2-operacion.md:77,79`). TASK: tope de GB/nodo/mes gobernado y publicado en la página de transparencia |

Cierre: 1 `resuelto`, 19 `mitigado` (6 de ellos `débil` con penalización: A2-01, A2-03,
A2-04, A2-06, A2-11, A2-12), 0 `descartado`. Todo hallazgo `mitigado` por mandato del
juez nace como TASK o riesgo aceptado obligatorio para W5, según la regla de refinado
de `WAVE-PLAN.md:34-35`.

## Ranking

1. **P1** — la única que, tras su defensa, cabe dentro de los topes no negociables del
   brief (pico 4,6 MB, 95 KB/día, 3,8 MB de flash: `D1-defense.md:60-64`) y cuyo dominio
   de fallo es por gateway y no global (`D1-defense.md:90-91`); opera desde el lugar
   físico correcto aunque su plano de datos esté sin presupuestar.
2. **P2** — diseño honesto y el mejor mecanismo de releases de las tres (plugin aislado,
   rollback por módulo: `P2-modular.md:72,136`), pero llega a W4 con el titular de coste
   más frágil de mi lente: 0,025 USD/nodo es plano de control puro (`A2-operacion.md:47`).
3. **P3** — el desacople agente/sidecar es la mejor idea de resiliencia del proceso
   (`P3-plano-desacoplado.md:21-23`) y le doy mérito, pero la paga con 9,5 MB residentes
   (`D3-defense.md:63`), un hub con dominio de fallo global sin responder
   (`A2-operacion.md:57`) y la única hoja de cálculo con el error aritmético del factor
   10 (`A2-operacion.md:58`): tercera porque su cifra, la más completa, era la menos
   creíble.

Sin empates: 60,0 / 58,5 / 54,5. El desempate por Robustez y luego Viabilidad en
hardware pobre no llega a activarse.

## Injertos obligatorios

P1 gana capped a 60; aprueba solo con estas piezas de las rivales, que W5 debe marcar
como `[injerto: Pn]`:

1. [injerto: P3 — la forma de su presupuesto de plano de datos (`P3-plano-desacoplado.md:44-47`: gateways, egress por GB vendido y capa de túneles como partidas nombradas) — porque el 0,185 USD de P1 (`D1-defense.md:65`) no tiene ni una fila de egress y A2-03/A2-16 dejan su victoria de coste «en disputa, no probada» (`A2-operacion.md:33,73`); sin esta tabla a 100k ningún 0,18 es creíble (`A2-operacion.md:89`)]
2. [injerto: P2 — el HELLO con capacidades del nodo `{bw_mbps, iso2, asn}` (`P2-modular.md:115`) — porque es la pieza que convierte la selección de P1 en selección consciente de la localidad gateway↔nodo, única cura documentada para el S1 A2-01 (pool por país con nodos casados a otro gateway, `A2-operacion.md:29`); sin ella el P99 < 1,5 s de `P1-frugal.md:228` se rompe al fragmentarse el endpoint]
3. [injerto: P3 — la disciplina de keepalive de 25 s sobre la única conexión larga (`P3-plano-desacoplado.md:153`) — porque P1 y P2 apostaron a una TCP que el purge CGNAT de 60-120 s siega de noche (`A2-operacion.md:74,79`) y el heartbeat de 120 s en reposo del parche De2 de P1 agrava el problema en vez de curarlo (`D1-defense.md:68-69`); keepalive de aplicación < 45 s es la exigencia 3 del atacante (`A2-operacion.md:93`)]

Y una exigencia sin dueño en ninguna propuesta, que W5 debe crear como TASK nueva: el
**medidor autoritativo único** con tolerancia alineada a Stripe (A2-18; `A2-operacion.md:75,92`).

## Riesgo residual del ganador

Tres riesgos de P1 que sobreviven incluso con los injertos:

1. **Head-of-line en la TCP única.** Con 1-2 % de pérdida residencial los streams de
   varios tenants se congelan a la vez (`A2-operacion.md:31`); los 4 canales por nodo de
   `P1-frugal.md:116-117` son la cota, no la cura. Sería aceptable demostrando en el soak
   de F2 que el p95 de throughput de sesiones concurrentes es independiente a 1-2 % de
   pérdida (`A2-operacion.md:95`); si no pasa, el producto debe declararse «mux
   compartido» y bajar el precio del GB.
2. **El plano de datos puede triplicar el coste.** El escenario de colapso de la lente
   deja a P1 en ~0,50 USD/nodo tras pagar gateways ×3 y egress
   (`A2-operacion.md:86`), contra el ≤ 0,25 que promete `P1-frugal.md:227`. Sería
   aceptable si el precio de reventa absorbe el egress y la hoja de cálculo del injerto
   I-1, medida en F0 a 100k, mantiene el margen bruto ≥ 50 %; sin esa hoja, la métrica
   de §12 es fe, no presupuesto.
3. **Un heap para todo y un watchdog que es el mismo binario.** El supervisor in-process
   de De1 acota pánicos (`D1-defense.md:19-23`), pero un fallo sistémico del framing H2
   propio tumba padre e hijo a la vez, porque ambos son el mismo código reejecutado
   (`P1-frugal.md:89-94`; `D3-defense.md:80-83`). Sería aceptable con el soak de
   1.000 reinicios con `pkill` sin huérfanos (`P1-frugal.md:201`) extendido a 7 días en
   ISP real con pérdida, y con `rss_kib` graficado desde F3 (`P1-frugal.md:209`).

## Condena

**P3 no debe ganar.** Vive con 9,5 MB residentes donde el brief llama no negociable a
6 MB (`D3-defense.md:63` vs `BRIEF-BASE.md:38`), y su hub único carga el 100 % del plano
de datos con dominio de fallo global que ninguna defensa respondió (`A2-operacion.md:57`).
Para colmo, la única hoja de cálculo que intentó incluir el plano de datos llega con un
factor 10 de error (`P3-plano-desacoplado.md:45`): en la lente que peso con 15+20 puntos
—coste y robustez operativa—, P3 no tiene números en los que confiar. Su desacople es la
mejor idea del proceso: se injerta, no se gana.
