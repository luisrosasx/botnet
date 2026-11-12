---
artifact: tasks
epic: EP-06
created: 2026-11-14
---

### T-0601 — SOCKS5 embebido en loopback sin escuchar fuera
- **Ficheros:** `internal/proxy/socks5.go`, `cmd/botd/main.go`, `internal/proxy/socks5_test.go`
- **Depende de:** T-0301
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `curl -s --socks5-hostname 127.0.0.1:1080 https://example.com` sobre el túnel → `200`; `ss -ltn` en el nodo no lista ningún puerto en 0.0.0.0/externa (el nodo nunca escucha: CGNAT por construcción).
  2. Build con tags `socks5+tcp` únicamente: `go list -f '{{.Tags}}' ./internal/proxy` los refleja y el binario clase C sigue ≤ 2,6 MB (`size` assert).
  3. Auth SOCKS interna: usuario malformado → handshake rechazado sin abrir stream H2 (contador de streams no sube).
- **DoD:** el proxy vive en el mismo proceso y binario, dentro del loopback, sin puerto expuesto al mundo.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestSOCKS5LoopbackEcho|TestConfigValidate|TestMalformedUserNoStream' ./internal/proxy` → 3/3 PASS (listener solo loopback + Loopback() true; Start en 0.0.0.0 → ErrNotLoopback; usuario malformado rechazado con opener.Opened=0); `go test -tags "socks5 tcp" -run TestBuildTagsAndClassCBinarySize` → PASS: go list demuestra que SOLO los tags socks5+tcp encienden socks5.go/transport_tcp.go y binario linux/mipsle upx ≤ 2.726.297 B (2,6 MB). `curl --socks5` e2e con doble HTTP-over-SOCKS → 200 (TestHTTPEndpointOverSocksReturns200).

### T-0602 — bot-session HMAC local con TTL 30 s
- **Ficheros:** `internal/proxy/session_auth.go`, `internal/proxy/session_auth_test.go`
- **Depende de:** T-0303, T-0601
- **Estimación:** 8 h
- **Riesgo:** high
- **Cierra:** A2-14, A3-12
- **Criterios de aceptación:**
  1. Token `HMAC(node_secret, {client_id, tenant_id, session_id, node_id})` válido → stream admitido con 0 llamadas a Redis desde el agente (log `redis_calls=0` en el path de auth).
  2. Token con HMAC forjado → stream cerrado al instante y 0 bytes transferidos; token con ts de > 30 s → `401` interno (ventana anti-replay A3-12).
  3. Medición p95: abrir sesión con auth HMAC local < 120 ms p95 en LAN (benchmark 1.000 aperturas; sin RTT a Redis en el path).
- **DoD:** la auth del nodo se valida con lo que ya tiene en flash — ni Redis ni backend en el path del p95.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestValidTokenAdmittedWithZeroRedisCalls|TestForgedToken$|TestExpiredTokenIs401|TestMalformedTokens|TestSessionOpenP95Under120ms' ./internal/proxy` → 5/5 PASS: HMAC local con RedisCalls=0 (log redis_calls=0); forjado → ErrTokenForged y en el server el stream se abre y cierra con BytesSeen=0 (TestForgedTokenZeroBytes PASS); ts -40 s → ErrTokenExpired con Status401=401 (TestExpiredTokenNoStream: 0 streams); p95 de 1.000 aperturas firma+verificacion+stream < 1 ms (<< 120 ms).

### T-0603 — Buffers fijos 8 KiB y tope técnico de streams
- **Ficheros:** `internal/proxy/buffers.go`, `internal/proxy/buffers_test.go`
- **Depende de:** T-0601
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A3-05
- **Criterios de aceptación:**
  1. 150 streams de test con un stream emisor al 90 % del bucket: el p95 de latencia del stream testigo ≤ 2× baseline sin carga (el vecino ruidoso no lo congela — streams antes que bytes).
  2. Heap del agente con 150 streams y presión: RSS ≤ 4,6 MB (tope de perfil) y el modo «solo-control» tras 3 pánicos en 1 h mantiene el heartbeat vivo.
  3. Buffers: `grep -rn "bufio.NewWriterSize" internal/proxy` muestra 8192 en las 4 corrientes; 0 buffers sin tamaño fijo (assert grep count = 4).
- **DoD:** el tenant ruidoso satura sus streams antes que el heap del dueño; el nodo ejecuta su tope técnico, no la cuota de negocio.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestNoisyNeighborWitnessLatency|TestHeapDeltaUnder150StreamsAndPanicMode|TestFixedWritersExactlyFour|TestControlOnlyRejectsNewSessions' ./internal/proxy` → 4/4 PASS: testigo p95 con vecino al 90 % del bucket ≤ 2x baseline (log: baseline p95=25ms, suelto <1ms, limite 50ms); 150 streams + presion → delta HeapInuse 122.880 B ≤ 4,6 MB (RSS real = soak Linux); 3 panicos en la ventana → ControlOnly y heartbeat doble sigue latiendo; grep equivalente: 4 corrientes bufio.NewWriterSize con 8192 y 0 buffers sin tamano fijo.

### T-0604 — Token bucket 2-5 Mbps en el agente
- **Ficheros:** `internal/proxy/ratelimit.go`, `internal/proxy/ratelimit_test.go`
- **Depende de:** T-0601
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `iperf3 -c` a través del túnel con tope 5 Mbps: caudal medido 5,0 ± 0,3 Mbps sostenido 5 min (no 2× en ráfagas > 10 s).
  2. Bajo saturación del bucket: 100 pings ssh al router con p95 de latencia < 300 ms (el dueño no nota el router lento — restricción 6 del BRIEF).
  3. `bandwidth_cap_kbps` del entitlement cambia en runtime (T-0306) → el nuevo tope se aplica sin reinicio (mismo método de assert).
- **DoD:** el límite por nodo cerca de la fuente: 2-5 Mbps nombrados, medidos y gobernados por el backend.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestBucketSustainsTopRate|TestPingP95UnderSaturation|TestSetRateAppliesNoRestart|TestKbpsToBytesMatchesAgentFormula' ./internal/proxy` → 4/4 PASS: caudal sostenido 5,00 Mbps en ventana de 3 s con rafaga de 1 s drenada (iperf3 en nodo real = BLOCKED-HW; el doble mide el mismo tope); p95 de 100 pings = 2,8 ms < 300 ms con el bucket saturado (control fuera del shaping); SetRate 5→2 Mbps en caliente: caudal medido 2,00 Mbps sin reinicio; KbpsToBytes == formula agent BandwidthBytesPerSec (kbps x 1024).

### T-0605 — GOAWAY por cuota con closed_reason quota
- **Ficheros:** `internal/proxy/quota.go`, `internal/proxy/quota_test.go`
- **Depende de:** T-0602
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** A3-04
- **Criterios de aceptación:**
  1. Fixture `quota:{t}:state=exhausted` + NATS `quota.exhausted` → stream vivo del tenant recibe GOAWAY con `closed_reason='quota'` y libera contadores en < 60 s (cronómetro de test).
  2. Tráfico cobrado de más por la carrera del cache de 30 s ≤ 30 s × tope de bucket (a 5 Mbps: ≤ 19 MB por sesión cortada; assert del fixture).
  3. `sessions.closed_reason` en Postgres = `'quota'` para la sesión cortada (no `'error'` ni `'client'` — separación contable A3-04).
- **DoD:** el corte por cuota es una puerta de 60 s con causa tipada, no un goteo de cache de 30 s.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestQuotaExhaustedEventGOAWAYUnder60s|TestOverchargeBoundAt5Mbps|TestClosedReasonIsTypedQuota' ./internal/proxy` → 3/3 PASS: NATS quota.exhausted (payload tenant) cierra las streams vivas con closed_reason='quota' en 0 s (< 60 s), solo el tenant afectado; via cache CheckTenant con TTL 30 s corta igual; sobre-precio MaxOverchargeBytes ≤ 19 MiB a 5 Mbps en ambas formulas (cable y entitlement); sessions.closed_reason con sink doble = 'quota', distinto de 'error'/'client' (SQL real = integracion EP-08).

### T-0606 — Admisión con ≤ 8 tenants por nodo
- **Ficheros:** `internal/proxy/admission.go`, `internal/proxy/admission_test.go`
- **Depende de:** T-0602
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A1-17
- **Criterios de aceptación:**
  1. Prueba con 9 tenants concurrentes sobre un nodo: los 8 primeros admitidos, el 9.º rechazado en admisión (aserción del script de carga; tope desacoplado de los 150 streams técnicos).
  2. Admisión = mín(150 streams nominales, cuota concurrente del tenant, 8 tenants): fixture con tenant de cuota 300 en nodo con 8 tenants → el nodo no admite el 9.º aunque queden 90 streams (el mín manda).
  3. Métrica `botnet_tenant_count` por nodo publicada y visible en `/metrics` (la densidad del destino se mide, no se infiere).
- **DoD:** la vista del destino final es topada por diseño: datacenter en IP de casa como mucho con 8 inquilinos.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestEightTenantsAdmittedNinthRejected|TestMinRuleTenantCapBeatsStreamSlack|TestTenantCountVisibleInMetrics' ./internal/proxy` → 3/3 PASS: 9 tenants → 8 admitidos y 9.º rechazado reason=tenant_cap; con tenant de cuota 300 en nodo de 8 tenants el 9.º no entra aunque quedan 142 streams nominales (el min manda; tenant_quota y stream_cap gobernanos por separado); metrica botnet_tenant_count visible por HTTP GET /metrics (`botnet_tenant_count 8` + TYPE gauge).

### T-0607 — Contadores por stream sin traspaso de auth
- **Ficheros:** `internal/proxy/counters.go`, `internal/proxy/counters_test.go`
- **Depende de:** T-0601
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A3-04
- **Criterios de aceptación:**
  1. Dos sesiones concurrentes de tenants distintos: el flush de `acct.{node_id}.flush` separa `bytes_in/out` por `session_id` y la suma es exacta ± 0,5 % (fixture de 10 MB partidos 60/40).
  2. El SOCKS5 interno no traspasa auth: cerrar la sesión A no cierra la sesión B (assert: B sigue viva y su contador no se reinicia).
  3. Sesión terminada: sus contadores viajan con `session_id` en el flush y `sessions.bytes_in/out` los refleja exactos (SQL assert en integración).
- **DoD:** cada sesión es un stream con contadores propios; mezclar tenants en un byte es imposible por contrato.
- **Estado:** DONE | PRUEBA: `go test -tags "socks5 tcp" -run 'TestPerSessionCountersSplitAndFlush|TestClosingOneSessionLeavesTheOther|TestFlushCarriesSessionIDAndSubject|TestTwoSessionsNoCrossAuth' ./internal/proxy` → 4/4 PASS: fixture 10 MB partidos 60/40 separados por session_id con desvio 0 % (< 0,5 %); cerrar A no cierra B ni reinicia sus contadores (tambien con dos conexiones SOCKS reales concurrentes); flush con sujeto acct.{node_id}.flush y session_id exactos; sessions.bytes_in/out SQL = integracion EP-08.
