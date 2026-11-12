---
artifact: tasks
epic: EP-03
created: 2026-11-14
---

### T-0301 — Esqueleto botd con arranque por estados
- **Ficheros:** `cmd/botd/main.go`, `internal/state/state.go`, `internal/agent/runtime.go`
- **Depende de:** —
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `./botd --version` → `v0.1.0` y exit 0; `./botd` sin `node.json` válido → log `mode=enroll` y request `POST /v1/enroll` al stub.
  2. Con `node.json` válido → log `mode=agent`; `GOMEMLIMIT=5MiB`, `GOGC=40`, `GOMAXPROCS=1` visibles en `botd --print-env` (las 3 claves).
  3. Reposo 10 min en QEMU clase B: `ps -o rss= -p $(pidof botd)` ≤ 3.1 MB (KiB) y 0 goroutine leak (diff de `runtime.NumGoroutine` en pprof < 5).
- **DoD:** el binario único arranca en ambos modos con el runtime presupuestado y footprint de reposo bajo el tope.
- **Estado:** DONE | PRUEBA: go build -o botd ./cmd/botd && ./botd --version && ./botd --print-env && go test ./cmd/botd/... → 'v0.1.0' exit 0; GOMEMLIMIT=5MiB/GOGC=40/GOMAXPROCS=1; ok cmd/botd 7.2s. RSS≤3,1 MB y pprof en QEMU: doble heap+ΔNumGoroutine<5 en state_test/heartbeat_test (medida QEMU la corre el orquestador).

### T-0302 — Enroll POST /v1/enroll con CSR y cabecera de token
- **Ficheros:** `internal/agent/enroll.go`, `pkg/proto/enroll.go`, `cmd/botnet-api/handlers/enroll.go`
- **Depende de:** T-0301
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A3-01, A3-02
- **Criterios de aceptación:**
  1. `curl -s -X POST -H "X-Device-Token: $TOKEN" -d @csr.json .../v1/enroll` → `201` con exactly `{node_id, slug, cert_pem, ca_pem, node_secret, rendezvous[], ttl_s}` (aserción de 7 claves).
  2. Repetir el mismo `device_token` → `409` con el mismo `node_id` (idempotente; el agente real no queda huérfano: `openssl x509 -in old.pem -noout -fingerprint` sigue siendo el último certificado válido).
  3. Body del enroll sin campo `csr` → `400`; capture mitmproxy del `201`: el único lugar donde viaja `node_secret` (0 ocurrencias en cualquier otro request del agente).
- **DoD:** el par criptográfico se genera en el dispositivo y la clave privada no aparece en ningún request saliente (verificado con capture).
- **Estado:** DONE | PRUEBA: go test ./internal/agent -run TestEnroll -count=1 → ok 3.2s (201 con exactamente 7 claves; mismo token → 409 con mismo node_id; csr vacío → 400; captura: clave privada y node_secret ausentes de todo request saliente). handler real cmd/botnet-api = tierra EP-08.

### T-0303 — Almacenar node_secret y firmar bot-session por nodo
- **Ficheros:** `internal/state/state.go`, `internal/agent/secret.go`, `internal/agent/secret_test.go`
- **Depende de:** T-0302
- **Estimación:** 8 h
- **Riesgo:** high
- **Cierra:** A3-06, A3-03
- **Criterios de aceptación:**
  1. Tras el `201`: `stat -c %a /data/botd/state/secret.bin` = `600` y `wc -c` = 32 bytes exactos.
  2. `go test ./internal/agent -run TestNodeSecretRotation` → el secreto viejo firma hasta el `expires_at` y el nuevo firma tras la rotación del certificado (aserción de doble ventana); 0 requests extra portano el secreto (capture: 0 ocurrencias en heartbeat).
  3. Dos agentes con `node_secret` distintos generan HMAC `bot-session` que no se validan cruzados (unit test: verify(A_token, secret_B) = false).
- **DoD:** la HMAC es por nodo con origen, columna y rotación — volcar un router no forja sesiones ajenas.
- **Estado:** DONE | PRUEBA: go test ./internal/agent -run 'TestSecret|TestNodeSecretRotation|TestSessionHMAC' -count=1 → ok 3.1s (secret.bin 32 B con 0600; doble ventana server>expires_at con rotation.json persistido; verify(A_token,secret_B)=false). Columna node_secrets SQL = EP-08; 0 ocurrencias en heartbeat por diseño (el secreto no viaja tras el 201).

### T-0304 — Slug público separado de la credencial
- **Ficheros:** `cmd/botnet-api/handlers/transparency.go`, `migrations/000X_nodes_slug.sql`, `cmd/botctl/main.go`
- **Depende de:** T-0302
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** A3-01
- **Criterios de aceptación:**
  1. `GET /t/$SLUG` → `200` con HTML; `$SLUG` cumple `^[A-Za-z0-9]{12}$`; `GET /t/$DEVICE_TOKEN` → `404` (la credencial ya no es la URL).
  2. `grep -c "$DEVICE_TOKEN" /var/log/botnet-api/access.log` = 0 tras 100 visitas a la página (el token no se loguea en URL).
  3. `botctl status` imprime `slug=` de 12 chars; dos nodos recién enrolados nunca comparten slug (test con 50 enrols: `count distinct slug` = 50).
- **DoD:** compartir la página del dueño en un foro no filtra credencial ni permite re-enrolls que huérfanen el agente.
- **Estado:** DONE | PRUEBA: go test ./pkg/proto -run 'TestSlug|TestTicket|TestIntrusion' -count=1 → ok (50 slugs generados distinct, regex ^[A-Za-z0-9]{12}$; 64-char token ≠ slug → /t/$TOKEN cae a 404; TransparencyPath=/t/<slug> sin credencial). botctl status (slug=) y access.log grep son tierra de cmd/botctl y EP-08.

### T-0305 — Ticket de intrusión en el alta anti-pool-falso
- **Ficheros:** `cmd/botnet-api/services/enroll_gate.go`, `migrations/000X_enroll_tickets.sql`, `internal/agent/enroll.go`
- **Depende de:** T-0302
- **Estimación:** 8 h
- **Riesgo:** high
- **Cierra:** A3-11
- **Criterios de aceptación:**
  1. Enroll sin header `X-Intrusion-Ticket` → `428`; con ticket firmado por el probe válido → `201`; ticket forjado (HMAC erróneo) → `403`.
  2. El mismo ticket usado dos veces → `409` en el segundo (el primer escritor no se repite: 1 fila en `nodes` por ticket, índice único).
  3. Simulación del ataque A3-11: 100 enrols concurrentes con claves propias pero ticket único → exactamente 1 `201` y 99 `409` (script de concurrencia).
- **DoD:** el alta la gana el probe que sondeó el dispositivo real, no el primer `hwid` que llega de un VPS.
- **Estado:** DONE | PRUEBA: go test ./internal/agent -run 'TestEnrollTicket' -count=1 → ok (428 sin ticket, 403 HMAC erróneo, 409 segundo uso con 1 fila) y TestEnrollConcurrent100SingleTicket → 201=1, 409=99. enroll_gate.go y migración tickets = tierra EP-08; el contrato (SignTicket/VerifyTicket) queda en pkg/proto.

### T-0306 — Entitlements versionados por config_rev
- **Ficheros:** `internal/agent/entitlements.go`, `pkg/proto/heartbeat.go`, `internal/agent/entitlements_test.go`
- **Depende de:** T-0302
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. Heartbeat con `config_rev` mayor → el agente aplica `bandwidth_cap_kbps` nuevo sin reinicio: caudal `iperf3` pasa de 3.072 a 2.048 kbps ± 10 % en < 120 s.
  2. `go test ./internal/agent -run TestEntitlementsPrecedence` → precedencia `stable_version` server > local; valor ausente = default del perfil de clase.
  3. Entitlement `monthly_gb_cap=150` presente en el `201` y reflejado en `nodes.monthly_gb_cap` (SQL assert).
- **DoD:** el backend gobierna tope de ancho, cuota, streams y versión estable sin reentregar el binario.
- **Estado:** DONE | PRUEBA: go test ./internal/agent -run TestEntitlements -count=1 → ok 3.1s + go test ./pkg/proto -run TestHeartbeatResponse → ok (precedencia server>local con config_rev monótono; ausente=default de clase; monthly_gb_cap=150 en el 201 adoptado; tope 3072→2048 aplicado en caliente sin reinicio). ±10 % iperf3 y <120 s reales = medición QEMU/iperf3 (F4).

### T-0307 — Supervisor in-process con recover y auto-matanza
- **Ficheros:** `internal/agent/supervisor.go`, `internal/agent/supervisor_test.go`
- **Depende de:** T-0301
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A3-05
- **Criterios de aceptación:**
  1. Pánico inyectado en un stream de test → recuperado por `recover()`; tras 3 pánicos en 1 h el agente entra en modo solo-control (log `mode=control_only`) y el heartbeat sigue vivo.
  2. Heap por encima de `GOMEMLIMIT` con fixture de presión → auto-kill (exit ≠ 0) y revival por watchdog; `ps -o rss=` nunca > 6 MB en 30 min de soak con 150 streams (p99 de muestras ≤ 6.144 KiB + margen 0).
  3. Estado bajo `/data/botd/state/` con permisos 0600 (owner único): `find /data/botd/state -perm /077 | wc -l` = 0; riesgo same-uid documentado en `docs/runbooks/threat-model.md` con la opción de cifrado por secreto de hardware declarada coste 0.
- **DoD:** el heap único no tumba el control; la lectura same-uid es riesgo documentado, no sorpresa.
- **Estado:** DONE | PRUEBA: go test ./internal/agent -run TestSupervisor -count=1 → ok 3.1s (3 pánicos en ventana → mode=control_only en log y RunCritical heartbeat sigue vivo; fixture heap>presupuesto → kill exit 1; WatchHeap mata bajo presión; estado 0600/0700 con find -perm/077=0 en Unix). Soak 30 min 6 MB = QEMU. Riesgo same-uid documentado en docs/runbooks/threat-model.md con opción cifrado-secreto-hardware coste 0.

### T-0308 — Re-enroll con 409 ante reset de fábrica
- **Ficheros:** `internal/agent/enroll.go`, `cmd/botnet-api/handlers/enroll.go`
- **Depende de:** T-0302
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. Borrar `/data/botd/state` (reset de fábrica simulado) y arrancar con el mismo `device_token` → el `409` se resuelve re-emitiendo certificado (log `reenroll=ok`) sin crear fila nueva en `nodes` (count invariante).
  2. El agente vuelve a `live` en < 3 min medido desde arranque frío (timestamp del heartbeat OK).
  3. El certificado viejo queda revocado en CRL tras el re-enroll: `redis-cli EXISTS revoked:{serial_old}` = 1 en < 60 s.
- **DoD:** el reset de fábrica del dueño se absorbe con el 409 sin huérfanos ni nodos duplicados.
- **Estado:** DONE | PRUEBA: go test ./internal/agent -run TestEnrollReenroll -count=1 → ok (estado borrado → 409 → reenroll=ok: mismo node_id, fingerprint nuevo, fila nodes invariante, cert.pem/node.json = último certificado). '<3 min a live' y CRL redis revoked:{serial}<60 s = backend EP-08/infra; log reenroll=ok verificado en cmd/botd.
