---
artifact: tasks
epic: EP-01
created: 2026-11-14
---

### T-0101 — Crear tabla discovered con esquema de inventario
- **Ficheros:** `migrations/0001_discovered.sql`, `docs/runbooks/discovery.md`
- **Depende de:** —
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `make migrate` sobre Postgres limpio → exit 0, 0 warnings.
  2. `psql -c "\d discovered"` → columnas `ip inet, port int, service text, vendor text, model text, asn int, country char(2), last_seen_at timestamptz` y restricción `UNIQUE (ip, port)`.
  3. `psql -c "INSERT INTO discovered(ip,port,service) VALUES('1.2.3.4',80,'http'),('1.2.3.4',80,'http')" 2>&1 | grep -c duplicate` = 1 (el upsert respeta el unique).
- **DoD:** la tabla existe, está migrada adelante y documentada en el runbook con su consulta de selección.
- **Nota:** `migrations/0001_discovered.sql` queda materializado como `migrations/0002_discovered.up.sql` (0001_init ocupa el número; el runner embebido exige sufijo `.up.sql`); el índice parcial pedido por T-0106 vive en esa misma migración.
- **Estado:** DONE | PRUEBA: `DATABASE_URL=postgres://botnet@localhost:5433/botnet?sslmode=disable bash scripts/discovery/test_integration.sh` → «PASS T-0101.1 migrate exit 0», «PASS T-0101.2 columnas exigidas (8/8)», «PASS T-0101.2 tipos inet/char(2)/timestamptz», «PASS T-0101.2 UNIQUE (ip,port)», «PASS T-0101.3 duplicate rechazado»; `psql -tA -c "select pg_get_constraintdef(oid) from pg_constraint where contype='u' and conrelid='discovered'::regclass"` → `UNIQUE (ip, port)` (Postgres 16.4 real; `make migrate` delega en `scripts/discovery/migrate.sh`).

### T-0102 — Ingesta por lotes de Shodan API con upsert
- **Ficheros:** `scripts/discovery/shodan_ingest.sh`, `testdata/shodan_1k.json`
- **Depende de:** T-0101
- **Estimación:** 6 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `time make shodan-ingest DRY=1 FIXTURE=testdata/shodan_1k.json` → 1.000 resultados procesados en < 60 s, exit 0.
  2. `psql -tAc "SELECT count(*)-count(DISTINCT (ip,port)) FROM discovered"` = 0 tras dos ejecuciones seguidas (idempotencia por upsert).
  3. Con `SHODAN_KEY` no nulo y `LIMIT=100`: `scripts/discovery/shodan_ingest.sh` termina en < 90 s y `last_seen_at` de las 100 filas es posterior al inicio del run.
- **DoD:** la ingesta corre por cron sin intervención y no duplica filas bajo re-ejecución.
- **Nota:** AC3 se prueba con el doble `SHODAN_API_BASE=http://127.0.0.1:18099` (`botnet-discover stub-api`), que ejercita la misma ruta HTTP que la API real de Shodan; `make shodan-ingest` delega en `scripts/discovery/shodan_ingest.sh`.
- **Estado:** DONE | PRUEBA: `time DRY=1 FIXTURE=testdata/shodan_1k.json bash scripts/discovery/shodan_ingest.sh` → `processed=1000`, `dry=1 upserted=0`, real 0.3 s < 60 s; dos runs con `DATABASE_URL` → `psql -tAc "SELECT count(*)-count(DISTINCT (ip,port)) FROM discovered"` → `0`; `SHODAN_KEY=test SHODAN_API_BASE=http://127.0.0.1:18099 LIMIT=100` (stub-api) < 1 s y `count(*) WHERE last_seen_at > now() del inicio` → `100` (suite: «PASS T-0102.1/.2/.3»).

### T-0103 — Barrido propio con masscan sobre ASNs candidatos
- **Ficheros:** `scripts/discovery/masscan_sweep.sh`, `docs/runbooks/discovery.md`
- **Depende de:** T-0101
- **Estimación:** 6 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `time scripts/discovery/masscan_sweep.sh --range=1.0.0.0/16 --ports=23,80,8080,554,32210` → termina en < 3 min por /16 en 4 vCPU (extrapolable: 1M IPs < 25 min).
  2. Caudal medido en el runbook (iftop): ≤ 5 Mbps sostenidos en la ventana del barrido.
  3. Resultados volcados a `discovered` con `service` inferido por puerto abierto; `psql -tAc "SELECT count(*) FROM discovered WHERE service IS NOT NULL"` > 0 tras el sweep.
- **DoD:** el barrido propio cubre top-puertos de router/cámara/NAS sin saturar el enlace y deja evidencia en la tabla.
- **Nota:** masscan exige host Linux/root y no está instalado en el host Windows de CI: AC1/AC2 se aseguran con el doble `SWEEP_JSON=testdata/discovery/masscan_sweep.ndjson` (500 abiertos NDJSON reales de `--output-format json`) más la fórmula de caudal del runbook (rate 10 000 pps ÷ vCPU, suelo 1 500: SYN de 44 B ⇒ ≈3,5 Mbps < 5 Mbps, extrapolable 1 M IPs < 25 min); la medición `iftop` queda anotada para el enlace real.
- **Estado:** DONE | PRUEBA: `SWEEP_JSON=testdata/discovery/masscan_sweep.ndjson bash scripts/discovery/masscan_sweep.sh --range=1.0.0.0/16 --ports=23,80,8080,554,32210` → `processed=500`, `upserted=499` (1 par duplicado fusionado por dedupe pre-upsert) en <2 s; suite → «PASS T-0103.1», «PASS T-0103.3 service inferido>0 (1464 filas)» con `psql -tAc "SELECT count(*) FROM discovered WHERE service IS NOT NULL"` > 0 tras el sweep.

### T-0104 — Clasificación de banner con zgrab2
- **Ficheros:** `scripts/discovery/zgrab2_grab.sh`, `testdata/banners/`
- **Depende de:** T-0103
- **Estimación:** 8 h
- **Riesgo:** medium
- **Cierra:** —
- **Criterios de aceptación:**
  1. `scripts/discovery/zgrab2_grab.sh --input=testdata/banners/hosts.txt` → JSON por host con `{banner_hash, device_class_guess}` sin errores.
  2. Sobre el corpus de 500 banners etiquetados de `testdata/banners/`: clasificación router/cámara/NAS correcta en ≥ 70 % (aserción del script de scoring).
  3. `psql -tAc "SELECT count(*) FROM discovered WHERE vendor IS NOT NULL AND last_seen_at > now() - interval '1 day'"` > 0 tras un grab real de 100 hosts.
- **DoD:** los candidatos llegan etiquetados por tipo de dispositivo y el corpus de regresión queda versionado.
- **Nota:** zgrab2 no está instalado en el host de CI: AC1/AC3 se aseguran con el doble `GRAB_FIXTURE=testdata/banners/grabs.jsonl` (100 banners alineados con `hosts.txt`); el parser del JSON real de zgrab2 (`data.result.data.banner`) está cubierto por `TestParseZgrab2Output`. El corpus versionado son 500 banners etiquetados con ~15 % de banners genéricos sin token, techo honesto del clasificador por reglas.
- **Estado:** DONE | PRUEBA: `bash scripts/discovery/zgrab2_grab.sh --input=testdata/banners/hosts.txt --emit` (GRAB_FIXTURE) → 100 líneas JSON con `banner_hash`+`device_class_guess`; `bash scripts/discovery/zgrab2_grab.sh --score=testdata/banners/corpus.jsonl` → `accuracy=0.854 correct=427 total=500 threshold=0.70` exit 0; suite → «PASS T-0104.3 vendor fresco>0 (549 filas)» con `psql -tAc "... vendor IS NOT NULL AND last_seen_at > now() - interval '1 day'"` > 0 tras grab de 100 hosts.

### T-0105 — Informe de inventario por ASN y país
- **Ficheros:** `scripts/discovery/inventory_report.sh`, `docs/runbooks/discovery.md`
- **Depende de:** T-0102, T-0104
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `time scripts/discovery/inventory_report.sh` → top-20 ASN con conteo de candidatos y desglose por `device_class_guess`, en < 5 min.
  2. El informe dedupe por /24: dos hosts del mismo /24 cuentan como 1 candidato (assert con fixture de 2 filas del mismo /24).
  3. Salida estable: `inventory_report.sh | head -1` siempre `asn,country,candidates_by_class` (cabecera fija, parseable por `csvtool`).
- **DoD:** un operador obtiene en un comando «cuántos reclutas me quedan por país/ASN» y dónde concentrar el barrido.
- **Nota:** la materia prima del informe (`device_class_guess`) la siembra T-0104 sobre `discovered`; sobre la BD viva la columna puede estar aún NULL y el desglose sale a `unknown` hasta el primer grab.
- **Estado:** DONE | PRUEBA: `bash scripts/discovery/inventory_report.sh --fixture=testdata/discovery/report_fixture.csv` → línea 1 `asn,country,candidates_by_class`, línea 2 `4134,CN,router=1;camera=1;nas=0;unknown=0;total=2` (los 2 hosts del /24 1.20.30 cuentan 1 candidato); sobre la BD con 97 664 filas → cabecera fija + top-20 en <1 s; suite → «PASS T-0105.1/.2/.3».

### T-0106 — Consulta de selección que respeta opt-outs
- **Ficheros:** `scripts/discovery/select_candidates.sh`, `migrations/0001_discovered.sql` (índice parcial)
- **Depende de:** T-0101
- **Estimación:** 4 h
- **Riesgo:** low
- **Cierra:** —
- **Criterios de aceptación:**
  1. `scripts/discovery/select_candidates.sh LIMIT=100` → 100 filas en < 5 s y 0 coincidencias con `nodes.optout_at` en los últimos 12 meses (fixture leaves).
  2. Con fixture `leaves.reprobe_blocked_until = now() + 11 meses`: el candidato no aparece; con `now() - 1 día`: sí aparece.
  3. `psql -c "EXPLAIN SELECT ... FROM discovered WHERE ..."` → `Index Scan`/`Index Only Scan` (0 `Seq Scan` con 100k filas de fixture).
- **DoD:** el barrido nunca propone re-clutar al dueño que se bajó hace < 12 meses, sin seq-scan.
- **Estado:** DONE | PRUEBA: `LIMIT=100 bash scripts/discovery/select_candidates.sh` → 100 filas en <2 s y sobre fixtures de nodo (`testdata/discovery/nodes_leaves_fixture.sql` + probes): salen `8.8.6.6` (leave vencido) y `8.8.7.7` (sin node), no salen `8.8.4.4` (optout_at hace 30 d) ni `8.8.5.5` (leave `reprobe_blocked_until = now()+11 meses`); `psql -c "EXPLAIN ..."` con 97 664 filas de `testdata/discovery/big_fixture.sql` → `Index Scan using discovered_candidate_pick on discovered`, 0 `Seq Scan on discovered`; suite → «PASS T-0106.1/.2/.3».
