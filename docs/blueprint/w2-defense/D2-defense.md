---
artifact: "Defensa D2 — Agente modular con plugins firmados"
wave: 2
author_role: defender
defends: P2
created: 2026-11-10
---

# D2 — Defensa de la arquitectura modular (P2)

Defiendo P2: supervisor `botnetd` más los plugins firmados `bottunnel`, `botproxy` y `bottelemetry`. Esta defensa lo somete, no lo alaba: cinco debilidades parcheadas con coste medible, presupuesto recalculado, refutación de P1 y P3 citando sus secciones y seis respuestas a W3. La tesis sobrevive con una enmienda: WireGuard deja de ser dogma en la clase de 32 MB, donde manda el mux ya abierto.

## 1. Parche de las debilidades propias

**Parche 1 — la suma de PSS de cuatro binarios Go distintos no se comparte.** W1 §2 descontaba páginas compartidas del runtime Go, pero el descuento vale dentro del mismo fichero mapeado: cuatro binarios duplican cada uno su runtime y su heap, y los 6,0 MB en reposo quedaban con margen cero ante un GC voraz de `botproxy`. Corrección: sustituir el wireguard-go userspace de `bottunnel` por boringtun (Rust, sin GC), que baja su RSS a ~0,6 MB, y `GOMEMLIMIT=2MiB` + `GOGC=100` en los plugins Go. Alternativa descartada: subir a 7 MB; el BRIEF fija ≤ 6 MB (restricción 4). Coste: toolchain Rust/musl extra en la matriz, ~150 ms de build por arch y +15 % de CPU en la ruta WG.

**Parche 2 — WireGuard obligatorio sobre firmwares que no traen `tun`.** Mi §1 admite que el WG de kernel casi no existe y mi §2 confiesa que la cripto userspace es el mayor coste de CPU: 2,4 MB residentes por un RPC que ocurre minutos al mes es caro. Corrección: añado frames `CTL_COMMAND` al mux TLS que `botproxy` ya mantiene hacia el edge — es bidireccional — y `bottunnel` queda relegado a las clases A/B con holgura; la identidad no cambia: `wg_pubkey` sigue en la fila `nodes`. Alternativa descartada: eliminar WG en toda la flota como P1; pierde IP privada y RPC cuando el nodo pierde el mux (reinicio de edge). Coste: ~600 líneas entre `botproxy` y el gateway; ahorra 0,6–2,4 MB de PSS en la clase C, el router de 32 MB real.

**Parche 3 — matriz de 24 artefactos con un IPC sin versión.** La matriz 6×4 no impedía que `botproxy` v2 hablara con un `botnetd` v1 por un contrato IPC desplazado; la carrera multiproceso de §11 (riesgo 5) era mitigación, no prevención. Corrección: campo `ipc_ver` en el HELLO del mux y en el manifest, el CI mantiene su puerta de las 24 huellas y `deviceinfo` sirve solo los módulos que la clase admite. Coste: un campo de protocolo y ~10 % del manifest; cero en el dispositivo.

**Parche 4 — flash de 8 MB con overlay útil de ~4 MB.** Mi paquete de 5,5 MB no cabe en la clase C; §11 lo confesaba como riesgo 3 sin tallado. Corrección: bundle mínimo de clase C = `botnetd` + `botproxy` (3,1 MB UPX), estado y slot de update en tmpfs, telemetría plegada en `botnetd` como timer. Perder el slot al reiniciar es aceptado: el manifest vive en el backend. Alternativa descartada: dos slots A/B en overlay — 6,2 MB en un overlay de 4 MB es inviable. Coste: lógica de clases en `deviceinfo`; ahorra 2,4 MB de flash.

**Parche 5 — cascada de crashes de plugins sin root ni systemd.** `botnetd` resucita plugins con backoff, pero dos plugins cayendo en bucle generan tormenta de reinicios y logs que queman flash. Corrección: circuit breaker por plugin (3 crashes en 10 min → drain hasta el tick del cron guard), `ulimit -v` por plugin heredado de P3 y log en ring buffer de RAM con vuelco a spool. Coste: ~60 KB de RSS; cero CPU en régimen estable.

## 2. Presupuesto recalculado

| Magnitud | W1 (§2) | Tras los 5 parches | Margen sobre el límite |
|---|---|---|---|
| RSS en reposo (bundle completo, PSS) | 6,0 MB | 4,2 MB: botnetd 1,6 + bottunnel 0,6 + botproxy 2,0 | 1,8 MB (30 %) sobre 6 MB |
| RSS pico (burst telemetría 0,6 MB, ~3 s) | 7,2 MB | 5,0 MB | 1,0 MB (17 %) sobre 6 MB |
| RSS clase C (32 MB real, sin bottunnel) | 6,0 MB | 3,6 MB (botnetd + botproxy) | 40 % del presupuesto |
| Binarios en disco (UPX) | 4,7 MB (5,5 con config) | 3,7 MB (~4,5 con config); 3,1 MB mínimo | 44 % de 8 MB; clase C: 3,1 + tmpfs |
| CPU en reposo | ≤ 1,5 % de 400 MIPS | ≤ 1,2 % (boringtun no paga scheduler Go) | proxy a 2 Mbps ≈ 10 MIPS sin cambio |
| Bytes/día | ~0,5 MB | ~0,45 MB; ~0,25 MB clase C | 0,15 % de una cuota de 15 GB/mes |
| Coste por 1.000 nodos | 25 USD + 0,50 USD release | 25 USD + 0,40 USD (egress 3,7 GB/release) ≈ 0,025 USD/nodo/mes | plano de datos se factura al cliente aparte |

Sobre 32 MB: firmware y kernel dejan 8–12 MB libres; el pico de 5,0 MB deja 3–7 MB para los servicios del dueño, cuando el pico W1 (7,2 MB) no dejaba ninguno. Ningún parche rompe el presupuesto: los cinco lo recortan. Si F1 con `smaps` pasara de 6,0 MB en algún arch, el recorte ya está diseñado: plegar `bottelemetry` en `botnetd` (−0,6 MB de burst) y, si persiste, suprimir `bottunnel` en esa clase; el `ctl`-sobre-mux deja el plano de datos intacto.

## 3. Refutación de las rivales

**Contra P1 (frugal):**

1. *Su control es solo pull.* P1 §6 declara «WireGuard: cero» y mete las órdenes en `pending_cmds[]` del heartbeat, que late cada 90 s (P1 §2); su F3 tolera «0 sesiones nuevas en 60 s» (P1 §10). P2 alcanza al nodo bajo CGNAT por la malla o por el mux ya abierto (P2 §6): segundos, no minutos. P1 sacrificó el canal descendente por no pagar WG.
2. *Un heap para todo y cada release reinicia todo.* El «RSS pico (50 streams) ≤ 6 MB» de P1 §2 es techo, no colchón: proxy, agente y watchdog comparten heap — un parseo de SOCKS5 que filtre se lleva el heartbeat puesto — y el update de P1 §4 (`exit 75`, `--commit` tras 5 min sanos) interrumpe agente, proxy y latido en cada release. P2 §4 reinicia solo el plugin afectado en < 2 s: radio de explosión 1/N frente al 100 % del monolito.
3. *CA de flota con certificado por nodo.* P1 §4 emite certificado mTLS por nodo (TTL 90 días, renovable) y §9 admite que el `device_token` es un hash, no identidad criptográfica. CA online, almacén de clave en el router sin root y revocación: tres superficies que mantener frente a una ed25519 de nodo, un `node_secret` HMAC en HELLO (P2 §8) y la clave WG en `nodes`. P1 paga en el dispositivo lo que P2 paga en Postgres.

**Contra P3 (thin + sidecar):**

1. *10 MB totales contra un presupuesto de 6 MB.* P3 §2 fija «≤ 10 MB» de RSS por nodo (agente 4–5 + sidecar 6) cuando la restricción 4 del BRIEF fija ≤ 6 MB; su §2 dice que el firmware deja 10–14 MB libres, así que P3 reserva casi toda la RAM libre del router. P2 cabe en 4,2 MB de media y 5,0 de pico: la diferencia es el colchón del dueño.
2. *El plano de datos sobre la dirección menos fiable.* P3 §5: el gateway abre TCP al SOCKS5 del nodo «vía malla», sostenida por un keepalive UDP de 25 s (§6). La restricción 1 del BRIEF dice que el dispositivo siempre inicia: en el sentido gateway→nodo, las sesiones mueren si cae la binding UDP del CGNAT. P2 §6 confina WG al control y mueve los datos por TCP+TLS iniciado por el nodo: la dirección que CGNAT nunca rompe.
3. *Sus números no cuadran y su corte es lento.* P3 §2 publica «~$0,60/nodo/mes» y su día 90 (§12) exige «infra ≤ $0,40/nodo/mes»: 50 % de brecha en su propia contabilidad. Y el corte de cuota cierra listeners «≤ 90 s» (P3 §10 F3, §12), 18 veces más lento que los ≤ 5 s de P2 (§5 Liberación, F4): a 2 Mbps son ~22 MB facturados tras agotar cuota frente a ~1,2 MB.

## 4. Ataques anticipados y respuesta

1. **Detección** — «cuatro procesos con nombres evidentes y un UDP periódico: huella de manual». Respuesta: P2 §9 compra tráfico saliente uniforme, no sigilo; los nombres son identificadores de build (se renombran con `-ldflags`) y un POST de retiro lo limpia todo si el dueño con root lo descubre. El sigilo extremo no está en el BRIEF; la limpieza operativa, sí.
2. **DPI del ISP** — «una TLS larga más UDP periódico: flujo clasificable y correlacionable». Respuesta: todo saliente, con SNI a un edge en AS propio; el keepalive WG de 45 s queda enterrado en el ruido de QUIC y voz del hogar. No prometo vencer al DPI: lo acepto, como P1 y P3, y lo pago con 0,45 MB/día de control, dos órdenes por debajo del plano de datos correlacionable.
3. **Agotamiento (bindings y puertos NAT)** — «el CGNAT purga UDP y las tablas NAT se llenan». Respuesta: keepalive por debajo del timeout UDP típico, y si el ISP purga agresivo, el `ctl`-sobre-mux del Parche 2 mantiene el control vivo aunque el túnel muera. El nodo abre 1–2 sockets TCP por par edge (P2 §5), no 250 streams de P1 ni N conexiones entrantes de P3: 1.000 nodos, ~2.000 sockets en el edge, trivial.
4. **Tormenta de reconexión** — «cae el edge o el hub y 1.000 nodos reconectan a la vez». Respuesta: jitter ±20 % en heartbeat y backoff con jitter en stager, beacon y plugins caídos (P2 §4); si cae el hub WG no se pierde ni una sesión porque los datos no viajan por WG (P2 §6) — el escenario que hunde al hub de P3, que está en medio de cada byte de cliente. El mux reconecta con `HELLO` reintentado contra cualquier edge del pool Redis.
5. **OOM** — «el OOM killer de 32 MB elige tu mayor proceso». Respuesta: `ulimit -v` y `GOMEMLIMIT` hacen que el killer mate al culpable acotado — casi siempre `botproxy`: mueren sesiones, sobrevive el control, el watchdog reanima en < 90 s y el lease TTL 60 s del gateway absorbe el hueco. Contra un OOM sistémico, P1 cae entero con su monolito; P2 pierde solo el plugin.
6. **Legal** — «el dispositivo no es nuestro; consentimiento visible y baja que no sobreviva al firmware». Respuesta: el retiro (P2 §4) borra estado, peer WG y persistencia y sale 0; en clase C los módulos viven en `/overlay` y un refrito de firmware los borra solos, sin restos. El hueco del consentimiento visible lo cierro con la página de transparencia de P1 (Concesiones).

## 5. Concesiones

- `[concesión]` De P1 §4: umbral explícito `--commit` tras 5 min sanos para fijar la versión de un plugin, en lugar de mi regla «dos crashes → rollback». Determinista y a coste cero.
- `[concesión]` De P1 §2: `debug.SetMemoryLimit` con auto-kill por heap y revival por watchdog en cada plugin Go; inmunidad ante mi riesgo de GC sin pedir root.
- `[concesión]` De P1 §8: página pública `/t/{device_token}`; mi §8 solo tenía API de retiro y un dueño sin curl necesita una URL.
- `[concesión]` De P3 §4 (`SwapBinary`): swap del plugin `botproxy` en puerto adyacente con healthcheck y drain; convierte mi interrupción < 2 s en ~0 para sesiones largas.
- `[concesión]` De P3 §7: particionar los rollups de uso por día en lugar de mi `consumption_1m` mensual; la conciliación de 90 días (P2 §12, ±1 %) consulta rangos cortos y los índices se mantienen chicos.

## 6. Criterio de abandono

Dos disparos, medidos antes de gastar un euro de flota:

1. **F1 sobre hierro real:** en tres routers MIPS de 32 MB y 8 MB de flash, si el PSS del bundle mínimo (`botnetd` + `botproxy`, sin túnel ni telemetría) medido con `smaps` pasa de 6,0 MB en al menos dos, o `df` muestra que los 3,1 MB no caben en el overlay escribible en dos de tres, P2 perdió su razón de ser — que cuatro procesos quepan donde el monolito no — y se descarta a favor de **P1**, conservando los módulos como frontera interna por build tags: un binario, cuatro paquetes.
2. **F2 con la malla:** en dispositivos sin `tun` de kernel, si el túnel userspace sostenido pasa del 8 % de un MIPS de 400 (`top`, media de 24 h) o el `ctl`-sobre-mux no entrega el retiro forzado en < 10 s end-to-end, la apuesta del túnel obligatorio falló y se migra también a **P1**, que elimina la pregunta.

Si en cambio se probara que la aceptación gateway→nodo de **P3** gana mi mux en p95 de sesión por más de un 30 %, no se abandona P2: se injerta. El abandono exige que el multiproceso no quepa o que el túnel no pague: `smaps`, `df` y `top` deciden en F1–F2, no una opinión en W4.
