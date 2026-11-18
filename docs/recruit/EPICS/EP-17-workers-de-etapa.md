---
artifact: epic
id: EP-17
title: Workers de etapa envueltos — SYN, banner, exclusión, credencial y entrega
phase: F3
risk: high
estimate_days: 6
closes: A1-02, A1-09, A1-12, A1-13, A2-14, injertos V2-3, V3-4
artifacts:
  - cmd/botnet-probe/main.go
  - cmd/botnet-strike/main.go
  - scripts/recruit/exclusions.csv
  - testdata/recruit/exclusions.csv
  - testdata/recruit/masscan.ndjson
  - scripts/field/bootstrap.sh
---

## Objetivo

Repartir las cinco etapas del embudo entre tres procesos **stateless** que no inventan nada: el barrido y el banner reutilizan `internal/discovery` (masscan NDJSON, `SweepRate`, `ParseZgrab2Output`, `Classify`), la credencial y la entrega reutilizan `internal/intrusion` (legba, CVE, `stager.go`) y `scripts/field/bootstrap.sh`. Cada worker reclama un trabajo con lease, ejecuta un subproceso, escribe su `outcome` y muere; si el orquestador no está, reclama directo y emite a `pps_min`.

## Alcance

- **`cmd/botnet-probe/main.go` (nuevo)** — etapa `sweep`: invoca masscan con `--source-ip 74.208.94.91 --adapter eth0`, `--min-rate`/`--max-rate` de la ventana activa, `nice -n 10` e `ionice -c3`, parsea NDJSON con `discovery.ParseMasscanNDJSON` y deja `sweeping → bannered` o `discarded/no_answer`. Cohortes de 60 s (90.000 SYN a 1500 pps) para que el lazo no gobierne sobre una cohorte inacabada (A2-14).
- **Banner (etapa `banner`)** — zgrab2 sobre los puertos del candidato; `discovery.ParseZgrab2Output` + `discovery.Classify` fijan `device_class` (A/B/C), `model_guess` y `asn`/`country`; sin banner ⇒ `discarded/no_banner`.
- **Exclusión doble (etapa `assessed` y al nacer)** — carga `scripts/recruit/exclusions.csv` en `recruit_exclusions` (fixture `testdata/recruit/exclusions.csv`) con la regex de PTR `(?i)\b(bank|banco|credit|gov|gob|muni|mil|edu|univ|hospital|clinic|salud|health)\b`, ASN de banco/gobierno/sanidad, operadores de escaneo y CERTs, el propio /24 y ASN de origen, dos `country` de baja densidad y categoría `honeypot` con muerte tipada (A1-12). Se comprueba **al nacer** el candidato y **antes de `assessed`**.
- **Credencial (etapa `cred`)** — `botnet-strike` por candidato con `internal/intrusion/legba.go` sobre los 4 protocolos (`http`, `ssh`, `telnet`, `ftp`); escribe `cred_protocol` y `cred_ref` (el secreto **nunca** se persiste, A1-02); 3 fallos en < 90 s ⇒ `burned`; sin credencial ⇒ `discarded/no_cred`; tope de 3 intentos por IP de destino y hora compartido con `deliver` (A1-13).
- **Entrega (etapa `deliver`)** — `internal/intrusion/stager.go` decide el destino por clase y flash; `scripts/field/bootstrap.sh --arch <a> --inject-key` inyecta `authorized_keys` y, si solo hay contraseña, `sshpass` como fallback; sin SSH ⇒ `discarded/no_delivery` (A1-02); flash < 4 MB ⇒ `discarded/flash_cap`; `bootstrap.sh --uninstall` idempotente invocado en `rolled_back`/`retired` (A1-09).
- **Modo fixture en CI** — `--sweep-json testdata/recruit/masscan.ndjson` y `--banner-json` dan la etapa sin tocar la red `[injerto: V3]`; `RECRUIT_PPS_FIXED=<n>` congela el lazo para que el fixture sea determinista.

## Fuera de alcance

- El SOCKS5 y su verificación por nodo son EP-18; aquí el worker solo prepara al candidato.
- La decisión de ritmo (cuántos SYN, qué hora) es EP-16; el worker **obedece** la ventana y no se auto-regula.
- `nodes.public_ip` (EP-19) y el paraguas (EP-20): el worker escribe su `outcome` y `recruit_candidate_events`, nada más.
- No se añade un cuarto runner de intrusión: RouterSploit/CVE se consumen por lookup (`botnet-strike cve-lookup`), no por ejecución.

## Dependencias

- **EP-16**: `POST /v1/work/claim` / `complete`, lease por fase con fencing y `age_clock_at`; sin EP-16 los workers no tienen cola.
- **EP-15**: las tablas `recruit_candidates`, `recruit_jobs`, `recruit_exclusions` con sus claves de idempotencia.
- Desbloquea EP-20 (las métricas `state_rows{state}` y `queue_depth{stage}` salen de estas etapas) y EP-21 (el embudo necesita `enrolled` para producir los 20 nodos).
- Coincide en el tiempo con EP-18: el binario de clase A con el tag y el frente del gw se construyen en rama aparte.

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| masscan sin `--source-ip`/`--adapter` en un VPS con Docker: SYN con IP o pasarela equivocadas y RPF descartando el SYN-ACK (A1-14) | alta | alto | flags explícitos + spike que aserte al menos un SYN-ACK contra un host abierto conocido |
| Un masscan huérfano sigue emitiendo pps extra tras morir el worker (A2-04) | media | alto | `KillMode=control-group` + `TimeoutStopSec=20` (EP-19) y lease de 30 s con reclaim (EP-16) |
| `install` rinde ~0 sobre la población seleccionada por credencial por defecto (A1-02) | alta | alto | ruta `authorized_keys` primero y `sshpass` como fallback, elegido por `cred_protocol` |
| El tick de 60 s gobierna un barrido de 216 s y la bajada llega 2-3 ticks tarde (A2-14) | media | medio | cohortes de 60 s con `--max-rate` recalculado sin reiniciar el barrido |
| Legba quema el router con 6 logins en un día (A1-13) | media | medio | contador por IP destino y hora compartido entre `cred` y `deliver` |

## Criterio de salida de la épica

En modo fixture (`--sweep-json testdata/recruit/masscan.ndjson`, `RECRUIT_PPS_FIXED=1500`) un lote de 200 candidatos recorre `new → bannered → assessed` con `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state IN ('bannered','assessed')"` ≥ **180**, `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='assessed' AND cred_protocol IS NULL"` = **0** entre los que tienen credencial, y `psql -tAc "SELECT count(*) FROM recruit_candidate_events WHERE actor LIKE 'probe:%' OR actor LIKE 'strike:%'"` ≥ **200** con 0 red real (0 paquetes SYN en `tcpdump -c 100`).

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-1701 | `botnet-probe`: worker de barrido SYN con coorte de 60 s | 8 h |
| T-1702 | Banner con zgrab2: `device_class`, `model_guess` y `country` | 6 h |
| T-1703 | Exclusión doble desde `exclusions.csv` con categoría `honeypot` | 6 h |
| T-1704 | Credencial con legba: `cred_protocol`, `cred_ref`, `burned` y tope por destino | 10 h |
| T-1705 | Entrega: `authorized_keys`, fallback `sshpass`, `no_delivery` y `--uninstall` | 10 h |
| T-1706 | Modo fixture NDJSON determinista en CI con lazo congelado | 6 h |
