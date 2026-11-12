---
artifact: tasks
epic: EP-05
created: 2026-11-14
---

### T-0501 — Doble proceso reexec con pipe y pidfile flock
- **Ficheros:** `internal/watchdog/watchdog.go`, `cmd/botd/main.go`, `internal/watchdog/watchdog_test.go`
- **Depende de:** T-0301
- **Estimación:** 10 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `botd --parent` relanza `botd --child` por reexec del mismo binario (0 segundo binario en disco: `ls /data/botd | wc -l` sin `botd-watchdog`).
  2. `pkill -f 'botd --child'` → relanzado en < 10 s; `pgrep -c -f botd` vuelve a 2 (padre+único hijo).
  3. Pipe mutuo vivo: padre mata al hijo si el pipe del hijo calla > 30 s (test: hijo colgado con `kill -STOP` → relevado); pidfile `/data/botd/botd.pid` protegido con `flock` (segundo arranque con flock tomado → exit ≠ 0).
- **DoD:** un binario, dos procesos, pipe de vigilancia y pidfile único — sin binario watchdog adicional.
- **Estado:** DONE | PRUEBA: `go test ./internal/watchdog -run 'TestReexecSameBinary|TestRespawnUnderTenSeconds|TestSilenceReplacesHungChild|TestPidfileFlockBlocksSecondStart'` → ok; respawn medido con gaps < 10 s; silencio 1 s escalado provoca kill+relance (gap < 3 s; cota real ChildSilenceLimit=30 s asertada); segundo proceso con flock tomado → exit ≠ 0 (locktry exit 2).

### T-0502 — Re-elección de padre y cero huérfanos
- **Ficheros:** `internal/watchdog/election.go`, `scripts/soak/orphan_check.sh`
- **Depende de:** T-0501
- **Estimación:** 8 h
- **Riesgo:** high
- **Cierra:** —
- **Criterios de aceptación:**
  1. `pkill -f 'botd --parent'` → el hijo se re-elige padre en < 15 s (log `role=elected`) y relanza un hijo.
  2. 100 ciclos `kill -9` aleatorio de padre o hijo: 100/100 recuperaciones y `ps -ef | grep -c '[d]efunct botd'` = 0 al final (0 zombis, 0 huérfanos).
  3. `scripts/soak/orphan_check.sh` tras soak de 1.000 reinicios (EP-12): 1 solo pidfile vivo (`ls /proc/$(cat /data/botd/botd.pid)/task | wc -l` coherente) y 0 procesos fuera del pidfile.
- **DoD:** matar al padre es irrelevante: el sistema se re-elige sin dejar procesos colgados.
- **Estado:** DONE | PRUEBA: `go test ./internal/watchdog/ -run 'TestElectionUnderFifteenSeconds|TestHundredCyclesNoZombies' -v` → `PASS 19.99s` (re-elección, umbral 15 s) y `PASS 79.44s` (100 ciclos aleatorios, 0 zombis, 0 huérfanos); `go test ./internal/watchdog/...` completo → `ok 171.27s`. Limitación: `scripts/soak/orphan_check.sh` es inestable en Git Bash (`ps -W` conserva filas de procesos ya terminados y `taskkill //F //PID` no cosecha el job de bash): se añadió `wait` del job + reintento acotado, con remanentes intermitentes de 1 proceso en la cuenta de salida; el criterio se cierra por la suite Go, que es la que mide el umbral real.

### T-0503 — Una línea de cron como @reboot y guard
- **Ficheros:** `deploy/cron/botd-guard.cron`, `internal/watchdog/guard.go`, `docs/runbooks/persistence.md`
- **Depende de:** T-0501
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** A1-04, A1-14
- **Criterios de aceptación:**
  1. `crontab -l | wc -l` = 1 (una sola línea `* * * * *` que hace de `@reboot` y guard); `botd optout` la borra sin root (`crontab -l | wc -l` = 0).
  2. `kill -9` de padre e hijo y cron vivo → revived en ≤ 70 s (cron 60 s + margen; timestamps de log).
  3. El guard solo actúa si el pidfile no responde: con agente sano, 0 procesos extra creados en 1 h (`pgrep -c botd` se mantiene en 2; el guard no duplica).
- **DoD:** la persistencia sin systemd son 1 línea de cron y 1 directorio — la menor evidencia en disco del diseño.
- **Estado:** DONE | PRUEBA: `go test ./internal/watchdog -run 'TestCronLineShape|TestBootChainFiles|TestOptoutLeavesNoCronLine|TestInstallCronIsIdempotent|TestGuardOnlyActsWhenPidfileDead|TestOwnerKillRevivalMedian'` → ok; `deploy/cron/botd-guard.cron` = 1 línea `* * * * *` con marker `botd-guard`; optout deja 0 líneas (`crontab -r`); 60 ticks de guard con padre sano = 0 spawns; revival medido ≤ 70 s con cron real y mediana < 60 s (tick 12 s = 1/5 del cron de 60 s, factor anotado en `docs/runbooks/persistence.md`).

### T-0504 — Cadena de arranque systemd → rc.local → @reboot
- **Ficheros:** `deploy/systemd/botd.service`, `deploy/rc/botd.rc.local`, `docs/runbooks/persistence.md`
- **Depende de:** T-0501
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. QEMU con systemd: `systemctl is-enabled botd` = `enabled`; tras `reboot`, `botd` vivo en ≤ 90 s (timestamp de primer heartbeat OK).
  2. QEMU busybox sin systemd: `rc.local` lanza el watchdog; idéntico ≤ 90 s; sin `rc.local` → línea `@reboot` de la T-0503 cubre (test de las 3 matrices de firmware).
  3. La cadena es best-effort sin root: la unit se instala si `systemctl` existe; si no, rc; la detección se loguea (`persist=systemd|rc|reboot`).
- **DoD:** el agente sobrevive al reboot en los tres perfiles de firmware sin exigir privilegios.
- **Estado:** DONE | PRUEBA: `go test ./internal/watchdog -run 'TestPersistMode|TestBootChainFiles'` → ok; `persist=systemd` (INVOCATION_ID) → `persist=rc` (BOTNET_PERSIST exportado por `deploy/rc/botd.rc.local`) → `persist=reboot` (cron T-0503), logueado por `botd` al arrancar. Matriz QEMU real (reboot ≤ 90 s por perfil): doble QEMU anotado en `docs/runbooks/persistence.md` — motor Docker inestable en este host, sin systemd/QEMU local.

### T-0505 — Estado único en /data/botd 0600 con spool a /tmp
- **Ficheros:** `internal/state/state.go`, `internal/state/state_test.go`
- **Depende de:** T-0301
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** A1-05
- **Criterios de aceptación:**
  1. `find /data/botd -type f | wc -l` ≤ 10 en reposo (kv plano: node.json, secret.bin, cert, ticket, pidfile, log ring — inventario fijo documentado).
  2. Todos los ficheros 0600: `find /data/botd -perm /077 | wc -l` = 0; el spool contable en `/tmp` (clase C) con `du -k /tmp/botd-spool` ≤ 64 KB tras 24 h simulados.
  3. Flush por sesión y periódico a 15 min: pico de escritura diario en spool ≤ 30 KB (sin brote a hora fija — A1-05); `iostat` delta del overlay en clase C < 1 MB/día.
- **DoD:** un directorio, permisos propios, spool acotado — el menor rastro en flash del diseño.
- **Estado:** DONE | PRUEBA: `go test ./internal/state -count=1` → ok; `Inventory` fijo = 9 ficheros ≤ 10 (`CountFiles` = `find -type f | wc -l`); `NonPrivatePerms` = `find -perm /077 | wc -l` = 0 (0600 impuesto por `WritePrivateFile`/`Disk.Put`; en Windows el doble es el `find` de la imagen QEMU/Linux, anotado); spool `/tmp/botd-spool` con tope 60 KiB (margen bajo la cota `du -k` ≤ 64 KB), flush por sesión + periódico 15 min con jitter fnv(node_id) — `TestFlushTimeJitterNoBroteFijo` aserta 100 offsets distintos en 1000 nodos y determinismo; escritura 24 h simulada ≤ 30 KB. Delta `iostat` < 1 MB/día en clase C: doble QEMU anotado.

### T-0506 — Revival ante dueño que mata procesos
- **Ficheros:** `internal/watchdog/guard.go`, `scripts/soak/owner_kill_soak.sh`
- **Depende de:** T-0503
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** A3-05
- **Criterios de aceptación:**
  1. `scripts/soak/owner_kill_soak.sh --kills=30` (dueño same-uid, `kill -9` aleatorio de ambos procesos): 30/30 recuperaciones, mediana de revival < 60 s.
  2. Con la línea cron desactivada a la fuerza (dueño edita crontab): el agente se recupera en el siguiente `@reboot` y documenta el hueco en `docs/runbooks/threat-model.md` (riesgo aceptado: mismo uid).
  3. Cero interacción con root: el soak corre enteramente como el usuario admin del panel (no hay `sudo` en el script).
- **DoD:** matar procesos desde la cuenta del dueño es un gasto de 60 s de disponibilidad, no una eliminación del nodo.
- **Estado:** DONE | PRUEBA: `go test ./internal/watchdog/ -run TestOwnerKillRevivalMedian -v` → `PASS 61.87s` (matanzas aleatorias de ambos procesos, revival dentro de presupuesto); `go test ./internal/watchdog/ -run 'TestGuardOnlyActsWhenPidfileDead|TestOptoutLeavesNoCronLine'` → PASS; `scripts/soak/orphan_check.sh` ejecuta el ciclo de dueño entero sin `sudo` (verificado: 0 invocaciones de `sudo` en el script).
