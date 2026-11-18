---
artifact: epic
id: EP-18
title: Verificación SOCKS5 real por nodo — el tag, el frente del gw y la sesión anclada
phase: F3
risk: high
estimate_days: 5
closes: A1-01, A1-03, A1-08, A3-03, injertos V1-1, V1-3
artifacts:
  - scripts/cross/build_matrix.sh
  - cmd/botnet-gw/main.go
  - cmd/botnet-gw/denylist.go
  - cmd/botctl/main.go
  - internal/proxy/socks5.go
  - internal/proxy/session_auth.go
---

## Objetivo

Que la condición de cierre «20 nodos reales con SOCKS5 real por nodo» sea **ejecutable y no teórica**. Hoy `scripts/cross/build_matrix.sh:80` compila sin `-tags socks5`, así que `internal/proxy/socks5_stub.go` deja `Start` devolviendo `ErrDisabled` y el nodo no escucha en `127.0.0.1:1080`; además `cmd/botnet-gw/main.go` no tiene front SOCKS5/CONNECT. A1-01 y A1-03 dicen que esto es **trabajo nuevo**, no «tal cual», y esta épica lo trata así. La decisión de proxy del blueprint §8 manda: el tag se publica solo para las arquitecturas de clase A.

## Alcance

- **Matriz con tag (blueprint §8)** — `scripts/cross/build_matrix.sh` pasa `-trimpath -tags socks5` para `amd64` y `arm64` (clase A); B/C conservan el stub. `MAX_A` sube de 4.194.304 B a **8.900.000 B** porque con el tag el binario pasa de 2.547.720 B a 8.403.456 B (8,01 MiB) y roza el techo de flash de clase A (8.388.608 B, lo pasa en 14.848 B). El techo de flash queda documentado en `docs/blueprint/BLUEPRINT.md` §2.
- **Frente SOCKS5/CONNECT en el gw** — `cmd/botnet-gw` escucha en `127.0.0.1:1080` y responde la negociación SOCKS5 (`05 02` / `05 00`) antes de hacer `CONNECT` hacia el nodo. EP-09 AC4 (`echo CONNECT ... | nc gw 1080`) deja de ser teórica.
- **Sesión anclada al nodo (A1-03)** — alta en `POST /api/v1/sessions` y autenticación RFC1929 con `bot-session` + `node_secret` del nodo y anti-replay de 30 s. Los 20 verify **no** comparten sesión ni sticky del gw (`cmd/botnet-gw/select.go:34-42`); `recruit_socks5_checks.node_id NOT NULL`.
- **Deny-list ampliada (A1-08)** — a los rangos actuales de `cmd/botnet-gw/denylist.go:20` (10/8, 172.16/12, 192.168/16, 100.64/10) se añaden `127/8` y `169.254/16`, más `::1` y `fe80::/10`; la verificación usa **IP literal** (`socks5`, no `socks5h`) para que la resolución no salga de la casa del dueño.
- **Worker de verificación (injerto V1)** — `botctl recruit verify --json`, stateless, un intento por ejecución, escribe una fila en `recruit_socks5_checks` con `target`, `http_status`, `bytes_in`, `latency_ms`, `egress_ip`, `ip_match`, `ok` y `attempt`. `ok=true` solo con la evidencia dura del blueprint §6: `http_status=200`, `bytes_in>0`, `latency_ms BETWEEN 0 AND 1500` e `ip_match=true`.
- **Contraste de IP de salida (injerto V1)** — `egress_ip = nodes.public_ip` y `egress_ip <> 74.208.94.91`; destino fijo `--dest http://www.example.com`; doble modo `--fixture testdata/recruit/socks5_checks.jsonl` (CI) y `--live` (VPS) — A3-09.
- **Consecuencia del B/C (blueprint §8, riesgo 1)** — un candidato enrolado con el stub queda `deferred` con `fail_reason='socks_fail'` y `note='clase_bc_sin_socks5'`; **los 20 verificados son de clase A** y `device_class` se publica en la bitácora.

## Fuera de alcance

- La matriz de tamaños para B/C no cambia: su tope sigue siendo `MAX_BC_UPX=2.726.297 B` post-upx.
- La contabilidad del plano de datos (EP-06/EP-10) y la cuota de inquilinos (`MaxTenantsPerNode = 8`) no se tocan.
- El presupuesto de ruido que protege al gw del escaneo es EP-16/EP-20; aquí solo el camino de datos de la verificación.
- No se añade un SOCKS5 nuevo en el agente: el servidor ya vive en `internal/proxy/socks5.go`; lo que falta es compilarlo y conectarle el frente.

## Dependencias

- **EP-15**: `recruit_socks5_checks` con `recruit_socks5_key UNIQUE (candidate_id, attempt)`, `recruit_socks5_one_win` y `recruit_socks5_ok_evidence`.
- **EP-16**: el worker toma `candidate_id` de la cola y escribe el resultado por la API del orquestador; la transición `enrolled → verified` la decide el orquestador.
- **EP-19** (hacia adelante): el `ExecStartPre` y el `/healthz` del servicio dependen de que el tag esté en el binario y de que el gw escuche.
- **Paralelizable desde el día 1**: no depende de la cola para la matriz ni para el frente del gw — abre rama aparte (es justo el trabajo que A1-01 vuelve obligatorio).

## Riesgos

| Riesgo | Prob. | Impacto | Mitigación |
|---|---|---|---|
| El tag multiplica ×3,3 el binario y revienta el flash de clase A (8.403.456 B vs 8.388.608 B) | alta | alto | tag solo en A; `MAX_A` a 8.900.000 B y techo de flash documentado; B/C con stub y `verify` en `deferred` |
| Un `200 OK` no prueba el nodo bajo prueba (A1-03) | alta | crítico | sesión anclada al `node_id` + `ip_match` + `count(DISTINCT node_id)` (hoy: 1 nodo y 20 reintentos) |
| La resolución por nombre salta la deny-list y alcanza el panel del router (A1-08) | media | alto | IP literal en la verificación + `127/8`, `169.254/16`, `::1`, `fe80::/10` |
| `example.com` cambia de comportamiento o devuelve portal cautivo con 200 (A3-09) | media | medio | destino fijo, `bytes_in>0` obligatorio y modo fixture en CI |
| Un 200 con IP de salida del VPS (tráfico local del gw) | media | crítico | `ip_match` con `egress_ip <> 74.208.94.91` y `nodes.public_ip` escrito (EP-19) |

## Criterio de salida de la épica

`strings dist/botd-amd64 | grep -c 'bot-session malformado'` = **1** (hoy 0); `netstat -ano | grep -c '127.0.0.1:1080.*LISTEN'` = **1** en el nodo; con 20 nodos reales en la lista, `psql -tAc "SELECT count(DISTINCT node_id) FROM recruit_socks5_checks WHERE ok"` = **20** sobre 20 filas (hoy 1); `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE ok AND ip_match"` = **20** y `size -t dist/botd-amd64` ≤ **8.900.000 B**.

## Tasks

| Task | Título | Estimación |
|---|---|---:|
| T-1801 | `-tags socks5` en la matriz para clase A con `MAX_A` nuevo | 6 h |
| T-1802 | Front SOCKS5/CONNECT en `cmd/botnet-gw` y listener en `:1080` | 8 h |
| T-1803 | Sesión RFC1929 anclada al `node_id` con anti-replay de 30 s | 10 h |
| T-1804 | `botctl recruit verify --json`: evidencia por intento en `recruit_socks5_checks` | 8 h |
| T-1805 | Contraste de IP de salida, deny-list ampliada y doble modo fixture/`--live` | 8 h |
