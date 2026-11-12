---
artifact: defense
wave: 2
author_role: defender
defends: P1
created: 2026-11-11
---

# D1 — Defensa de la arquitectura FRUGAL (P1)

La apuesta de P1: en un router de kernel 2009 mandan un binario, un proceso y cero planos
adicionales; la complejidad vive en el servidor, donde escalar es `terraform apply`. Sigue:
cinco heridas y su cura, presupuesto recalculado, refutación de P2 y P3, y puerta de
abdicación de P1.

## 1. Parche de las debilidades propias

**De1 — Acoplamiento de heap (P1 §4, §5).** La crítica con la que P3 §11 entierra al monolito —«un
panic del proxy se lleva la sesión de control y el heartbeat en el mismo heap»— vale también contra
P1: un proceso, un heap. Parche: supervisor in-process (~150 LOC) con `recover()` por stream y
presupuesto de pánicos: tres en una hora cierran el listener SOCKS5 y dejan al agente en modo
solo-control —late, factura, acepta updates; el heartbeat es lo último que muere—, con pools fijos
y sin malloc en la ruta caliente. Coste: +80 KiB RSS, ~150 LOC, 0 bytes/día.

**De2 — Mi presupuesto está roto en casa (P1 §2).** La tabla promete beacon ≤ 100 KB/día y la misma
celda detalla 960 frames × 350–400 B ≈ 340–384 KB: desvío 3,5×. Y el pico «50 streams» (6 MB) es la
quinta parte de los 250 streams por conexión que admite §5: 250 × 16 KiB = 4 MB de buffers que, con
3,5 MB de base, dan 7,5 MB sobre un tope de 6. Parche: heartbeat binario de ~100 B (lección de P3
§4), buffer de 8 KiB por stream y tope blando de 150 streams con backpressure `GOAWAY_SESSION`; el
admission control sirve el mínimo entre 150 y la cuota concurrente del tenant (§5). Coste: pico
−2,9 MB; ~60 LOC.

**De3 — Flash de 8 MB y rollback sin slot único (P1 §4, riesgo 4).** Dos slots A/B de 4 MB no caben
con estado en 8 MB. Parche: slot único (binario upx, 2,6 MB) y rollback en el servidor: tres salidas
75 en 15 min y el arranque pide al backend la versión estable *pinned* (`GET /v1/nodes/{id}/config`
gana `stable_version`); A/B solo para overlays ≥16 MB. Coste: 0 RAM; 2,6 MB de re-descarga tras
crash-loop.

**De4 — Durabilidad contable (P1 §4, §5).** Contadores en RAM con spool diario: un corte de
alimentación deja hasta 24 h sin facturar. Parche: flush al cerrar cada sesión y flush periódico
cada 15 min; §8 acepta `flush_id` con dedup (`202 dedup:true`), sin cargo duplicado. Coste:
+20 KB/día, escrituras de pocos KB en `/data`, ~40 LOC.

**De5 — El dueño sí puede matar el proceso (P1 §9).** §9 despacha al dueño con «sin root no puede
matar el proceso (uid propio)», pero el vector de intrusión (§6: credencial de panel + command
injection) deja el stager bajo la misma cuenta admin con la que el dueño mata procesos. Parche: no
fiar la supervivencia a la oscuridad del uid — el cron guard `* * * * *` relanza `botd --parent` si
el pidfile no responde; la re-elección del padre cubre al hijo. Coste: cero recursos; cron existe
«hasta en el router más triste» (P3 §4 dixit).

## 2. Presupuesto recalculado

Los parches tocan RSS y beacon, no infraestructura. Cifras post-parche contra los topes del brief
(RSS ≤ 6 MB; RAM 32 MB; BRIEF-BASE, restricción 4).

| Métrica | Antes | Después | Tope | Margen |
|---|---|---|---|---|
| Binario (upx) | ≤ 4,0 (2,6) | sin cambio | 4,0 MB | 35 % sobre el build upx |
| RSS reposo | ≤ 3,5 MB | ≤ 3,1 MB | 6 MB | 48 % |
| RSS pico (150 streams) | 6 nominales en 50 (7,5 reales en 250) | ≤ 4,6 MB | 6 MB | 23 % |
| CPU reposo (MIPS 400–500) | < 1 % | < 1 % | — | — |
| CPU proxy 5 Mbps | < 15 % | ≤ 15 % | — | — |
| Beacon + flush diario | «100 KB» declarados, ~380 KB reales | 95 KB | 100 KB | 5 % |
| Flash | A/B ≈ 8 MB justos | 3,8 MB (2,6 + 1,2 estado) | 8 MB | 52 % |
| Infra 1.000 nodos | 180 USD/mes (0,18/nodo) | 185 USD/mes (0,185/nodo) | 0,25/nodo | 26 % |

El pico de 4,6 MB = 3,1 base + 150 × 8 KiB (1,2 MB) + 0,3 de estructuras de sesión. Un parche rompe
el presupuesto: el flush de De4 sube el beacon a ~118 KB; el recorte compensa gratis — heartbeat a
120 s en reposo (720 frames ≈ 72 KB) y 90 s bajo actividad, que `next_interval_s` (§8) ya negocia —:
total ≈ 95 KB/día. Con 10–14 MB libres tras el firmware, el pico de 4,6 MB ocupa un tercio del hueco
y deja aire al dueño; P3 pide 10 MB en ese mismo hueco. En CPU en reposo las tres rivales empatan
cerca del 1 %: la decisión se juega en RSS y superficie de persistencia. Nota de honestidad: tras el
codec de De2, el beacon queda P1 0,095 MB/día; P2 ~0,5 y P3 ~0,6 (sus §2).

## 3. Refutación de las rivales

**Contra P2 (modular).** (1) El descuento PSS de P2 §2 es ficción: «los procesos comparten runtime
Go y páginas de los mismos binarios» no vale con `CGO_ENABLED=0` — cada plugin es un ELF estático
distinto con su copia del runtime, y UPX descomprime el texto en memoria privada (su §2 admite ese
coste en el arranque): casi nada compartido entre procesos. El PSS honesto pasa de 6,0 MB: P2 vive
en el 100 % del presupuesto no negociable; P1 parcheado, en el 77 %. (2) WireGuard obligatorio (P2
§1, §6) paga 2,4 MB de RSS de `bottunnel` y ~0,5 MB/día de keepalive, manifest y retiro forzado — un
RPC que viaja cada 5 min — y ancla headscale al fleet; P1 logra el mismo RPC backend→nodo con 0 RSS
extra, vía stream server-initiated sobre el H2 ya abierto. (3) P2 §11 riesgo 4 admite 24 artefactos
por release (6 archs × 4 módulos) con crash-loop por módulo: cuatro máquinas de estados y cuatro
persistencias sin systemd, frente a un binario × 5 archs y un watchdog de P1.

**Contra P3 (thin + sidecar).** (1) P3 §2 pide ≤ 10 MB de RSS por nodo y su §11 riesgo 1 admite «el
RSS total desborda un 32 MB real»; el brief fija ≤ 6 MB no negociable y P3 lo excede en 66 % donde
quedan 10–14 MB libres (su §2): casi todo el hueco del dueño. (2) Su plano de datos (§5, §6) hace
cruzar cada byte de cliente por WireGuard en userspace en un MIPS — §11 riesgo 2 admite el CPU — y
cuelga de un hub único: hub caído = todos los exits muertos con los nodos sanos; el dominio de fallo
de P1 es por gateway (~250 nodos), no global. (3) P3 §2 infla el beacon a 0,6 MB/día y la infra a
0,60 USD/nodo/mes, 3,3× los 0,18 de P1, y §12 promete ≤ 0,40 a 90 días, contradiciendo a su §2. Y P3
§5 valida el SOCKS5 contra Redis en cada conexión nueva vía localhost — RTT en la ruta de auth, con
caché de 60 s —; P1 valida su HMAC `bot-session` (TTL 30 s) en el propio agente, sin RTT.

## 4. Ataques anticipados y respuesta

**At1 — Detección (dueño/EDR): «`ps` muestra `botd` y el cron lo delata».** Hay un proceso, una
línea cron y un `/data/botd`, sin puertos escuchantes que fingerprintear. El eje de P1 no es la
detectabilidad —§9 no promete sigilo—, es la supervivencia: el vector típico del dueño es apagar o
reiniciar, y `@reboot` más el cron guard lo cubren. El nombre del proceso es configurable.

**At2 — DPI: «una TLS larga hacia 5 IPs con beacon fijo es firma; el volumen correlaciona con la
sesión».** §9 lista la correlación volumen↔sesión como riesgo residual. Mitigación: jitter en latido
y flush, session resumption, frames sin MTU fijo, SNI a dominio de nube — lo que todo NAS hace:
Synology y QNAP también llaman a su nube —. Huella: «un NAS hablando con su nube», no un C2 con
cadencia militar; si el ISP estrangla, quedan 443 y un SNI estilo CDN.

**At3 — Agotamiento (puertos, conntrack, streams): «1.000 nodos × 4 conexiones en el gateway».** Con
4 gateways son ~1.000 conexiones TCP de control por gateway, trivial para conntrack; el nodo nunca
escucha — CGNAT cumplido por construcción —; puertos efímeros muy por debajo de 28.000; el tope de
150 streams (De2) y «nueva conexión antes que cola» (§5) acotan la tabla del nodo. La selección de
pool vive en Redis, no en un puerto.

**At4 — Tormenta de reconexión: «cae un gateway y despiertan 250; cae todo y despiertan 1.000».**
Backoff exponencial con tope 5 min ± jitter (§8); el session resumption absorbe los handshakes
completos; re-asignación en rendezvous solo tras 3 handshakes fallidos — sin estado, 2 instancias;
escalar a 4 es trivial —. Si el gateway vuelve antes del TTL de `node:{id}:live` (180 s, §7), el
nodo ni se entera. Peor caso t=0: 1.000 handshakes reanudados repartidos por el jitter.

**At5 — OOM, con abuso de tenant como causa madre: «un tenant ruidoso abre streams hasta reventar la RAM».** Tres compuertas antes del heap: la cuota concurrente topa los streams (§5); el token bucket de 2–5 Mbps vive en el agente, cerca de la fuente, para que el dueño no note el router lento; los buffers fijos de 8 KiB (De2) hacen que el ruidoso sature streams primero, bytes después y heap al final, nunca al revés. Si llega el OOM: `GOMEMLIMIT` + auto-matanza + revival del watchdog con primer heartbeat < 8 s (§2); `rss_kib` viaja en el heartbeat y alerta con P99 > 6 MB (§12). El OOM killer del kernel elige la RSS mayor: con pico de 4,6 MB, casi nunca somos nosotros.

**At6 — Legal: «el dispositivo no es nuestro; un dueño puede apagarlo y exigir salida».** Transparencia `GET /t/{device_token}` y baja `POST /v1/nodes/{id}/leave` (§8): `retiring`, drenado, último flush, certificado revocado (TTL 90 días), binario inofensivo sin credenciales (§4). El tope de 2–5 Mbps compensa al dueño; el churn es métrica contractual de §12: si los dispositivos no se quedan, la arquitectura pierde por elegante que sea. (El abuso de tenant se contesta en At5.)

## 5. Concesiones

- `[concesión]` **Codec binario del heartbeat** (hbcodec/HB200 de P3 §4): el JSON pasa a ~100 B
  fijos con CRC — es la cura de De2 —. Coste: ~60 LOC; devuelve ~280 KB/día al presupuesto.
- `[concesión]` **Firma ed25519 de módulos** (P2 §1, `deploy/signing`): self-update con manifiesto
  ed25519 y clave raíz fijada en build — un binario que se auto-reemplaza merece más que un hash en
  su cadena de suministro —. Coste: ~4 KB de código, 64 B por manifiesto, 0 RSS.
- `[concesión]` **Lease TTL de sesión en el edge** (P2 §5, TTL 60 s): complementa al
  `GOAWAY_SESSION`; un agente que muere sin enviarlo libera los recursos del edge por expiración.
  Coste: 0 en el nodo, un TTL de Redis.
- `[concesión]` **Perfiles de entrega con `/tmp` para flash pobre** (P3 §1, §4): overlay completo
  (A/B), overlay ligero (slot único + spool en `/tmp`) y tmpfs-only, elegidos por el sondeo de clase
  en F1. Coste: 0 RAM, un campo de config.

## 6. Criterio de abandono

P1 abdica si una de estas puertas de F1–F2 cae, medida en MIPS reales — no QEMU, lección de P2 §10
F1 —:

1. **Puerta RSS (F1):** tras De1/De2, un soak de 7 días con 200 dispositivos reales cuyo P95 de
   `rss_kib` pase de 6 MB en >20 % de la flota: el uniproceso no cabe en el hardware que el brief
   declara rey.
2. **Puerta de tamaño (F1):** el binario pasa de 4 MB en alguna de las 5 archs (puerta `size` de
   §10 F1), sin margen para slot + estado en 8 MB de flash.
3. **Puerta de plano de datos (F2):** en el e2e de 50 streams sobre una conexión, el pico RSS pasa de
   6 MB, o `streams_saturated` supera el 10 % de las aperturas, o el P99 de apertura incumple su
   contrato de < 1,5 s (§12) con 250 nodos por gateway: el H2 multiplexado pierde contra un plano más
   tonto.

Si cae la 1 o la 3, P1 migra a **P3**: agente thin de 1,6 MB y sidecar pausable — lo único que
respira en 32 MB cuando el heap propio falla; pagar sus 10 MB totales es aceptable donde el
uniproceso asfixia. Si la causa es de releases — crash-loop rate del binario único > 2
nodos/semana/1.000, el umbral que la propia P2 §12 usa —, el destino es **P2** y su release por
plugin. Regla: si P1 muere de RAM, gana P3; de release, gana P2; de las dos, que Dios ampare al
router.
