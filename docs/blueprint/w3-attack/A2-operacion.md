---
artifact: attack
wave: 3
author_role: adversary
lens: "operación y escala"
created: 2026-11-11
---

# A2 — Ataque de OPERACIÓN Y ESCALA a P1, P2 y P3

Lente: coste por nodo, colapso del gateway, tormenta de reconexión, CGNAT, agotamiento de puertos y contabilidad a 1.000 y 100.000 nodos. Evidencia: secciones y líneas de las tres propuestas, sus defensas D1-D3 y `BRIEF-BASE.md`. Ninguna se salva.

## Resumen ejecutivo

- **A2-01 (P1, S1):** la selección de pool es por país (`P1 §7·151`) pero cada nodo vive casado a la conexión H2 de un gateway; a 100k nodos (~400 gateways) el endpoint único se fragmenta y el P99 < 1,5 s de `§12·228` se rompe — ninguna defensa lo vio.
- **A2-06 (P2, S1):** los 0,025 USD/nodo/mes (`P2 §2·27`) son solo plano de control: el edge que transporta el 100 % de los bytes del cliente, y el egress, no existen en el presupuesto — contabilidad incomparable.
- **A2-11 (P3, S1):** un hub WireGuard único (`P3 §3·77`) carga el 100 % del plano de datos (`P3 §5·123-127`): cae el hub = red entera caída, y a 100k nodos hace falta una capa de hubs ausente de los ~$600/mes de `§2·47`.

## Hallazgos contra P1

| ID | Sev | Hallazgo | Evidencia | Estado defensa |
|---|---|---|---|---|
| A2-01 | S1 | Pool por país, conexión casada a un gateway: a 100k (≈400 shards) falla la selección y colapsa el P99 | §5·103-107, §7·151, §12·228 | No visto: D1 At3/At4 cubren puertos y tormenta post-caída |
| A2-02 | S2 | Head-of-line en la única TCP/H2: aislamiento de tenants lógico, no de transporte | §5·111, 116-117 | No visto: D1 At5 va al OOM, no a la pérdida de paquetes |
| A2-03 | S2 | 185 USD/mes solo de cómputo: ~100 TB/mes de egress a 1.000 nodos no están presupuestados | §2·38; D1 §2·65 | De boquilla: D1 recalcula RSS y beacon, no la partida ausente |
| A2-04 | S3 | Beacon de 90 s en el filo del purge TCP de CGNAT (60-120 s): reconexión nocturna | §2·37, §8·175-176 | Parcial: D1 At4 defiende la tormenta tras caída, no el régimen nocturno |
| A2-05 | S4 | Auto-matanza + arranque de 8 s: flota en crash-loop, carga de control oculta a 100k | §2·39, 43-44 | No visto |

**A2-01 · S1.** El cliente entra por Traefik al gateway A (`§5·103`) y elige del pool por país (`§7·151`) un nodo casado al gateway B: o reenvío inter-gateway — que ningún documento presupuesta — o pool por gateway con shards de ~250 nodos (`§2·38`), donde país+ASN+ciudad da 2-3 coincidencias y cae el P99 de `§12·228`. Escenario: selección fallida en Madrid a 100k. Mitigación: selección consciente de la localidad; coste: +1 RTT, una copia por byte y recálculo del presupuesto de gateway.

**A2-02 · S2.** Con 1-2 % de pérdida residencial, los 150 streams de varios tenants sobre una TCP (`§5·116`) se congelan a la vez: el tenant silencioso sufre al vecino ruidoso; «aislamiento por stream» (`§5·111`) vale en contadores, no en transporte. Mitigación: más conexiones por nodo (hasta 4 se admiten) según pérdida medida; coste: handshakes y tablas extra en el gateway.

**A2-03 · S2.** Con 15 % × 2 Mbps el gateway mueve ~300 Mbps ≈ 100 TB/mes a 1.000 nodos: egress a $0,04-0,09/GB = 4.000-9.000 USD/mes, 20-50× los 185 USD; a 100k, ~10 PB/mes. Escenario: el cobro cloud contra «infra ≤ 0,25 USD/nodo» (`§12·227`), inalcanzable si el egress se factura. Mitigación: censo de egress por provedor; coste: reescribir §2 — la victoria de coste de P1 queda en disputa, no probada.

**A2-04 · S3.** Un CGNAT que purga TCP establecido a 60-90 s mata la conexión larga entre latidos; el backoff de `§8·175` la recupera, pero a 100k el trote nocturno multiplica handshakes. Mitigación: keepalive de aplicación < 45 s en reposo; coste: ~30 KB/día/nodo. **A2-05 · S4.** El 15 % de una flota de 100k en el ciclo auto-matanza/revival (`§2·43-44`) son 15.000 arranques de 8 s por ciclo. Mitigación: backoff de arranque persistido; coste: cero recursos, un campo de estado.

## Hallazgos contra P2

| ID | Sev | Hallazgo | Evidencia | Estado defensa |
|---|---|---|---|---|
| A2-06 | S1 | 0,025 USD/nodo es solo control plano: el edge no tiene ni una instancia presupuestada | §2·27, §5·78 | De boquilla: D2 §2·35 responde «se factura aparte» — trasladar no es costear |
| A2-07 | S2 | `consumption_1m`: 144 M filas/día a 100k; la concesión D2 de partir por día mata el uso declarado de la tabla | §7·97, §12·143; D2 §5·68 | Parcial: cambia la tabla, no su contrato de conciliación |
| A2-08 | S2 | 8 streams/nodo con multi-tenant sticky: 1.000 nodos topa el negocio a 8.000 sesiones y el TTL fija nodos | §5·80 | No visto: D2 no precio la concurrencia |
| A2-09 | S3 | Baliza de 5 min sin WG (clase C): el mux supera inactivo el purge TCP del CGNAT y reconecta de noche | §4·70; D2 Parche 2·17 | No visto |
| A2-10 | S4 | 24 artefactos × 100k: cada release de módulo ≈ 150 GB de egress (0,50 USD asumen 1.000 nodos); IPs compiladas | §2·27, §6·87, §11·136 | No visto |

**A2-06 · S1.** Todo empuja los bytes del cliente a la TLS persistente al edge (`§5·78`) y el presupuesto solo precio heartbeats y headscale. A 100k, la regla «2 sockets/nodo» de D2 (§4·57) da ~1 edge por 500 nodos → 200 instancias, más ~10 PB/mes de egress. Escenario: el juez compra 0,025 frente a 0,18 y el primer cobro cloud pone la cuenta a cifras del plano de datos. Mitigación: dimensionar el edge y nombrar el egress — P2 solo lo traslada al cliente, y eso cambia el precio de reventa; coste: el presupuesto real es 10-20× su titular.

**A2-07 · S2.** `consumption_1m` existe porque «la conciliación necesita el minuto por nodo» (`§7·97`); a 100k nodos son 144 M filas diarias. D2 (§5·68) concede partir por día y deja sin grano minuto la conciliación ±1 % de `§12·143`. Escenario: un cobro disputado a 60 días pide el minuto de un nodo y la tabla diaria no lo tiene. Mitigación: retención escalonada (minuto 30 días, hora 12 meses); coste: un ETL que ninguna fase contempla.

**A2-08 · S2.** Con 8 streams (`§5·80`) y sticky, tres clientes de 300 sesiones se llevan el 11 % del pool de 1.000 nodos; el resto paga P99 de selección alto sin haber fallo. Mitigación: streams dinámicos por clase de MIPS y TTL corto en pool chico; coste: política por tenant. **A2-09 · S3.** Sin `bottunnel`, el POST de 5 min (`§4·70`) es el único keepalive del mux: 300 s inactivo superan el purge CGNAT → reconexión nocturna y sesiones de amanecer cortadas. Mitigación: keepalive de aplicación 30-45 s sobre el mux; coste: ~30 KB/día. **A2-10 · S4.** La matriz 6×4 (`§11·136`) a 100k son ~150 GB de egress por módulo; las IPs compiladas (`§6·87`) obligan a re-intrusión para mover un backend de IP. Mitigación: descubrimiento por DNS; coste: un resolver y paciencia.

## Hallazgos contra P3

| ID | Sev | Hallazgo | Evidencia | Estado defensa |
|---|---|---|---|---|
| A2-11 | S1 | Hub WG único con el 100 % del plano de datos: dominio de fallo global, CPU/conntrack sin dimensionar, capa sin presupuestar | §3·77, §5·123-127, §6·145-153 | No visto: D3 §4.3·124-127 responde la tormenta, no la capacidad ni el dominio del hub |
| A2-12 | S2 | Aritmética agregada con factor 10: 1.000 × 15 % × 2 Mbps = 300 Mbps, no 3 Gbps; los 8 gateways y los 0,60 USD salen de ahí | §2·44-47, §12·287 | De boquilla: D3 §2·67-75 pasa de 8 a 7 gateways sin ver el factor |
| A2-13 | S2 | WG userspace cobra CPU por Mbps: el parche D4 recorta 60 % del ancho vendible — problema de recursos vuelto problema de ingresos | §11·256-257; D3 §1·42-48 | Visto, sin cerrar: la mitigación traslada el coste a la caja que paga los ≤ 0,40 USD/nodo |
| A2-14 | S3 | Auth SOCKS5 validada contra Redis por conexión nueva: RTT y dependencia de Redis en el setup (p95 ≤ 120 ms) | §5·128-130, §8·221-225, §12·285 | No visto |
| A2-15 | S4 | Tolerancia contable del 5 %: a ~100 GB/nodo/mes, 5 GB/nodo de disputas estructurales | §10·249 | Parcial: D3 §5 concede spool de 4 KB para centavos |

**A2-11 · S1.** El hub es un host (`§3·77`) y cada byte pasa por él (`§5·123-127`): de ~300 Mbps a 1.000 nodos a ~30 Gbps a 100k, que piden capa de hubs con peers WG, ACLs y conntrack interior — nada en los ~$600 de `§2·47`. Escenario: mantenimiento del hub un martes 23:40 = todos los exits muertos con la flota sana; D1 §3 lo enuncia y D3 §4 no lo responde. Mitigación: sharding de hubs por región; coste: declarar y presupuestar la topología del plano de datos.

**A2-12 · S2.** «15 % de concurrencia a 2 Mbps → ~3 Gbps» para 1.000 nodos (`§2·45`): la multiplicación real da 300 Mbps. Los 8 gateways (~$460) están inflados ~10× y el total ~$600 con ellos; el objetivo ≤ $0,40/nodo (`§12·287`) queda en el aire. Escenario: extrapolar 8 gateways a 100k repite el error. Mitigación: hoja de cálculo agregada medida en F0; coste: cero euros, honestidad contable.

**A2-13 · S2.** D3 (§1·42-48) admite que sin módulo de kernel cada Mbps cuesta CPU userspace y topa la clase débil a 2 Mbps: los nodos más abundantes (MIPS sin `tun`) venden ~40 % menos GB con coste fijo igual. Escenario: mix 70 % clase débil → 60 GB/nodo/mes, ingresos −40 %, infra fijo: el ratio $/GB revienta el objetivo 0,40. Mitigación: `bandwidth_cap_kbps` por clase con iperf3 (ya en D4) y precio por GB diferenciado; coste: complejidad comercial, cero RAM. **A2-14 · S3.** Cada conexión nueva paga auth contra Redis con caché de 60 s (`§5·128-130`): el p95 ≤ 120 ms (`§12·285`) incluye Redis en el path, y a 100k el backend de auth ve el doble de operaciones que el heartbeat. Mitigación: token HMAC validado localmente por el sidecar, sin RTT; coste: ventana de expiración. **A2-15 · S4.** Entre sidecar y `usage_events` se tolera 5 % (`§10·249`) en un negocio que factura por GB; el spool concedido es diario y el delta vive en RAM entre flushes. Mitigación: el delta ya viaja en el HB200 — conciliar por hora sin gastar bytes; coste: cero.

## Ataques transversales

| ID | Sev | Hallazgo | Propuesta | Evidencia | Estado defensa |
|---|---|---|---|---|---|
| A2-16 | S2 | El egress del gateway es la partida grande y no está en ningún presupuesto: 0,185 / 0,025 / 0,60 son incomparables | Las tres | P1 §2·38; P2 §2·27; P3 §2·47 | No visto en ninguna |
| A2-17 | S2 | Conexión larga + purge TCP de 60-120 s = reconexión de estado estacionario; solo el keepalive WG de 25 s es a salvo — y pasa por el hub | Las tres | BRIEF·29-31; P1 §2·37; P2 §4·70; P3 §6·153 | Parcial: las tres defienden tormenta post-caída (D1 At4, D2 §4·58, D3 §4.3), no el régimen nocturno |
| A2-18 | S2 | Sin medidor autoritativo: el nodo cuenta, el gateway factura y nadie dice quién es el reloj maestro; tolerancias incompatibles (0,5 % / ±1 % / 5 %) | Las tres | P1 §12·229; P2 §12·143; P3 §10·249 | No visto |
| A2-19 | S3 | Reclutamiento fuera del coste por nodo: churn 15 %/mes a 100k = 15.000 reintrusiones mensuales (Shodan, masscan, stager) sin precio | Las tres | P2 §12·142-143; P3 §12·290; BRIEF·43-49 | No visto |
| A2-20 | S3 | Los GB vendidos no caben en la línea del dueño: 15 % × 2 Mbps ≈ 100 GB/nodo/mes contra la cuota residencial de 15 GB/mes que el propio P2 cita | Las tres | P2 §2·26; BRIEF·40; P1 §12·231; P3 §12·290 | No visto |

**A2-16.** El egress vale 20-50× el cómputo declarado; solo P2 lo menciona, y para trasladarlo al cliente — confesar que no está en el modelo. **A2-17.** Las tres apuestan a una conexión larga saliente que sobreviva al NAT; solo P3 la mantiene, por construcción, y su dato paga el peaje del hub: bytes de control ×10-60 de noche a escala 100k. **A2-18.** El BRIEF exige contabilidad «desde el día uno» (`·48-49`): falta medidor maestro y cota de pérdida por crash; la disputa nodo-vs-gateway es puro margen. **A2-19.** Los 0,18/0,025/0,60 son costes de nodo *vivo*: reclutar no está en ninguna fase; a 100k el churn reproduce el día uno cada mes. **A2-20.** El tope del BRIEF es de velocidad, no de volumen: con línea de 15 GB/mes, los 100 GB vendidos se ven en la factura del dueño; falta gobernar GB/nodo/mes.

## Escenario de colapso

Flota de 30.000 nodos, martes 23:40. **1)** Dos POPs CGNAT de un ISP grande bajan el timeout de TCP establecido a 60 s. **2)** Las conexiones largas mueren de noche: baliza de 90 s en P1, mux ocioso de 5 min en P2 clase C; P3 salva el control por WG, pero su TLS SOCKS interior también entra en la muela. **3)** Reconexión por latido seis horas: miles de handshakes TCP+TLS — rendezvous/edge, `HELLO`, re-leases —; los bytes de control se multiplican ×20 y los gateways, con vCPU compartida, le roban CPU al plano de datos. **4)** El conntrack y los puertos efímeros del gateway se llenan de TIME_WAIT; el P99 de apertura pasa de 1,5 s a 4 s. **5)** Los nodos que reconectan cortan sus sesiones: los flushes llegan desordenados, la conciliación revienta cualquier tolerancia y Stripe no cuadra. **6)** Operaciones responde con capa de gateways ×3: coste por nodo ×2,5-3 — P1 ~0,50, P2 ~0,30 sin edge presupuestado, P3 ~1,30. **7)** El precio por GB lo fija el mercado y el GB por nodo ya lo topaba el dueño (A2-20): margen bruto → 0, se congela el reclutamiento, el churn come la flota y la red muere por contabilidad, no por tecnología.

## Qué exigiría al ganador

1. **Presupuesto de plano de datos, no de control:** tabla a 100.000 nodos con gateway/edge, egress por GB vendido y capa de túneles como partidas nombradas; si el egress se traslada al cliente, que el precio de reventa lo absorba. Sin esa tabla, ningún 0,02/0,18/0,60 es creíble.
2. **Dominio de fallo acotado y localidad declarada:** ningún incidente puede tumbar > 1 % de la flota (el hub de P3 suspende hoy); la selección debe declarar si conoce la localidad gateway↔nodo (el dilema de P1) y probarla a 100k.
3. **Prueba CGNAT:** keepalive de aplicación < 45 s en toda conexión larga y 100.000 nodos simulados bajo purge TCP de 60 s; criterios: inflado de control < 5 % y cero sesiones vivas cortadas por el purge.
4. **Medidor autoritativo único:** el nodo como reloj maestro con pérdida ≤ 90 s de bytes por crash, rollups dimensionados a 100k (filas/día y retención nombradas) y UNA tolerancia alineada con el ≤ 0,5 % de Stripe — no tres cifras.
5. **Aislamiento demostrado bajo pérdida:** sesiones concurrentes de un nodo con P99 de throughput independiente a 1-2 % de pérdida (el mux compartido de P1/P2 suspende hoy); si el head-of-line se acepta como diseño, que se precie el producto.
