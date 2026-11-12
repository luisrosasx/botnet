---
artifact: tasks
epic: EP-14
created: 2026-11-14
---

### T-1401 — Matriz cross estática de 6 arquitecturas
- **Ficheros:** `Makefile`, `scripts/cross/build_matrix.sh`, `deploy/sliver-builder/profiles.yaml`
- **Depende de:** —
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `make cross` → 6 ELFs en `dist/` (amd64, arm64, armv5, armv7, mipsbe, mipsle); `file` no dice `dynamically linked` en ninguno.
  2. `size -t dist/*` por clase: perfil A ≤ 4,0 MB; perfiles B/C con upx ≤ 2,6 MB (`upx -t` OK); puerta F1: las 5 archs de la fase con `size` ≤ 4 MB.
  3. `CGO_ENABLED=0` fijado en el recipe (aserción del script; `go env GOOS GOARCH` del build cross que imprime y `go version -m dist/botd-mipsbe` muestra `CGO_ENABLED=0` en build settings).
- **DoD:** un binario × 6 archs, todos estáticos y bajo el flash de su clase — la matriz del BRIEF sin multiplicar artefactos.
- **Estado:** DONE | PRUEBA: `bash scripts/cross/build_matrix.sh` (=make cross; host sin `make` en PATH) → "cross OK: 6/6 ELFs estaticos en dist/ version=32502a0"; tamaños 2,44–2,70 MB post-upx (A≤4 MiB, B/C≤2,6 MiB); `go env GOOS GOARCH` y `go version -m dist/botd-mipsbe` muestran CGO_ENABLED=0; `file` sin "dynamically linked" en 6/6.

### T-1402 — Humo QEMU por arquitectura contra stub
- **Ficheros:** `scripts/qemu/smoke.sh`, `scripts/qemu/stub_api.go`
- **Depende de:** T-1401, T-0302
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `make qemu-smoke` → por arch: arranque, enroll (stub responde `201` con claves del contrato) y `HEARTBEAT 200 ok` en log en < 60 s por arch (6/6).
  2. mipsbe: assert de endianness en el handshake (magic `BE` verificado en el capture del humo — un binario mal compilado pierde la familia entera).
  3. RSS medido en el humo clase C (QEMU 32 MB): ≤ 3,1 MB en reposo (`ps -o rss=`); 0 core dumps en `dist/qemu-logs/`.
- **DoD:** la puerta F1 del blueprint es automática: 5-6 binarios que enrollan y laten en emulación, no en la promesa.
- **Estado:** DONE | PRUEBA: banco x86_64 Ubuntu 26.04 por SSH con qemu-user 10.2.1 (instalado allí, `qemu-user`); `bash scripts/qemu/smoke.sh` → `qemu-smoke: OK 6/6 archs enrolan y laten (<60 s) RSS<=3,1 MB core=0` rc=0, stub con 6 pares `ENROLL 201`+`HEARTBEAT 200 ok` en 7 s totales (≈1,2 s/arch), RSS del agente 796 KiB (mipsbe) y 844 KiB (mipsle) descontada la base del emulador, 0 core dumps. Se emula la copia desempaquetada de `dist/raw/` (el stub UPX no sobrevive a qemu-user).

### T-1403 — Manifiesto ed25519 con raíz fijada en build
- **Ficheros:** `deploy/signing/sign_release.sh`, `deploy/signing/verify_release.sh`, `internal/agent/update.go`
- **Depende de:** T-1401
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** injerto 11, A1-11
- **Criterios de aceptación:**
  1. `scripts/signing/sign_release.sh dist/ keys/root_ed25519.pub` → `manifest.json` con `{version, sha256, url, sig}` por arch; `verify_release.sh` → exit 0; alterar 1 byte del `sig` → exit ≠ 0.
  2. `botd --print-root` imprime la pubkey raíz compilada en el binario (coincide byte a byte con `keys/root_ed25519.pub`); un manifiesto firmado con clave ajena se rechaza ANTES de ejecutar (log `update=rejected_sig`).
  3. El manifest sigue el renaming de T-1305: `botctl status | grep name` == entrada del manifest por clase (assert cruzado); sha256 solos ya no bastan — el MITM no puede servir el binario que él mismo hasheó.
- **DoD:** el suministro tiene anclaje criptográfico con raíz propia: una release falsa exige la raíz, no la ruta.
- **Estado:** DONE | PRUEBA: `bash deploy/signing/sign_release.sh dist/ deploy/signing/keys/root_ed25519.pub` → manifest.json {version,sha256,url,sig}×6; `verify_release.sh` → "verify: OK 6 entradas" exit 0; sig alterado 1 byte → rc=1 con `update=rejected_sig`; `go test ./cmd/botnet-build/...` ok (print-root byte a byte y name por clase T-1305).

### T-1404 — Self-update con commit, rollback y estable pinned
- **Ficheros:** `internal/agent/update.go`, `internal/agent/update_test.go`
- **Depende de:** T-1403, T-0306
- **Estimación:** 10 h
- **Riesgo:** high
- **Cierra:** A2-05
- **Criterios de aceptación:**
  1. Flujo e2e: descarga a `botd.new` → rename atómico → `exit 75` → el watchdog revive → `--commit` tras 5 min sanos (`botd --version` nueva y `stable_version` local actualizado).
  2. Rollback server-side: 3 salidas 75 en 15 min (crash-loop fixture) → backend fija `nodes.stable_version` a la versión anterior y el agente vuelve atrás en < 20 min con reloj acelerado (assert de versión final).
  3. A/B local solo en clase A: `ls /data/botd | grep -c 'botd.[AB]'` = 2 en clase A y 0 en B/C (slot único, presupuesto de flash intacto: T-0204).
- **DoD:** actualizar es reversible sin humano: commit tras 5 min, rollback por crash-loop, raíz firmada en medio.
- **Estado:** DONE | PRUEBA: `go test ./internal/agent/...` → ok; TestStageCommitE2E (download httptest→botd.new→rename→commit stable_version), TestCrashLoopRollbackAcceleratedClock (3×75 en 15 min → versión final=stable <20 min), TestSlotPolicyByClass (botd.[AB]=2 en clase A, 0 en B/C).

### T-1405 — Resolver DNS para destinos en vez de IPs compiladas
- **Ficheros:** `internal/tunnel/resolver.go`, `internal/tunnel/resolver_test.go`
- **Depende de:** T-0402
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A2-10
- **Criterios de aceptación:**
  1. Los destinos de control se resuelven por DNS con TTL + jitter por nodo (0 IPs compiladas en el binario: `strings dist/botd-amd64 | grep -Ec '([0-9]{10,}\.)'` sin IPv4 fijas de backend).
  2. Zona DNS de test: cambio de registro → el 100 % de los nodos simulados re-asignan destino en ≤ TTL + jitter (24 h worst case; assert con reloj acelerado) sin reentregar binario.
  3. Resolver caído (fixture sin DNS): el agente conserva el último destino válido y reintenta en < 5 min; 0 arranques en pánico (log sin `resolver fatal`).
- **DoD:** mover el backend de IP es un registro DNS: la re-intrusión masiva de A2-10/A1-10 no vuelve a ocurrir.
- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel/...` → ok (100 nodos re-asignan en ≤TTL+jitter con reloj acelerado; DNS caído conserva destino y reintenta <5 min); 0 IPs fijas en binario: `grep -a -Eo '([0-9]{1,3}\.){3}[0-9]{1,3}' dist/botd-amd64 | wc -l` → 0.

### T-1406 — Multi-SNI, jitter de puerto y egress por release
- **Ficheros:** `internal/tunnel/dispersion.go`, `scripts/release_egress.sh`, `docs/runbooks/release.md`
- **Depende de:** T-0407, T-1401
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A1-13
- **Criterios de aceptación:**
  1. Capture de 50 agentes simulados actualizando: puertos destino con histograma de ≥ 8 buckets y ≥ 2 SNI (la correlación cadencia↔volumen del DPI del enlace no tiene un solo destino).
  2. `scripts/release_egress.sh --nodes=100000` → egress total de una release ≤ 500 GB y bytes por nodo ≤ 4,5 MB (perfil A) — la pesadilla de ~150 GB por módulo de P2 no se repite con 1 binario.
  3. `docs/runbooks/release.md` declara la cadencia de release (ventanas, no constantes globales) y su línea en la hoja de plano de datos (capa de túneles: 0 incremental).
- **DoD:** una release a 100k es un evento presupuestado en egress y disperso en destino: ni avalancha ni firma.
- **Estado:** DONE | PRUEBA: `go test ./internal/tunnel/...` ok (capture 50 agentes: ≥8 buckets de puerto y ≥2 SNI); `bash scripts/release_egress.sh --nodes=100000` → "por_nodo=2.70 MB total=269.8 GB OK" rc=0 (--nodes=200000 → rc=1); docs/runbooks/release.md declara ventanas y línea de túneles 0 incremental.

### T-1407 — Conciliación horaria de bytes_delta sin bytes extra
- **Ficheros:** `cmd/botnet-api/jobs/hourly_reconcile.go`, `cmd/botnet-api/jobs/hourly_reconcile_test.go`
- **Depende de:** T-1001, T-0404
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A2-15
- **Criterios de aceptación:**
  1. `make reconcile-hourly` sobre fixture de 24 h: desvío `bytes_delta` (viaja en el heartbeat) ↔ `usage` ≤ 0,5 % en las 24 horas (0 horas en rojo; el 5 % estructural de P3 se vigila, no se hereda).
  2. Cero bytes extra: la conciliación no añade requests por nodo (contador de red del agente en la hora de test sin delta medible vs hora sin job).
  3. Hora con desvío > 0,5 % en fixture → alarma `usage.audit` publicada y el cobro no se bloquea (aserción del job: publica y sigue).
- **DoD:** la tolerancia contable se audita cada hora con datos que ya viajan: cero coste, cero sorpresa mensual.
- **Estado:** DONE | PRUEBA: `go run ./cmd/botnet-build reconcile-hourly` (=make reconcile-hourly) → "24h, 0 horas en rojo (tol 0.50%), 0 alarmas usage.audit"; `go test ./cmd/botnet-api/jobs/...` → ok (fixture rojo publica y no bloquea; lecturas 2/hora, cero requests al agente).
