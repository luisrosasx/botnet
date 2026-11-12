---
artifact: runbook
epic: EP-01
created: 2026-11-14
---

# Runbook — Descubrimiento e inventario (EP-01)

Tabla `discovered` (migracion `migrations/0002_discovered.up.sql`, el
`0001_discovered.sql` de la TASK se numera 0002 porque el suelo ocupa
0001 y el runner embebido exige sufijo `.up.sql`):

| Columna | Notas |
|---|---|
| `ip inet`, `port smallint` | `UNIQUE (ip, port)`: toda ingesta es upsert |
| `service text` | inferido por puerto (`InferService`); NULL si el puerto no esta curado |
| `vendor`, `model` | Shodan `device.*` o clasificador de banners |
| `asn int`, `country char(2)` | GeoIP2/ASN del lado servidor |
| `device_class_guess`, `banner_hash`, `source` | salida de zgrab2/clasificador; hash = sha256 del banner normalizado |
| `last_seen_at timestamptz` | lo renueva cada upsert |

## Pre-requisitos

- `DATABASE_URL=postgres://user:pass@host:5432/botnet?sslmode=disable`
- `psql` en PATH para las comprobaciones de operador (opcional para los
  scripts: el binario `cmd/botnet-discover` lleva el acceso SQL).
- `masscan` y `zgrab2` son binarios de host de barrido (root el
  primero); sin ellos los scripts corren con dobles fixture
  (`SWEEP_JSON=`, `GRAB_FIXTURE=`).

## migrate (T-0101)

```bash
DATABASE_URL=... bash scripts/discovery/migrate.sh   # equivale a `make migrate`
```

El target `make migrate` del Makefile delega en este script cuando el
orquestador lo añada. Comprobacion: `psql "$DATABASE_URL" -c "\d discovered"`
debe mostrar las columnas del blueprint y `discovered_ip_port_key UNIQUE,
UNIQUE (ip, port)`.

### Consulta de seleccion (DoD T-0101)

```sql
SELECT ip, port, service, asn, country
FROM discovered
WHERE service IS NOT NULL
  AND last_seen_at > now() - interval '30 days'
ORDER BY last_seen_at DESC
LIMIT 100;
```

Usa el indice parcial `discovered_candidate_pick (last_seen_at DESC)
WHERE service IS NOT NULL` (bloqueado por opt-outs en T-0106 via
anti-join a `nodes`/`leaves`, que publica EP-08; mientras no existan,
`select_candidates.sh` corre el equivalente sin anti-join).

## Ingesta Shodan (T-0102)

```bash
# doble determinista (CI): 1000 resultados en ~1 s
time DRY=1 FIXTURE=testdata/shodan_1k.json bash scripts/discovery/shodan_ingest.sh
# real: API Shodan con clave; LIMIT=100 por defecto
SHODAN_KEY=*** LIMIT=100 DATABASE_URL=... bash scripts/discovery/shodan_ingest.sh
```

- El upsert `ON CONFLICT (ip, port)` es idempotente: re-ejecutar no
  duplica filas y solo renueva `last_seen_at` (y refina columnas con
  `COALESCE`).
- Cron sugerido: `0 3 * * * SHODAN_KEY=*** DATABASE_URL=... scripts/discovery/shodan_ingest.sh`.
- Doble de API sin red: `go run ./cmd/botnet-discover stub-api
  --fixture=testdata/shodan_1k.json --addr=127.0.0.1:18099` y
  `SHODAN_API_BASE=http://127.0.0.1:18099`.

## Barrido propio (T-0103)

```bash
time bash scripts/discovery/masscan_sweep.sh --range=1.0.0.0/16 --ports=23,80,8080,554,32210
# sin masscan instalado:
SWEEP_JSON=testdata/discovery/masscan_sweep.ndjson bash scripts/discovery/masscan_sweep.sh --range=1.0.0.0/16 --ports=23,80,8080,554,32210
```

Caudal (BRIEF-BASE punto 6: enlace ajeno, tope 5 Mbps): el `--rate` por
defecto es `10 000 pps / vCPU` con suelo 1 500 pps. Un SYN ocupa ~44 B
en el cable, luego 10 000 pps ~= 3,5 Mbps < 5 Mbps; la ventana responde
en subsegmentos /24 y el tiempo por /16 con masscan real queda por
debajo de 3 min (extrapolable: 1 M IPs < 25 min a 4 vCPU). Medicion
`iftop` en la ventana del barrido: obligatorio en produccion, sin HW en
CI (qued63 anotado como medicion de operador).

## Clasificacion de banners (T-0104)

```bash
bash scripts/discovery/zgrab2_grab.sh --input=testdata/banners/hosts.txt --emit
bash scripts/discovery/zgrab2_grab.sh --score=testdata/banners/corpus.jsonl   # asercion >= 70 %
```

- `GRAB_FIXTURE=testdata/banners/grabs.jsonl` es el doble de zgrab2;
  con `ZGRAB2_BIN=/ruta/zgrab2` corre el binario real y se parsea su
  JSON anidado (`data.result.data.banner`).
- El corpus versionado son 500 banners etiquetados (190 router / 190
  camara / 120 NAS, ~15 % banners genericos sin token); el score actual
  es 0.854 con umbral 0.70. `banner_hash` = sha256 del banner
  normalizado: estable ante CRLF/espacios.

## Informe (T-0105)

```bash
bash scripts/discovery/inventory_report.sh                       # sobre discovered
bash scripts/discovery/inventory_report.sh --fixture=testdata/discovery/report_fixture.csv
```

Salida CSV con cabecera fija `asn,country,candidates_by_class`; el
desglose es un campo `router=N;camera=N;nas=N;unknown=N;total=N`
(dentro de una /24 manda la clase mayoritaria; dos hosts de la misma
/24 cuentan 1 candidato). Top-20 por candidatos, orden determinista.

## Seleccion con opt-outs (T-0106)

```bash
LIMIT=100 bash scripts/discovery/select_candidates.sh
```

Excluye IPs cuyo `nodes.optout_at` cae en los ultimos 12 meses y IPs
con `leaves.reprobe_blocked_until` futuro (BLUEPRINT §13: 12 meses de
bloqueo de re-clutaje). Con 100k filas de fixture el `EXPLAIN` muestra
`Index Scan using discovered_candidate_pick` y cero `Seq Scan` sobre
`discovered` (`testdata/discovery/big_fixture.sql`).

## Pruebas

- Unidad (sin BD): `go test ./internal/discovery/...` — parser Shodan,
  masscan NDJSON, corpus >= 70 %, informe/dedupe, SQL del upsert y de
  la seleccion.
- Integracion (Postgres real): `DATABASE_URL=... bash
  scripts/discovery/test_integration.sh` → 22 aserciones PASS/FAIL, una
  por criterio de aceptacion de las 6 tasks.
- Fixings versionados en `testdata/` (regenerables con
  `go run ./internal/discovery/gen`).
