---
artifact: D3-defense
wave: 2
author_role: defender
defends: P3
created: 2026-11-10
---

# D3 — Defensa de P3: agente THIN + sidecar `botnet-proxy` sobre malla WireGuard

Tesis defendida: el plano de control lo sostiene un agente delgado (WireGuard, heartbeat binario
`HB200` de 200 B y supervisor del sidecar) y el plano de datos lo mueve un proceso aparte,
`botnet-proxy`, relanzable, pausable e intercambiable sin tocar la sesión de control. Esta defensa
no alaba el diseño: cada sección aprieta un tornillo y ningún parche cambia la tesis.

## 1. Parche de las debilidades propias

**D1 — El RSS total roza el límite del brief.** El BRIEF-BASE fija «RSS del agente ≤ 6 MB» y P3 §2
presenta un total ≤ 10 MB (agente 4–5 + sidecar 6) sobre un router que deja 10–14 MB libres:
vivimos al filo y la cifra era declarativa. *Parche:* reclamar la lectura correcta del brief —su
tabla separa «Implante» y «Proxy», luego el tope de 6 MB es del agente, que queda en ≤ 4,5 MB
medidos con PSS en F1— y forzar enforcing en vez de promesa: admisión de 64 conexiones concurrentes
con buffers fijos de 32 KiB por dirección (2 MB de buffers) y `GOMEMLIMIT=6MiB` en el sidecar; la
conexión 65 recibe `CONNECT` reintentable. *Coste:* recorta el pico de concurrencia, no el
presupuesto de reposo; ~0,1 MB de tablas de admisión.

**D2 — `ulimit -v 8192` es hostil a Go en 32 bits.** El runtime Go reserva espacio de direcciones
muy por encima de su heap; en mipsbe/mipsle un tope de 8 MB de VSZ puede reventar el arranque del
sidecar antes de abrir socket, y falla en el campo, no en la CI. *Parche:* `GOMEMLIMIT` + pools de
buffers fijos + auto-kill por heap superado con relanzamiento del agente —mi watchdog mutuo es el
mecanismo de ejecución, no el kernel—; `ulimit -v` solo donde los arenas de Go caben (x86-64,
arm64). *Coste:* cero bytes; ~80 líneas de guardia; GC algo más caro en MIPS, medible en F1.

**D3 — El cliente NATS del agente contradice la tesis THIN.** En §5 el agente se suscribe a NATS
sobre WG para el corte de cuota: TCP larga, librería, reconexión y keepalives dentro del proceso de
4 MB —un cuarto residente no declarado— y el eslabón que cae cuando el hub flappea, justo cuando el
corte tiene que funcionar. *Parche:* suprimir el cliente NATS del dispositivo; el corte viaja en la
respuesta del heartbeat (`quota_state`, ≤ 64 B, ya existe) y NATS queda solo en el servidor.
*Coste:* −0,5 a −1 MB de RSS del agente; latencia de corte peor caso de ≤ 90 s a ≤ 150 s (un
heartbeat perdido) y la métrica de F3 se ajusta a esa promesa.

**D4 — wireguard-go userspace ES el plano de datos, y el MIPS más pobre es el que recluta.** P2
acierta en su §2: «la cripto WG en userspace es el mayor coste». Sin módulo de kernel, cada Mbps
del cliente paga CPU de userspace, y la fragmentación UDP con MTU raros suma peaje. *Parche:*
perfil de ancho por `device_class`: 5 Mbps con módulo de kernel, 2 Mbps en userspace con MTU
fijado en el hub; F0 decide con iperf3 por arch y siembra `bandwidth_cap_kbps` desde esa medición.
*Coste:* hasta −60 % de ancho vendible en la clase débil —impacto de ingresos, no de recursos—;
cero bytes; `pool:{country}:{asn}` compensa vendiendo menos sesiones por nodo.

**D5 — El OOM killer es el ejecutor real y P3 solo lo admite en §9.** Un pico del sidecar en 32 MB
puede tumbar el router entero. *Parche:* guardián de presión de memoria en el bucle de 60 s del
agente: lee `MemAvailable` de `/proc/meminfo`; bajo 4 MB envía `SIGSTOP` al sidecar; bajo 2 MB lo
mata y marca el flag `paused` en el HB200; la recuperación libera ~6 MB sin tocar WG ni heartbeat.
*Coste:* una lectura de procfs por minuto; dos bits de `flags` —el padding de HB200 gana un
`codec_ver` de 4 bits sin crecer un byte—.

## 2. Presupuesto recalculado

| Concepto | Antes del parche | Después del parche | Límite y margen |
|---|---|---|---|
| RSS agente (WG incluido) | 4–5 MB | ≤ 4,5 MB (fuera NATS, +guardián ~0,05) | brief 6 MB: margen 1,5 MB |
| RSS sidecar | 6 MB declarados | ≤ 6 MB forzados (GOMEMLIMIT + 64 con × 32 KiB) | 6 MB: margen 0 |
| RSS total residente | ≤ 10 MB | ≤ 9,5 MB | 32 MB dejan 10–14 libres: margen 0,5–4,5 MB |
| Flash (upx) | 4,0 MB (1,6 + 2,4) | 4,0 MB | 8 MB de flash: margen 4,0 MB |
| CPU en reposo | ~1 % de 300 MIPS | ~1 % | sin tope duro; medible en F0 |
| Bytes/día de control | ≤ 0,6 MB | ≤ 0,58 MB (cae la TCP larga de NATS) | métrica día 30: 0,6 MB |
| Infra por 1.000 nodos | ~600 USD/mes | ~600 USD/mes (0,58/nodo) | métrica día 90: ≤ 0,40 |

Ningún parche rompe el presupuesto: D1 baja 0,5 MB el techo total, D3 devuelve medio mega al
agente, D2 y D5 no añaden bytes. El recorte es de capacidad, no de RAM: el tope de 64 conexiones y
los 2 Mbps userspace afectan al pico, y lo compensa un `pool/select` sin estado que mueve la sesión
a otro nodo. Sobre los límites del brief: el agente solo cabe en 6 MB con 1,5 MB de margen, y el
total de 9,5 MB cabe en los 10–14 MB que el firmware deja libres en 32 MB. La promesa de 0,40
USD/nodo a día 90 queda exigente tras D4: la compensa recortar de 8 a 7 gateways spot si F0
demuestra un mix de clases con menos sesiones vendidas por nodo.

## 3. Refutación de las rivales

**Frente a P1 (monolito frugal sin WireGuard).** (1) *Su presupuesto vive en un solo heap.* P1 §2
fija «RSS pico (50 streams) ≤ 6 MB»: agente, proxy y cincuenta buffers comparten montón, y su §11
riesgo 2 admite GC voraz con «auto-matanza por heap + revival por watchdog». Cuando dispara, el
plano de control muere con el de datos —heartbeat y sesiones se van juntos—, y su watchdog es el
mismo binario reejecutado (§4): un fallo sistémico de framing H2 tumba a padre e hijo a la vez. En
P3 el mismo pico solo mata el sidecar, y el heartbeat ni se entera. (2) *Su riesgo número uno
traiciona la tabla del brief.* §11 riesgo 1 confiesa que «gost v3 como librería pasa el binario de
4 MB» y su plan B es «SOCKS5 propio (~600 LOC)»; además §6 declara «el túnel es TLS propio sobre
HTTP/2» con framing y reconexión propios, §8 inventa un handshake a medida (`OPEN_SESSION`,
`GOAWAY_SESSION`) y suma un servicio de rendezvous. P1 acumula el mayor volumen de código propio
del sistema para borrar WireGuard; P3 ejecuta el brief y solo se desvía donde puede medirlo.
(3) *Su identidad cuelga de un hash y de una CA.* §9 concede «colisiones de `device_token` (hash,
no identidad cripto)», §8 renueva certificados cada 90 días y §11 riesgo 5 pierde el certificado
con el reset de fábrica. En P3 la clave pública ed25519 ES la identidad de WG (`wg_pubkey` UNIQUE
en `nodes`): una clave, cero CA de flota, cero renovación en aparatos que se reinician solos.

**Frente a P2 (supervisor + plugins firmados).** (1) *Su presupuesto es promesa contable, no
construcción.* §2 reclama «suma de PSS ≤ 6,0 MB» para cuatro procesos descontando páginas
compartidas, pero cuatro binarios Go estáticos son cuatro montones que comparten poco más que
páginas de texto; su §11 riesgo 1 lo admite: «suma de PSS > 6 MB en hardware real — medir en F1».
Si la medición falla, P2 no tiene adónde recular. El presupuesto de P3 es estructural: heap del
agente de tamaño fijo (codec de 200 B, tabla de peers WG) y sidecar topado por runtime; si la
medición falla, mato el sidecar y sigo latiendo. (2) *Paga cripto dos veces para planos que no se
tocan.* §6 confina WG al control y mueve los datos «como TCP+TLS normal», §5 añade mux TLS hacia
el edge y §11 riesgo 1 mantiene «la cripto WG en userspace es el mayor coste» en `bottunnel`: en un
MIPS de 400 P2 paga cripto WG por ~158 KB/día de heartbeats y cripto TLS otra vez por cada byte del
cliente. P3 paga WG una vez (un único peer hub) y mueve control y datos sobre la misma asociación:
un CPU trabajando por Mbps, no dos pilas criptográficas residentes. (3) *Su grano de release cuesta
24 artefactos.* §11 riesgo 4 confiesa «6 archs × 4 módulos = 24 artefactos por release» con
manifest, firmas, reanudación por `Accept-Ranges` y rollback por módulo: maquinaria para una
organización que aún no ha cobrado. Reiniciar solo el plugin roto vale en régimen estacionario, no
en fases 1–3, donde el binomio agente+proxy (12 artefactos) cubre todo cambio posible.

## 4. Ataques anticipados y respuesta

1. **Detección** (beacon de 200 B fijos cada 60 s, binario extraíble): el tamaño fijo es un activo
   anti-DPI —ningún patrón de longitudes que agarrar—, el jitter ±15 % difumina el ritmo y el nodo
   expone una sola dirección UDP saliente, la del hub. Los 200 B no portan secreto —`HB200` no
   tiene campo de credencial—: extraer el beacon no da material de suplantación, el enrollado es
   reto ed25519. No defiendo a propósito lo visible al dueño: proceso y línea de cron.
2. **DPI correlacionando el volumen del hub**: ya lo concedí en §9 —no lo oculto—; lo mitiga la
   estructura: 1.000 nodos comparten 8 gateways con sesiones multiplexadas, así que la correlación
   es por gateway, no por nodo. En el enlace residencial se ven ~0,58 MB/día de control y ráfagas
   de 2–5 Mbps: la firma de un NAS hablando con su nube.
3. **Tormenta de reconexión del hub** (1.000 nodos saludando a la vez): el handshake WG es un
   intercambio de clave barato por peer; la tolerancia de heartbeat de 150 s (`hb:{node_id}` TTL
   150 s) impide vaciar el pool al primer estornudo; backoff con jitter completo hasta 120 s
   reparte llegadas; la simulación de 1.000 nodos es puerta de F0: si no pasa ahí, no se abre F3.
4. **OOM en 32 MB** (el kernel mata lo grande o tumba el router): tras el parche D5 hay tres
   muelles antes del killer —`GOMEMLIMIT` forzando GC, la admisión rechazando la conexión 65, el
   guardián con `SIGSTOP` bajo 4 MB de `MemAvailable`—. Si el killer aparece igualmente, solo
   encuentra el sidecar: el agente lo relanza en ≤ 60 s y el heartbeat no ha dejado hueco > 150 s
   en Redis. La tesis del desacople se paga a sí misma exactamente aquí.
5. **Abuso de tenant** (un scraper martillea un nodo y quema su uplink): un tenant es una sesión con
   `session_id` propio; el limiter topa 2–5 Mbps cerca del dueño; el token socks vale 5 min con
   caché de 60 s —si Redis estornuda no caen las sesiones abiertas— y el backend rechaza tokens de
   otro tenant. Radio de explosión: una sesión en un nodo; `pool/select` desvía el resto, y el
   agotamiento de tokens por rotación intensa queda acotado por el TTL.
6. **Legal / baja del dueño** (el dispositivo no es nuestro y el router no escucha entradas): el
   endpoint de transparencia dispara `POST /v1/nodes/{node_id}/disable`, el backend marca
   `pending_low_level`, y el agente lo lee en la respuesta de su próximo heartbeat —pull sin puerto
   entrante, a prueba de CGNAT—: cierra listeners, manda un último HB200 con flag `bye` y borra
   claves. Convergencia peor caso: un heartbeat ± jitter; el barrido de `last_hb_at` > 24 h recluta
   al zombie. La baja no necesita un router vivo.

## 5. Concesiones

- `[concesión]` De P2: **firma ed25519 de binarios con la clave raíz fijada en el build**, además
  del sha256 —mi §4 solo verifica sha256 y un MITM en la ruta de update puede servir el binario que
  él mismo hashó—. El mecanismo (descargar, verificar firma, rename atómico, reiniciar solo el plugin
  justo) migra entero a mi swap y cuesta 64 B por artefacto.
- `[concesión]` De P2: **rollback por crash-loop** —dos crashes con la versión nueva disparan el
  revert a `.bak`—. Mi §4 guarda `.bak` pero no fija política de retorno; ese criterio cierra el
  círculo del swap sin maquinaria nueva.
- `[concesión]` De P1: **spool contable en disco** («se pierde un heartbeat, no la facturación»,
  P1 §6). Mi `bytes_delta` vive en contadores del sidecar; un crash entre flushes pierde centavos, y
  mi métrica de día 30 exige desvío ≤ 5 % en `usage_events`. Un spool de 4 KB en el overlay con
  flush diario compra esa tolerancia por un write al día.
- No concedo el plano de transporte (WG frente a TLS/H2 propio): es la línea de agua que separa las
  arquitecturas, y la sección 6 le pone número y fase al desafío.

## 6. Criterio de abandono

P3 se descarta a sí misma si, al cierre de F0/F1, en mipsbe y mipsle reales de 32 MB (no QEMU), se
da cualquiera de estas mediciones:

1. **iperf3 sobre wireguard-go userspace no sostiene ≥ 2 Mbps con < 15 % de CPU media** en alguna
   de las dos archs débiles: el plano de datos sobre WG userspace es inviable y el rival al que se
   migra es **P1**, la única que elimina WG del dispositivo (su §6); se hereda su túnel TLS/H2
   conservando el codec HB200 intacto.
2. **El p95 de RSS total medido con `free` en la flota de F1 supera 12 MB, o el p95 de `proxy_rss`
   del heartbeat supera 8 MB pese a GOMEMLIMIT y la admisión de 64 conexiones**: la premisa de tres
   procesos residentes ha muerto en 32 MB, y se migra también a **P1** (un proceso, un heap),
   reservando la malla headscale solo para las clases x86-64/arm64 donde sí cabe.
3. **El criterio de F1 «200 conexiones concurrentes al sidecar» no se sostiene con ≤ 6 MB de RSS**:
   el modelo de concurrencia por nodo se cae antes de F2, y el rival con menos bits residentes —de
   nuevo **P1**— pasa a base del refinado.

Si P3 supera F0/F1, su enemigo temporal es **P2**, pero ese frente se abre en F4/F5: al cumplimiento
de 20 releases, si el ahorro medido de sus módulos firmados supera en ≥ 60 % el coste de mi swap de
dos binarios para el mismo cambio, se injerta la unidad de release de P2 aunque sobreviva mi modelo
de procesos. Dos números deciden lo demás: la medición de RSS de F1 y el iperf3 de F0; si ambos
fallan a la vez, P3 se retira sin arrastrarse tres fases más.
