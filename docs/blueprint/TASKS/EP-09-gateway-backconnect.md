---
artifact: tasks
epic: EP-09
created: 2026-11-14
---

### T-0901 — HELLO de capacidades hacia Redis
- **Ficheros:** `cmd/botnet-gw/hello.go`, `pkg/proto/hello.go`, `internal/agent/hello.go`
- **Depende de:** T-0401, T-0801
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** injerto 6
- **Criterios de aceptación:**
  1. Tras el primer HELLO: `redis-cli hgetall node:$ID:caps` devuelve exactly `{bw_mbps, iso2, asn, device_class}` (4 campos) con TTL 24 h; `smembers gw:pool:$GW` contiene al nodo.
  2. El HELLO viaja por el túnel existente (capture: 0 conexiones TCP nuevas para la capacidades — 1 sola conexión por tramo se mantiene).
  3. Nodo sin HELLO en 24 h: `caps` expira y el gateway lo saca del pool (`SCARD gw:pool:$GW` decrece; assert con reloj acelerado).
- **DoD:** las capacidades del nodo son datos del gateway, publicados por el propio nodo: la selección tiene de dónde elegir (única cura del S1 A2-01).
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw/ ./internal/agent/ -run 'Hello|Caps|Sweep|Ingest'` → ok botnet-gw 1.49s (HGETALL 4 campos + TTL 24h + SMEMBERS pool + sweep expira con TTL 1s acelerado), ok internal/agent 15.6s (FrameHello por tunel existente, Dials=1)

### T-0902 — Casado rdv→gateway por localidad
- **Ficheros:** `cmd/botnet-rdv/geo.go`, `cmd/botnet-rdv/geo_test.go`
- **Depende de:** T-0402, T-0901
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A2-01
- **Criterios de aceptación:**
  1. fixture de 1.000 nodos y 4 gateways: el 100 % de los nodos casa con el gateway de su país; cuando país+ciudad coinciden, ciudad manda (aserción del test de asignación).
  2. Con `iso2` del HELLO vs país del gateway: 0 asignaciones transfronterizas en la simulación (Madrid↔Lisboa mal casado = fallo).
  3. Matar el gateway asignado: el rdv re-asigna al nodo con el siguiente gateway más cercano del mismo país en < 5 min (sin reentregar el binario — herencia de A1-10).
- **DoD:** pool por gateway con localidad declarada: el nodo vive donde está su pool, y el P99 no cruza shards.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-rdv/` → ok 3.25s; 1.000 nodos/4 gateways: 0 transfronterizas, city-hits=493 (ciudad manda), failover Madrid→Valencia mismo país, presupuesto reasignación 16 s < 5 min sin reentrega

### T-0903 — Selección local de pool con 307 de redirección
- **Ficheros:** `cmd/botnet-gw/select.go`, `cmd/botnet-gw/index_geo.go`
- **Depende de:** T-0901, T-0805
- **Estimación:** 12 h
- **Riesgo:** high
- **Cierra:** A2-01
- **Criterios de aceptación:**
  1. `make gw-load` (1.000 nodos, 4 gateways): P99 de selección < 1.500 ms (informe JSON `p99_ms`) y redirects `307` < 5 % de las selecciones.
  2. Selección sin pool coincidente → `307` con `Location` al gateway cuyo pool coincide (índice geo→gateways); el reenvío es de control: 0 bytes de sesión copiados entre gateways (assert de contadores de egress).
  3. La selección lee solo `gw:pool:{gw_id}` local: con Redis monitor de test, 0 queries a pools de otros gateways durante 1.000 selecciones.
- **DoD:** el S1 A2-01 muere: el endpoint único no se fragmenta a 100k porque seleccionar nunca es un viaje inter-gateway, salvo un RTT de control excepcional.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw/ -run 'GwLoad|SelectReads|Redirect'` → ok; informe `docs/verification/gw_load_report.json` p99_ms=0.52 (<1.500), redirects 4,67 % (<5 %), egress del gw redirigente sin un byte copiado; monitor de pools: cada gw tocó solo su `gw:pool:` (250 queries × 4 = 1.000 selecciones, 0 a pools ajenos)

### T-0904 — Sticky con TTL y rotativo ponderado
- **Ficheros:** `cmd/botnet-gw/sticky.go`, `scripts/gw/stickiness.sh`
- **Depende de:** T-0903
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A2-08
- **Criterios de aceptación:**
  1. `scripts/gw/stickiness.sh --mode=sticky --n=50 --ttl=300` → mismo `node_id` en ≥ 95 % de los 50 requests (clave `sticky:{t}:{client}:{key}` con TTL de `api_keys.sticky_ttl_s`).
  2. `--mode=rotating --n=50` → ≥ 80 % de `node_id` distintos y el reparto pondera `bw_mbps` (nodo de 5 Mbps recibe ≥ 2× las sesiones de uno de 2 Mbps en la simulación).
  3. TTL corto en pool chico: pool con < 50 nodos → sticky_ttl efectivo ≤ 120 s (política asertada) para no fijar nodos.
- **DoD:** sticky y rotativo son modos contractuales con umbrales medidos, no promesas de pizarra.
- **Estado:** DONE | PRUEBA: `scripts/gw/stickiness.sh --mode=sticky --n=50 --ttl=300` → ok (100 % mismo node_id ≥95 %); `--mode=rotating --n=50` → ok (SWRR: 50/50 distintos ≥80 %); ponderación bw medido 5 Mbps=100 vs 2 Mbps=40 (ratio 2,50 ≥2×); pool 49 nodos → sticky_ttl efectivo topado a 120 s (aserción de binding real)

### T-0905 — Streams dinámicos por clase en el gateway
- **Ficheros:** `cmd/botnet-gw/admission.go`, `cmd/botnet-gw/admission_test.go`
- **Depende de:** T-0901, T-0606
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A2-08
- **Criterios de aceptación:**
  1. Tope técnico por clase desde `device_class`: clase A 150, clase B 100, clase C 64 streams nominales (tabla versionada; fixture: nodo clase C rechaza el stream 65).
  2. Prueba de equidad: 3 tenants de 300 sesiones sobre 1.000 nodos no dejan al 4.º sin admisión durante 10 min (métrica `pool_starved=0`); el tope de 8 fijo de P2 no se repite como dogma.
  3. La cuota concurrente del tenant (`quotas.concurrent_limit`) y los ≤ 8 tenants siguen mandando por encima del tope de clase (el mín manda; assert combinado).
- **DoD:** el negocio multi-tenant ya no topar a 8 sesiones por nodo: el tope es por clase, medido y gobernado.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw/ -run 'ClassCaps|Fairness|TenantQuota'` → ok; tabla versionada A150/B100/C64 (clase C rechaza el stream 65 con `class_cap`); 3 tenants×300 sesiones sobre 1.000 nodos → pool_starved=0 en 10 min; cuota 5 en clase A cae por `tenant_quota` en el 6.º y el 9.º tenant por `tenant_fanout` (el mín manda)

### T-0906 — Conexiones por pérdida medida y head-of-line
- **Ficheros:** `cmd/botnet-gw/loss_policy.go`, `scripts/soak/loss_soak.sh`
- **Depende de:** T-0405
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A2-02
- **Criterios de aceptación:**
  1. Soak con `netem loss 1.5%` y sesiones de 3 tenants: p95 de throughput por sesión independiente entre sesiones (ratio max/min p95 ≤ 1,2 — aserción del informe del soak, condición R5).
  2. El gateway empuja `loss_estimate` al nodo (via heartbeat) y el nodo abre la segunda conexión solo si > 1 % medido (T-0405 manda; assert de que en reposo sano hay 1 conexión).
  3. Si la aserción 1 falla en el soak: el producto se declara «mux compartido» en `docs/runbooks/pricing.md` y el repricing del GB queda registrado (criterio del veredicto, no excusa).
- **DoD:** el head-of-line con 1-2 % de pérdida está medido por sesión; o hay aislamiento o hay precio honesto.
- **Estado:** DONE | PRUEBA: `scripts/soak/loss_soak.sh` (netem 1,5 %) → ok; `docs/verification/loss_soak_report.json`: ratio p95 max/min 1,025 ≤ 1,2 (veredicto «aislado», fallback «mux compartido»+repricing cableado en el script); política: 0,4 % → 1 conexión, 1,5 % → 2 (umbral >1 % T-0405), empuje `LossPush` en el carril del heartbeat (cada 3.er frame)

### T-0907 — Deny-list de destino en el CONNECT
- **Ficheros:** `cmd/botnet-gw/denylist.go`, `cmd/botnet-gw/denylist_test.go`
- **Depende de:** T-0805
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A3-X2
- **Criterios de aceptación:**
  1. `CONNECT 192.168.1.1:80` → rechazado `host_unreachable` (192.168/16); `10/8`, `172.16/12` y `100.64/10` igual (tabla de 4 rangos, 8 asserts de fixture).
  2. `CONNECT 8.8.8.8:53` y `CONNECT 1.1.1.1:443` → admitidos; 0 bytes de la LAN del dueño cruzan el túnel (contador de egress del nodo con solo intentos LAN = 0 bytes, rechazo en el gateway).
  3. Port-scan simulado (1.000 CONNECTs a LAN en 1 min) → el panel del router no recibe ni un SYN (tcpdump en la LAN del fixture: 0 paquetes) y el tenant recibe `429` de rate-limit por sesión.
- **DoD:** el cliente no puede martillear la casa del dueño: la deny-list RFC1918/CGNAT vive en el gateway, no en el nodo.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw/ -run 'DenyList|PublicDest|Lanscape'` → ok; 8 asserts de fixture (192.168/16, 10/8, 172.16/12, 100.64/10 → `host_unreachable`; 172.32 no topado); 8.8.8.8:53 y 1.1.1.1:443 admitidos con contador de egress LAN=0 B; port-scan 1.000 CONNECTs/1 min → 0 SYN (egress 0 B), budget 20 → 980×429 y ventana renovada con reloj acelerado

### T-0908 — Drenaje de gateway y censo de egress
- **Ficheros:** `cmd/botnet-gw/drain.go`, `deploy/gw/egress_census.md`
- **Depende de:** T-0902, T-0409
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A2-06
- **Criterios de aceptación:**
  1. `redis-cli SET gw:drain:$GW 1` → 0 admisiones nuevas, GOAWAY progresivo, y `psql -tAc "SELECT count(*) FROM sessions WHERE ended_at IS NULL AND gw_id='$GW'"` = 0 pasados 60 s (huérfanos: ninguno).
  2. El rdv re-asigna el 100 % de los nodos drenados a otros gateways en < 5 min (fixture 250 nodos; assert de `gw:pool` destino).
  3. `deploy/gw/egress_census.md` registra por provedor el egress medido por gateway: ≥ 75 Mbps sostenidos con 250 nodos (criterio de dimensionado del edge, herencia A2-06/A2-16).
- **DoD:** apagar un gateway un martes a las 23:40 es un drenaje de 60 s; el edge está dimensionado con números de provedor, no de fe.
- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw/ -run 'Drain|Reassign|EgressCensus'` → ok; `SET gw:drain:gw-es-mad 1` → `AdmitNew` da `ErrAdmissionDrained` (0 admisiones), GOAWAY progresivo 250 sesiones y huerfanos=0 dentro del presupuesto de 60 s; re-asignación rdv del 100 % de los 250 nodos a `gw:pool:gw-es-vlc` (0 sin destino); censo en `deploy/gw/egress_census.md`: 4 provedores a 250 nodos dan 77,5–90,0 Mbps ≥ 75 (aserción `TestEgressCensusThreshold`)
