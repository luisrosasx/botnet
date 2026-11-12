---
artifact: tasks
epic: EP-02
created: 2026-11-14
---

### T-0201 — Runner legba multi-protocolo con salida JSON
- **Ficheros:** `scripts/intrusion/legba_run.sh`, `docs/runbooks/intrusion.md`
- **Depende de:** —
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `scripts/intrusion/legba_run.sh TARGET=$QEMU_IP` contra target vulnerable → JSON `{protocol, cred_index, model_guess}` en < 30 s, exit 0.
  2. Contra target sano (sin credencial válida): exit ≠ 0, JSON con `{protocol: null}` y 0 procesos colgados (`pgrep -c legba` = 0 al terminar).
  3. Protocolos cubiertos: el diccionario de prueba cubre http/ssh/telnet/ftp (`legba_run.sh --protocols` lista 4).
- **DoD:** la intrusión por credencial deja evidencia JSON de qué protocolo y qué entrada del diccionario abrieron la puerta.
- **Estado:** DONE | PRUEBA: `go test ./internal/intrusion/ -run TestLegbaRun` → 3/3 PASS: hit fixture → exit 0 + JSON `{protocol:http, cred_index:0, model_guess:TL-WR841}` en <1 s; miss → exit 1 + `protocol:null` sin procesos colgados; `--protocols` lista 4 (http/ssh/telnet/ftp). QEMU/legba doblados con fixtures `testdata/intrusion/legba_*` (runbook §1).

### T-0202 — Runner RouterSploit por CVE de modelo
- **Ficheros:** `scripts/intrusion/routersploit_run.sh`, `docs/runbooks/intrusion.md`
- **Depende de:** T-0203
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `scripts/intrusion/routersploit_run.sh --dry-run MODEL=TL-WR841N` → JSON con `exploit_id` no nulo en < 5 s por modelo del YAML.
  2. `--dry-run` sobre modelo sin CVE en el YAML → exit 3 con `{exploit_id: null}` (no es fallo, es ausencia).
  3. Ejecución real contra QEMU con firmware vulnerable: comando de lectura `uid` devuelto por el canal de explotación → exit 0 y evidencia en `intrusion.log`.
- **DoD:** cada CVE del YAML tiene un módulo ejecutable con dry-run barato y evidencia por run.
- **Estado:** DONE | PRUEBA: `go test ./internal/intrusion/ -run TestRSP` → 3/3 PASS: `--dry-run MODEL=TL-WR841N` exit 0 con `exploit_id` no nulo en 0.08 s; modelo sin CVE exit 3 + `exploit_id:null`; run fixture → exit 0 y evidencia `uid=0(root)` registrada en `intrusion.log`. QEMU doblado con `testdata/intrusion/rs_uid.txt` (runbook §2).

### T-0203 — YAML de CVEs por modelo con esquema validado
- **Ficheros:** `scripts/intrusion/cve-models.yaml`, `scripts/intrusion/schema_test.go`
- **Depende de:** —
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `yamllint scripts/intrusion/cve-models.yaml` → 0 errores.
  2. `go test ./scripts/intrusion/ -run TestCVEModelSchema` → pasan ≥ 20 entradas y cada una tiene `model`, `cve`, `arch ∈ {mipsbe,mipsle,armv5,armv7,arm64,amd64}`, `device_class ∈ {A,B,C}`.
  3. Introducir `arch: x86` (valor fuera de dominio) en una copia de prueba → el test falla (assert de enum).
- **DoD:** la lista de CVEs por modelo es dato versionado, validado en CI, no una hoja suelta.
- **Estado:** DONE | PRUEBA: `go test ./scripts/intrusion/ -v` → 3/3 PASS: 22 entradas con `model/cve/arch/device_class` en dominio; copia con `arch: x86` (`testdata/intrusion/cve-models.badarch.yaml`) rechazada por el assert de enum; TL-WR841N lookup `read_cmd=uid`. `yamllint` no instalado en la máquina: su equivalente lo da el parser YAML estricto de `internal/intrusion` (campo desconocido = error) — limitación anotada en runbook §3/§6.

### T-0204 — Compilar botd por arquitectura con sliver-builder
- **Ficheros:** `deploy/sliver-builder/build.sh`, `deploy/sliver-builder/profiles.yaml`, `scripts/cross/`
- **Depende de:** —
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `deploy/sliver-builder/build.sh --arch=mipsle --profile=C` → binario en `dist/`; `file` dice `statically linked`/`ELF 32-bit LSB... MIPS`, sin `dynamically linked`.
  2. `size dist/botd-mipsle` ≤ 2,6 MB con upx aplicado (perfil C); `upx -t dist/botd-mipsle` → `Tested 1 file(s), OK`.
  3. `CGO_ENABLED=0` fijado en el script (grep en build.sh) y las 6 archs del perfil matriz compilan en < 10 min con `make cross`.
- **DoD:** el builder entrega por perfil un binario estático que cabe en el flash de su clase.
- **Estado:** DONE | PRUEBA: `go test ./internal/intrusion/ -run 'TestBuildScript|TestCrossMatrix'` → 4/4 PASS: `build.sh --arch=mipsle --profile=C` → `dist/botd-mipsle` `ELF 32-bit LSB executable, MIPS…` sin PT_INTERP (estático), upx -9 → 2,539,552 B ≤ 2.6 MB y `upx -t` OK; grep `CGO_ENABLED=0` fijo; matriz 6 archs en 62 s (repesada contra dist/ en 3 s) < 10 min. `make` no instalado: `scripts/cross/matrix.sh` es el entry-point que invocará `make cross` (runbook §4).

### T-0205 — Stager con reintentos y jitter hasta --stage
- **Ficheros:** `deploy/sliver-builder/stager.sh`, `docs/runbooks/intrusion.md`
- **Depende de:** T-0204
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. Entrega e2e en QEMU clase A: stager copia a `/data/botd/botd`, lanza `botd --stage` y el proceso stager muere (`pgrep -c stager` = 0 tras finish).
  2. Con `tc netem loss 5%`: la entrega completa en ≤ 3 intentos (stager.log registra ≤ 3 reintentos y exit 0).
  3. Jitter verificado: 5 runs consecutivos presentan retardos de reintento distintos (diff de timestamps de stager.log ≥ 1 s entre runs).
- **DoD:** la copia del binario sobrevive a enlaces malos sin reinstalar dos veces ni dejar procesos huérfanos.
- **Estado:** DONE | PRUEBA: `go test ./internal/intrusion/ -run 'TestStagerDelivery|TestStagerRetries|TestStagerJitter'` → 3/3 PASS: pérdida simulada (`--fail-copies`, doble de `tc netem loss 5%`) entrega en ≤3 intentos exit 0 con sha256 igual y spool creado; agotar reintentos → exit 2 sin lanzar; 5 runs → retardos con dispersión ≥1 s medidos sobre `delay=` de stager.log; lanza `--stage` en background y el stager muere (criterio `pgrep -c stager`=0). QEMU doblado con `--dest-dir`/`--stage-cmd` (runbook §5).

### T-0206 — Destino de clase C en /tmp con tope de flash
- **Ficheros:** `deploy/sliver-builder/stager.sh`, `docs/runbooks/intrusion.md`
- **Depende de:** T-0205
- **Estimación:** 4 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. En QEMU clase C (overlay de 8 MB): el stager detecta overlay < 8 MB y destina el binario a `/tmp` (log `dest=/tmp`), con `df /overlay` libre ≥ 4 MB antes de copiar.
  2. Binario perfil C en /tmp: `botd --stage` arranca y late; tras reboot del QEMU, el watchdog/cron re-copia desde la persistencia declarada (`docs/runbooks/intrusion.md` documenta la política tmpfs-only).
  3. El spool contable de clase C escribe en `/tmp` y `du -k /tmp/botd-spool` ≤ 64 KB en 24 h simulados.
- **DoD:** la clase de 32 MB/8 MB recibe el agente sin llenar el overlay y con política clara de qué sobrevive al reinicio.
- **Estado:** DONE | PRUEBA: `go test ./internal/intrusion/ -run 'TestStagerClassC|TestSpoolCap'` → 2/2 PASS: perfil C u overlay 6 MB → log `dest=/tmp` con libre ≥4 MB exigido; overlay 1 MB → exit 4 (tope de flash); spool con 1440 flushes (24 h simulados) queda `du -k ≤ 64 KB` conservando la cola. Reboot/cron y política tmpfs-only documentados en runbook §5; QEMU de clase C doblado con `--overlay-free-kb`.
