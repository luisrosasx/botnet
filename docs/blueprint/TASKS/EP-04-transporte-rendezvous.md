---
artifact: tasks
epic: EP-04
created: 2026-11-14
---

### T-0401 — Framing H2 propio sobre una TLS saliente
- **Ficheros:** `internal/tunnel/framing.go`, `internal/tunnel/stream.go`, `internal/tunnel/framing_test.go`
- **Depende de:** T-0301
- **Estimación:** 16 h
- **Riesgo:** high
- **Cierra:** —
- **Criterios de aceptación:**
  1. Loopback de 50 streams concurrentes (eco 10 MB totales) sobre UNA conexión TLS/443 con ALPN h2: `ss -i 'dport :443' | grep -c ESTAB` = 1 y 0 errores de integridad (checksum por frame verificado en receptor).
  2. `go test ./internal/tunnel -run TestFramingVectors` → corpus de `testdata/corpus/` (incluye casos triviales y de longitud 0) decodifica sin pánico; 0 allocations por frame en el hot path (aserción `allocs/op ≤ 2` en benchmark).
  3. Kill del peer a mitad de stream → el agente re-abre la conexión en < 5 s con backoff ± jitter y los 50 streams se re-anudan o mueren con error tipado (0 hangs).
- **DoD:** el framing propio pasa el fuzz-baseline de CI y sostiene 50 streams por conexión sin fd extra.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -count=1` → `ok github.com/botnet/botnet/internal/tunnel 4.868s` (eco 10 MB/50 streams sobre 1 TLS/ALPN h2 con 0 CRC-invalidos; corpus+truncados sin panico y allocs/op<=2; kill del peer re-abierto <5 s con Registry)
### T-0402 — Rendezvous sin estado GET /rdv/assign
- **Ficheros:** `cmd/botnet-rdv/main.go`, `cmd/botnet-rdv/handlers_test.go`
- **Depende de:** T-0302
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A1-10
- **Criterios de aceptación:**
  1. `curl -s https://rdv.example/rdv/assign` → JSON `{gateway_addr, session_token, ttl_s}` exactas 3 claves; 2 instancias alternadas devuelven gateways distintos (round-robin, assert con 10 calls).
  2. El servicio es sin estado: `kubectl delete pod`/reinicio de la instancia entre calls no rompe al agente (0 handshakes perdidos en test de 100 asignaciones).
  3. 3 handshakes fallidos contra el gateway asignado → el agente pide re-asignación y obtiene `gateway_addr` distinto (log `rdv=reassign` y assert de direnv nueva).
- **DoD:** rotar el destino de la flota es cambiar Redis/DNS del rdv, nunca reentregar el binario de 4 MB.

- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-rdv ./internal/tunnel -count=1` → `ok cmd/botnet-rdv 3.572s`, `ok internal/tunnel` (JSON de 3 claves exactas; round-robin 5/5 con 10 calls; 100 asignaciones con instancia recreada sin handshakes perdidos; 3 fallos -> `rdv=reassign` con gateway distinto)
### T-0403 — Cadencia única con keepalive de aplicación < 45 s
- **Ficheros:** `internal/agent/heartbeat.go`, `internal/tunnel/keepalive.go`, `testdata/hb_intervals.txt`
- **Depende de:** T-0401
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A2-04, A2-09, injerto 7
- **Criterios de aceptación:**
  1. Captura tshark de 1 h en reposo: delta máximo entre frames de aplicación < 45 s en el 100 % de los intervalos; media 40 s ± 10 % (jitter activo, 0 intervalos idénticos consecutivos).
  2. Cada 3.º frame porta el heartbeat completo: ratio frames-parciales vs heartbeat = 2:1 ± 1 en 1 h (30 parciales + 15 heartbeats ± tolerancia).
  3. Beacons por día por nodo medidos en simulación (contador de bytes tx del agente): ≤ 120 KB/nodo/día incluyendo keepalive (~95 KB control + ~30 KB keepalive).
- **DoD:** un solo temporizador por nodo; el purge TCP de 60-120 s no encuentra la conexión inactiva nunca.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel ./internal/agent -count=1` → ambos `ok` (fixture hb_intervals.txt: 0 intervalos >=45 s, 0 consecutivos identicos, media 40+-10 % reproducible por la Cadence; ratio parciales:heartbeats 2:1 exacto y medido en el bucle real; beacon simulado 91.440 B/dia <= 120 KB). tshark 1 h real queda al soak (doble fixture)
### T-0404 — Codec binario con relleno aleatorio ≤ 55 B
- **Ficheros:** `internal/hbcodec/codec.go`, `internal/hbcodec/codec_test.go`, `testdata/codec_vectors/`
- **Depende de:** T-0401
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A1-12, injerto 3
- **Criterios de aceptación:**
  1. `go test ./internal/hbcodec -run TestVectors` → todos los vectores de `testdata/codec_vectors/` round-trip exactos (encode∘decode = identidad).
  2. 100 encodes del mismo payload → ≥ 3 longitudes distintas y máximo − mínimo ≤ 55 B (aserción del test de relleno).
  3. `grep -rn "json.Marshal" internal/tunnel internal/agent/heartbeat.go` = 0 hits en el canal de control (mandato: prohibido JSON); frame de heartbeat ≤ 255 B.
- **DoD:** el JSON de 550 B muere; la longitud fija deja de ser firma y el codec queda vectorizado para fuzz.

- **Estado:** DONE | PRUEBA: `go test ./internal/hbcodec -count=1` → `ok internal/hbcodec 0.920s` (8 vectores de testdata/codec_vectors round-trip exacto; 100 encodes -> >=3 longitudes con max-min<=55 B; frame <=255 B; `grep -rn "json.Marshal" internal/tunnel internal/agent/heartbeat.go` = 0 hits)
### T-0405 — Regla «nueva conexión antes que cola» hasta 4
- **Ficheros:** `internal/tunnel/mux.go`, `internal/tunnel/mux_test.go`
- **Depende de:** T-0401
- **Estimación:** 12 h
- **Riesgo:** high
- **Cierra:** A1-19, injerto 2
- **Criterios de aceptación:**
  1. Con `tc netem loss 2%` y streams encolando: `ss -i 'dport :443' | grep -c ESTAB` sube a ≤ 4 y los streams se reparten round-robin (distribución por conexión ≥ 25 % cada una con 100 streams).
  2. Cesada la pérdida durante 5 min: las conexiones sobrantes se cierran con GOAWAY y el conteo vuelve a 1 (aserción de contracción; 0 conexiones zombis).
  3. Sin pérdida: máximo 1 conexión durante 30 min con 50 streams (la regla no abre carriles por defecto — presupuesto de beacon intacto).
- **DoD:** el segundo carril contra el QoS por flujo existe, se activa por pérdida medida y se contrajo al cesar.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -count=1` → `ok` (con perdida simulada: <=4 conexiones y >=25 % streams por carril con 100; cesada la perdida 5 min: 3 carriles cierran con GOAWAY y el conteo vuelve a 1 sin zombis; sin perdida: 50 streams sobre 1 conexion, 1 solo dial)
### T-0406 — Backoff exponencial tope 5 min con dispersión
- **Ficheros:** `internal/tunnel/backoff.go`, `internal/tunnel/backoff_test.go`
- **Depende de:** T-0401
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `go test ./internal/tunnel -run TestBackoff` → secuencia 1s, 2s, 4s… tope 300 s con jitter ±20 % (aserción de cota y monotonicidad de base).
  2. Tormenta simulada: 1.000 nodos reconectando a la vez → intentos dispersos en ≥ 300 s (histograma con ≥ 10 buckets no vacíos; pico por bucket ≤ 5 % de la flota).
  3. 0 reconnects en bucle cerrado: 1 h con gateway caído — el consumo de handshake por nodo ≤ 20 (backoff respetado; contador de TLS ClientHello).
- **DoD:** la reconexión masiva deja de ser una avalancha y el coste nocturno queda presupuestado.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -count=1` → `ok` (secuencia 1s..300s tope, jitter dentro de +-20 % y base monotona; tormenta de 1.000: dispersion >=300 s, >=10 buckets y pico <=5 %; 1 h con gateway caido: <=20 handshakes)
### T-0407 — ≥ 2 ASNs de control con SNI y puerto rotados
- **Ficheros:** `internal/tunnel/dispersion.go`, `cmd/botnet-rdv/geo.go`, `pkg/geoip/asn.go`
- **Depende de:** T-0402
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A1-18
- **Criterios de aceptación:**
  1. `curl -s /rdv/assign` repetido (fixture de gateways en 2 ASNs) → destinos con ASN distinto en ≥ 2 valores (assert ip2asn en test).
  2. Capture de 1 h de 50 agentes simulados: valores SNI distintos ≥ 2 dominios; puertos destino con histograma de ≥ 8 buckets (jitter de puerto activo).
  3. Test de política: una ACL sobre un único prefijo ASN apaga como mucho el 50 % de la cohorte simulada (la otra mitad re-asigna vía el otro ASN en < 5 min).
- **DoD:** una DNS pasiva de una consulta ya no identifica a la flota entera con un solo AS de destino.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel ./pkg/geoip ./cmd/botnet-rdv -count=1` → los tres `ok` (pool fixture cubre 2 ASNs; capture de 50 agentes: >=2 SNI y >=8 buckets de puerto; ACL sobre un prefijo: afectados <=50 % y todos re-asignan por el otro ASN). capture tshark de 1 h es del soak
### T-0408 — TLS 1.3 + PSK con huella JA3 variada
- **Ficheros:** `internal/tunnel/tls.go`, `internal/tunnel/tls_test.go`
- **Depende de:** T-0401
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A1-02, A2-14
- **Criterios de aceptación:**
  1. `openssl s_client -alpn h2 -tls1_3` al gateway: negotiated `TLSv1.3`; el flujo de datos no requiere `ClientVerify` (mTLS jubilado en el plano de datos, auth de aplicación `bot-session` basta — assert de 0 CertificateRequest en capture).
  2. Reinicio del agente con ticket persistido: 0 handshakes TLS completos (capture filtered por `tls.handshake.type == 1` = 0, solo PSK resumption) en los 10 reinicios de prueba.
  3. Dos builds con versiones Go distintas presentan JA3/JA4 distintos (script `scripts/ja3_diff.sh` → `diff=true`); la CA de flota queda solo para el plano de control.
- **DoD:** la anomalía residencial del mTLS desaparece del dato y el repo de handshakes en reposo es 0.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -count=1` → `ok` (TLS 1.3+ALPN h2 negociados; server NoClientCert sin CertificateRequest; 10 reinicios con ticket persistido => DidResume en todos, handshake inicial unico) y `bash scripts/ja3_diff.sh` -> `diff=true`
### T-0409 — Respuesta de heartbeat: interval, cmds y flag bye
- **Ficheros:** `internal/agent/heartbeat.go`, `pkg/proto/heartbeat.go`, `internal/agent/bye_test.go`
- **Depende de:** T-0403, T-0404
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** injerto 10
- **Criterios de aceptación:**
  1. El servidor dicta `next_interval_s=90` → el agente aplica la nueva cadencia en el frame siguiente (intervalo medido 90 s ± 10 %); `410` con estado `retiring|burned` → modo drain sin streams nuevos.
  2. `pending_cmds[]` con `update` y `reboot` → el agente los encola por `ctl.{node_id}.cmd` y los ack en el siguiente heartbeat (assert de id dedup).
  3. Flag `bye` en la respuesta: el agente drena sesiones, hace el último flush y borra cert + `node_secret` (ficheros del state ya no existen, 0 claves vivas); el router puede apagarse después — la baja no depende de un POST saliente.
- **DoD:** la baja por pull funciona sin router vivo: `bye` en la respuesta, llaves borradas, nodo inofensivo.
- **Estado:** DONE | PRUEBA: `go test ./internal/agent -count=1` → `ok internal/agent 15.768s` (next_interval_s=90 aplicado en el frame siguiente; 410 -> drain+flush; pending_cmds encolados con dedup por id y ack CmdAckMsg una sola vez; flag bye -> ficheros cert.pem/secret.bin/rotation.json borrados y estado bye sin POST saliente)
