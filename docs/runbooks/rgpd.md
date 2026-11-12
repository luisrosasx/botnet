# Runbook — RGPD mínimo (EP-11 T-1103)

Qué dato personal vive, dónde (hash) y cuándo se borra. Cierra A3-X5:
retirarse borra identidad, conserva contabilidad.

## Inventario

| Dato | Dónde vive | Forma | Cuándo se borra / anula |
|---|---|---|---|
| `device_token` | `nodes.device_token_hash` | `char(64)` — SHA-256 en crudo; el token en claro no tiene columna (grep dump = 0) | El hash **no** se borra: es la huérfana que impide doblar el nodo tras `deleted_at` |
| `mac` | `nodes.mac_hash` (`bytea`) | Hash, nunca la MAC en claro | Conservado como hash tras el purge (assert del job) |
| `public_ip` | `nodes.public_ip` (`inet`) | En claro mientras el nodo vive | `NULL` en el purge, 13 meses tras la baja |
| Fechas legales | `nodes.consent_at` (alta, NOT NULL, puerta 428) y `nodes.optout_at` (baja) | `timestamptz` | No se borran: son la prueba de consentimiento |
| Historia de baja | `leaves` (`method`, `leave_code`, `left_at`, `reprobe_blocked_until`) | Sin identificadores personales | Retención propia ≥ 12 meses (bloquea el re-clutaje) |
| Contabilidad | `usage` (diaria por nodo), `sessions` (30 días) | Bytes y coste | `usage` 12 meses, `sessions` 30 días (`jobs/retention.go`); **el purge RGPD no las toca**: borrar persona no borra factura |

## Job

`cmd/botnet-api/jobs/purge.go` (`PurgeJob`): `UPDATE nodes SET
public_ip = NULL, deleted_at = now()` para nodos sin borrar con
`leaves.left_at` más antigua que 13 meses (12 de retención contable +
1 de margen de disputa, calendario real de Postgres, no 13×30 días).
Los hashes y las fechas sobreviven porque el UPDATE ni los nombra.

## Puerta

```bash
DATABASE_URL=postgres://botnet@127.0.0.1:5433/botnet?sslmode=disable make rgpd-check
```

`rgpd-check` siembra un retirado con baja hace 13 meses, corre el job
y audita: `public_ip` NULL, `mac_hash` no nulo,
`device_token_hash` no nulo, `deleted_at` no nulo, la fila `usage`
del retiro presente y 0 columnas de token en claro. Exit 0 =
RGPD mínimo en vigor.

## Límites aceptados

- No hay borrar "derecho al olvido" total: el hash de token es
  seudónimo estable que el blueprint declara colisión asumible
  (§14), no identidad criptográfica.
- La retención contable (12 meses) responde disputas de cobro a 60
  días con holgura (runbook `retention.md`).
