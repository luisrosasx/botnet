---
artifact: epic
id: EP-21
title: Puerta de cierre — cinco oleadas, veinte nodos reales, evidencia y runbook
phase: F5
risk: high
estimate_days: 6
closes: A3-05, A3-14, A3-17, objetivo de 20 nodos
artifacts:
  - docs/state/RECRUIT.md
  - docs/runbooks/recruit.md
  - docs/verification/EP-15.md
  - docs/verification/evidence/
---

## Objetivo

Cerrar el piloto de reclutamiento con números, no con actas: **cinco oleadas cerradas** y **veinte aparatos distintos** verificados con SOCKS5 real **a la vez**, con la evidencia fechada y recomponible, y con un runbook que permita repetir el cierre sin un humano recordando el orden. Es la épica que no se puede paralelizar: pide flota enrolada y tres ventanas limpias para la primera subida de marcha.

## Alcance

- **Oleadas medibles en tabla propia (A3-05)** — `botnet-recruit wave close` escribe en `recruit_waves` (`state='cerrada'`, `promoted`, `enrolled`, `verified`, `closed_at`, `note`); la bitácora `docs/state/RECRUIT.md` es espejo de esa tabla, no la prueba (A3-17: hoy ningún gate la cubre).
- **Definición operativa de `verified` (blueprint §5)** — las cinco condiciones simultáneas en el instante de medir: fila en `nodes` con `status IN ('enrolled','live')` y `optout_at IS NULL`; `verified_at` dentro de la oleada que cierra; exactamente una fila ganadora en `recruit_socks5_checks` con `ok=true`, `http_status=200`, `bytes_in>0`, `latency_ms` entre 0 y 1500 e `ip_match=true`; latido en `node_heartbeats` con `ts > now() - interval '15 min'`; y aparato de **clase A**.
- **Veinte nodos, no veinte filas (A3-01, A3-02)** — el conteo es `count(DISTINCT node_id)` con `JOIN nodes` y `node_id IS NOT NULL`; no cuentan dos `(ip,port)` del mismo aparato, ni el mismo `node_id` con tres intentos ganadores, ni un `verified` huérfano, ni un 200 cuya IP de salida sea la del VPS (74.208.94.91).
- **Calendario anclado a dos hitos fechados (A3-14)** — `primer SYN real` y `primer nodo verificado con ip_match=1` con fecha en la bitácora: los días del plan se atan a ellos y el presupuesto deja de ser intocable.
- **Evidencia recomponible (A3-09)** — por oleada, un fichero fechado en `docs/verification/evidence/` con su `sha256`; y un fichero `docs/verification/EP-XX.md` por épica nueva con **una fila PASS por AC** (patrón anclado de G1) y su celda de evidencia no vacía (G6).
- **Runbook `docs/runbooks/recruit.md`** — arranque, pausa (`POST /v1/control/pause`), rollback por escalones, revoque (`scripts/field/revoke_mass.sh`), `bootstrap.sh --uninstall`, arranque en frío con la cola al máximo, y la tabla de límites reales con su forma de comprobarlos.
- **Cierre con el paraguas** — `bash scripts/check-recruit.sh` en **0**, con las 5 condiciones del objetivo verdes y los 6 sabotajes del paraguas en rojo (cero falsos verdes); `go test ./...` con cero `FAIL` y cero `--- SKIP` en `./migrations/`.

## Fuera de alcance

- No se introduce maquinaria nueva: el cierre usa exactamente lo construido en EP-15..EP-20. Si falta una pieza, es un defecto de esas épicas, no trabajo nuevo de EP-21.
- No se amplía la población objetivo (20 aparatos, no 25) ni se baja ningún umbral para cerrar; un B/C enrolado extra cuenta como `deferred` con `clase_bc_sin_socks5`, no como fracaso.
- No se ejecuta el plan completo del blueprint (5 oleadas del piloto, no la operación permanente a 100k nodos).
- El panel y la RGPD de terceros (`consent_source='recruit_auto'`) se registran pero no se amplían aquí.

## Dependencias

- **EP-20** (el paraguas y sus sabotajes) y **EP-19** (el servicio desplegado y `nodes.public_ip`) — son condición de entrada; sin ambas no hay medición posible.
- **EP-18** (el `verify` real con `ip_match`) y **EP-17** (el embudo que produce `enrolled`): sin `enrolled` no hay 20 `verified`.
- Nada depende de EP-21 dentro del plan de reclutamiento: es la puerta.

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| Yield del embudo por debajo de lo previsto (ratio real 1:20) — riesgo 4 del blueprint §12 | alta | crítico | `>= 20 verified` al cierre de la oleada 5; con `< 10 enrolled` al final de la 3 se ensancha la ventana nocturna y **no** se sube `rate_class` |
| 5 oleadas no caben en el calendario con 3 ventanas limpias para la subida | media | alto | calendario anclado a los dos hitos fechados; la subida exige 3 ventanas y nunca dos en menos de 6 |
| Un `verified` huérfano o un doble de laboratorio infla el conteo | media | alto | `count(DISTINCT node_id)` + `JOIN nodes` + exclusión de la IP del VPS (T-2102) |
| La bitácora se rellena a mano y diverge de la tabla (A3-05) | media | medio | la comprobación `oleadas` lee `recruit_waves`; el `awk` de la bitácora es espejo y su gate es aparte (A3-17) |
| Un sabotaje del paraguas deja de morder justo en el cierre | media | alto | los 6 sabotajes se ejecutan como parte de la puerta, con reversión medida (T-2105) |
| Churn: los 20 se van y el conteo sigue en 20 (A3-04) | media | alto | latido `< 15 min` en el instante de medir + `node_leave_ratio_24h <= 0,05` |

## Criterio de salida de la épica

`bash scripts/check-recruit.sh; echo exit=$?` = **0** con `comprobaciones=8`; `psql -tAc "SELECT count(*) FROM recruit_waves WHERE state='cerrada' AND promoted>0 AND verified>=1"` = **5**; el conteo de nodos del paraguas da **≥ 20** `count(DISTINCT node_id)` con latido `< 15 min`, `ip_match` en 20/20 y `device_class='A'` en los 20; los 6 sabotajes devuelven salida **≥ 1** cada uno y, tras revertirlos, el paraguas vuelve a **0**; y `docs/runbooks/recruit.md` permite repetir el cierre comando por comando.

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-2101 | `wave close` y las 5 oleadas medibles en `recruit_waves` + bitácora | 8 h |
| T-2102 | Los 20 nodos reales simultáneos con las cinco condiciones de `verified` | 16 h |
| T-2103 | Evidencia fechada con `sha256` y ficheros `docs/verification/EP-15..21.md` | 8 h |
| T-2104 | Runbook `docs/runbooks/recruit.md` con operación, rollback y límites reales | 8 h |
| T-2105 | Cierre: paraguas en 0, seis sabotajes en rojo y gates existentes en 0 | 8 h |
