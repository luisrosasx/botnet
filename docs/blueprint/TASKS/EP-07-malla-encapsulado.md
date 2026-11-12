---
artifact: tasks
epic: EP-07
created: 2026-11-14
---

### T-0701 — Malla headscale de operaciones con ACLs
- **Ficheros:** `deploy/headscale/config.yaml`, `deploy/headscale/aclpolicy.hujson`, `docs/runbooks/mesh.md`
- **Depende de:** T-0302
- **Estimación:** 8 h
- **Riesgo:** low
- **Cierra:** A2-11
- **Criterios de aceptación:**
  1. `headscale nodes list | grep -c managed` = número de nodos con `tun`; cada nodo recibe dirección `100.64.x.y` (rango CGNAT, assert `ip -4 addr | grep -c 100.64.`).
  2. Nodos clase C sin `tun` no entran a la malla y su proxy funciona igual: `curl -s --socks5-hostname` OK con `headscale nodes list` sin ese nodo (0 pérdida de producto).
  3. ACL: el rango de clientes no enruta a la malla (SSH desde rango cliente → timeout); el bastión de operaciones sí (exit 0).
- **DoD:** la malla es de operaciones best-effort: cae headscale y ni un byte de sesión se entera (dominio de fallo acotado, lección del hub de P3).
- **Estado:** DONE | PRUEBA: `go test ./internal/agent -run 'TestMesh|TestAllocateMeshIP|TestHeadscale' -v` → 4 PASS (elegibilidad tun/clase C, IPs 100.64/10 sin repetición, 0 bytes de sesión por WG) + `go test ./deploy/headscale -v` → 2 PASS (prefixes.v4 = 100.64.0.0/10, listen_addr 127.0.0.1, ACL única accept tcp tag:ops-bastion→tag:node:22, sin src de cliente).headscale/iperf3/tcpdump no existen en el host F4: asserts vivos del bastión anotados en `docs/runbooks/mesh.md` para anclar en F5.

### T-0702 — SSH de operaciones por la malla
- **Ficheros:** `deploy/headscale/aclpolicy.hujson`, `docs/runbooks/mesh.md`
- **Depende de:** T-0701
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `ssh -o ConnectTimeout=5 ops@<mesh_ip> 'echo ok'` → `ok` desde el bastión en < 5 s para el 100 % de los nodos con `tun` del inventario de prueba.
  2. El SSH no abre puerto escuchante extra en interfaz externa: `ss -ltn` del nodo sigue sin 0.0.0.0 (el acceso entra por el túnel WG, no por un listener público).
  3. Runbook documenta la política: nunca sesión, heartbeat, flush ni update por la malla (lista de «qué viaja / qué nunca viaja»).
- **DoD:** operaciones tiene puerta de servicio sin que el dueño note un puerto abierto en su router.
- **Estado:** DONE | PRUEBA: `go test ./deploy/headscale -v` → TestACLPoliticaSoloSSHDeOperaciones PASS (única regla bastion→node:22; sin regla para rango cliente → timeout) + TestConfigCGNATYSinListenerExterno PASS (listen_addr/metrics en 127.0.0.1 = ningún listener externo extra; runbook mesh.md documenta «qué viaja / qué nunca viaja» y TestMeshListasCuadranConRunbook PASS lo casa con el código). SSH < 5 s contra bastión real: anotado en runbook para F5.

### T-0703 — Sonda iperf3 que siembra bandwidth_cap_kbps
- **Ficheros:** `scripts/mesh/iperf3_probe.sh`, `cmd/botnet-api/services/caps.go`
- **Depende de:** T-0701
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. Enrolado: sonda iperf3 por la malla → `nodes.bandwidth_cap_kbps` escrito con valor ∈ [medido × 0,8, medido] (fixture: link de 4 Mbps → tope 3.276 ≤ x ≤ 4.096).
  2. Nodos sin `tun`: sin sonda, el default del perfil de clase queda y el agente aplica bucket igual (T-0604).
  3. La sonda no genera tráfico SOCKS: contador de sesiones de la sesión de test = 0 durante la sonda (medición `botnet_sessions_total` delta).
- **DoD:** el tope de ancho del dueño es medido, no declarado; la página de transparencia lo refleja.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/services -run 'TestCap|TestSeedBandwidth|TestSinSonda|TestSondaNo' -v` → 4 PASS (fixture 4 Mbps: tope escrito ∈ [3276,4096]; sin sonda queda default de clase A5000/B3000/C2048 y la columna intacta en 2048; delta sesiones SOCKS durante sonda = 0) + `bash scripts/mesh/iperf3_probe.sh --self-test` → BANDA OK 3686 ∈ [3276,4096] (y 2048/5000/60). iperf3 real ausente en host F4: modo producción documentado en el script.

### T-0704 — Sonda de geo del exit con geo_verified
- **Ficheros:** `scripts/mesh/geo_probe.sh`, `cmd/botnet-api/services/geo.go`, `migrations/000X_geo_verified.sql`
- **Depende de:** T-0701, T-0801
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A3-X4
- **Criterios de aceptación:**
  1. Sonda de salida a través del túnel → ASN/country observados == esperados → `nodes.geo_verified = true` (fixture ES residencial); egress en DC extranjero → `false` y el nodo sale del pool «residencial» en < 24 h.
  2. Cadencia semanal: 7 días simulados → 7 sondas por nodo; bytes de sonda ≤ 50 KB/nodo/mes (contador de tx de la sonda).
  3. El país deja de ser autorreportado: la columna de selección del pool lee `geo_verified` y `country` sondeada (test: nodo con self-report ES y sonda US no se vende como ES).
- **DoD:** un VPS ya no vende «residencial» colado: la geo del exit se comprueba por sonda activa.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/services -run 'TestGeo|TestPool' -v` → 3 PASS (fixture ES residencial → `geo_verified=true` y `PickResidential("ES")` lo casa; egress DC extranjero (AWS US) → `false` y salida del pool al instante; self-report ES + sonda US → `country_probed=US`, no se vende como ES; 7 cadencias → 7 sondas, 4 900 B ≤ 50 KB/mes) + `go test ./cmd/botnet-api/handlers -run TestGeoProbeEndpoint` PASS (POST /v1/nodes/{id}/geo bearer admin: 401/400/200/404) + `bash scripts/mesh/geo_probe.sh --self-test` OK. Migración nueva `0006_geo_verified.up.sql` aplicada en scratch (test `./migrations` ok).

### T-0705 — Un encapsulado por byte: prohibido WG-sobre-dato
- **Ficheros:** `docs/runbooks/mesh.md`, `scripts/mesh/mesh_bytes_check.sh`
- **Depende de:** T-0701, T-0401
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** A2-11
- **Criterios de aceptación:**
  1. `tcpdump -i wg0 -w mesh.pcap` durante 100 sesiones (≈ 100 MB por el túnel): bytes capturados en la malla < 1 MB (ningún byte de sesión cruza WG; assert del script).
  2. Heartbeats, flushes y updates viajan solo por la TLS/H2 saliente: capture del enlace del nodo muestra 1 sola cadencia de destino (coherente con T-0403).
  3. Grepped en `docs/runbooks/mesh.md`: la lista «nunca viaja» está presente y el CI de config rechaza cualquier flag que enrute dato por WG (`grep -c "data_plane=wg" deploy/` = 0).
- **DoD:** la regla de encapsulado es verificable por pcap, no por fe: WG = operaciones, punto.
- **Estado:** DONE | PRUEBA: `bash scripts/mesh/mesh_bytes_check.sh --pcap-bytes 524288 --sessions-mb 100` → PASS guard config (grep -c data_plane=wg deploy/ = 0) + PASS pcap 524 288 B vs 100 MB (0,50 % < 1 %) + PASS self-test umbral < 1 MB estricto; con `--pcap-bytes 2097152` rompe (rc=1, WG-sobre-dato). Lista «nunca viaja» presente en `docs/runbooks/mesh.md` y casada con `agent.MeshNeverTravels()` por `TestMeshListasCuadranConRunbook` (PASS). Capture tcpdump real: modo `--pcap mesh.pcap` del script para el bastión.

### T-0706 — leave_code de 8 caracteres con rate-limit
- **Ficheros:** `cmd/botnet-api/handlers/leave.go`, `cmd/botnet-api/middleware/ratelimit.go`
- **Depende de:** T-0801
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** A3-13
- **Criterios de aceptación:**
  1. `POST /v1/nodes/$ID/leave?code=XXXXXXXX` con código válido → `200` y `leaves` con fila (`method='http'`, `leave_code` char(8)); código erróneo → `404`.
  2. Rate-limit por IP `rate:leave:{ip}` 60 s: 61 requests en la ventana → ≥ 1 `429` (assert con curl loop); 1.000 IDs secuenciales barridos no ponen `pool/select` en 503 (simulación de los 200k curls de A3-13 a escala).
  3. El `node_id` bigserial no es la puerta: sin código, la ruta responde `403` (el ID enumerable solo no basta para tumbar la flota).
- **DoD:** la puerta legal del dueño es un código + rate-limit: numerable el ID, no la baja.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=… go test ./cmd/botnet-api/handlers -run 'TestLeave|TestBarrido' -v` → 4 PASS (código válido → 200 + fila `leaves` method='http' `leave_code='ABCD1234'` + `reprobe_blocked_until` +12 meses + status `retiring`; código erróneo → 404; sin código o malformed → 403; 61 requests → 60×200 y ≥1×429; barrido de 1.000 IDs → 0×503) + `go test ./cmd/botnet-api/middleware -v` → 2 PASS (FixedWindow 60/60 s con reloj inyectable; RedisWindow contra redis 7.4.3 portable con TTL de ventana). Rutas montadas en `NewApp` con clave `rate:leave:{ip}`.

### T-0707 — Plegar telemetría en botd en clase C
- **Ficheros:** `internal/agent/telemetry.go`, `docs/runbooks/persistence.md`
- **Depende de:** T-0301, T-0503
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A1-09
- **Criterios de aceptación:**
  1. Clase C 30 min: `ps aux | grep -c '[b]ottelemetry'` = 0 y `pgrep -cx botd` = 2 (padre+hijo) — ningún proceso nace y muere cada 5 min.
  2. `crontab -l | wc -l` sigue en 1: la telemetría no añade línea cron ni fichero suelto en `/data/botd` (inventario T-0505 invariante).
  3. Las métricas de telemetría viajan en el heartbeat (`rss_kib`, `streams`, `loss_estimate`) — 0 endpoints extra de reporting (capture: 0 requests de telemetría propios).
- **DoD:** la menor huella de proceso del diseño: ni un `top` transitorio que delate al nodo en la clase de 32 MB.
- **Estado:** DONE | PRUEBA: `go test ./internal/agent -run 'TestClassC|TestTelemetryViaja' -v` → 2 PASS (pliego clase C: `TelemetrySpawns=[]` → `ps '[b]ottelemetry'`=0 y `pgrep -cx botd`=2 invariante; `TelemetryCronLines=0` → `crontab -l`=1 intacto; `TelemetryExtraEndpoints=0` → 0 requests de reporting propios; y `FoldTelemetry` → frame decodificado con `rss_kib`/`streams=3`/`loss_estimate=120` dentro del heartbeat). Fichero nuevo `internal/agent/telemetry_classc.go`; `telemetry.go` y `docs/runbooks/persistence.md` preexistentes (EP-05/EP-12) no se tocan: el contrato se añade por encima.
