---
artifact: epic
id: EP-18
title: "Verificación SOCKS5 real por nodo"
owner_layer: recruit
closes: A1-01, A1-03, A1-08, A3-03, A3-09, injertos V1-1, V1-3, gate G3
estimate_days: 5
risk: high
phase: F7
---

## Objetivo

Cerrar la pata más frágil del piloto: que un `200 OK` demuestre el aparato bajo prueba y no el portal cautivo del VPS. Para eso el binario de clase A lleva el proxy SOCKS5 compilado (`-tags socks5` con `MAX_A` nuevo), el gateway abre un frente SOCKS5/CONNECT en `:1080` que es trabajo nuevo y no «tal cual», la sesión RFC1929 queda anclada al `node_id` con anti-replay de 30 s, y `botctl recruit verify --json` escribe una fila por intento con su evidencia dura: `http_status`, `bytes_in>0`, `latency_ms≤1500`, `egress_ip` y `ip_match`. `ok=true` deja de ser una casilla que alguien rellena.

## Alcance

- `scripts/cross/build_matrix.sh` + `Makefile`: `-trimpath -tags socks5` solo en `amd64`/`arm64`, `MAX_A=8900000` (tope viejo 4.194.304 B) y `MAX_BC_UPX=2.726.297 B` intacto para B/C con `internal/proxy/socks5_stub.go`; el techo de flash de 8.388.608 B queda escrito en `docs/blueprint/BLUEPRINT.md`.
- `cmd/botnet-gw/main.go`, `cmd/botnet-gw/socks5_front.go`, `socks5_front_test.go` (nuevo): listener `--socks5-listen` en `:1080` que responde la negociación (versión 5 sin autenticación, `05 02 ff` al método no soportado) antes de tocar un nodo.
- `internal/proxy/socks5.go` + `internal/proxy/session_auth.go`: `user=bot-session:<id>` + `pass=$NODE_SECRET` con anti-replay de 30 s (línea 15 de `session_auth.go`).
- `cmd/botnet-api/handlers/sessions.go` + `cmd/botnet-gw/select.go`: `POST /api/v1/sessions` con `node_id` explícito; se acaba el sticky que elegía el nodo por los 20 verify.
- `cmd/botctl/main.go`, `cmd/botctl/recruit_verify.go`, `recruit_verify_test.go` (nuevo): `botctl recruit verify --node=<uuid> --json` con las 7 claves de evidencia, doble modo `--fixture` / `--live`, destino fijo `http://www.example.com` (blueprint §6) y worker stateless.
- `cmd/botnet-gw/denylist.go`: deny-list ampliada a `127.0.0.0/8`, `169.254.0.0/16`, `::1` y `fe80::/10` (A1-08).
- `testdata/recruit/socks5_checks.jsonl`: fixture de 20 nodos con el caso de portal cautivo (`http_status=200`, `bytes_in=0`).

Fuera de alcance: la regla de `verified` en código es EP-16 y su conteo en el paraguas es EP-20; el cierre con los 20 nodos reales es EP-21.

## Cierre

- **T-1801 AC1** — `strings dist/botd-amd64 | grep -c 'bot-session malformado'` = **1** (hoy **0**) y lo mismo en `dist/botd-arm64`; en `dist/botd-armv7` y `dist/botd-mipsbe` = **0** (B/C conservan el stub).
- **T-1801 AC2** — `grep -c 'MAX_A=8900000' scripts/cross/build_matrix.sh` = **1** y `size dist/botd-amd64 | awk 'NR==2{print $1+$2+$3}'` da un valor **> 4.194.304** y **≤ 8.900.000**; `bash scripts/cross/build_matrix.sh` sale **0** con las 6 arquitecturas.
- **T-1801 AC3** — `grep -cE -- '-trimpath -tags socks5' scripts/cross/build_matrix.sh` ≥ **1** y `bash scripts/cross/build_matrix.sh --dry-run | grep -c 'tags socks5'` = **2** (solo `amd64`/`arm64`).
- **T-1801 AC4** — B/C no se rompen: `size dist/botd-armv5 dist/botd-mipsle | awk '{s+=$5} END {print (s<=2*2726297)?"ok":"mal"}'` = `ok` y `upx -t dist/botd-mipsle` sale **0**.
- **T-1801 AC5** — el techo de flash queda escrito: `grep -cE '8\.388\.608|8388608' docs/blueprint/BLUEPRINT.md` ≥ **1** y `grep -c '8.900.000\|8900000' docs/blueprint/BLUEPRINT.md` ≥ **1**.
- **T-1802 AC1** — `ss -ltn | grep -c ':1080'` = **1** con el gw corriendo y el saludo `nc 127.0.0.1 1080` imprime `05 02` o `05 00` (`grep -cE '05 0[02]'` = **1**).
- **T-1802 AC2** — `botnet-gw --help | grep -c -- '--socks5-listen'` = **1** y `grep -cE 'socks5|CONNECT' cmd/botnet-gw/main.go` ≥ **2** (el frente es trabajo nuevo, A1-01).
- **T-1802 AC3** — `go test ./cmd/botnet-gw/ -run TestSocks5Handshake -v 2>&1 | grep -c -- '^--- PASS'` ≥ **3** (versión 5 sin autenticación, versión 4 rechazada y método no soportado con `05 02 ff`).
- **T-1802 AC4** — con `bot-session` inválido no abre el stream: `count(*) FROM recruit_socks5_checks` no sube, el log imprime `socks5=rejected` y no hay conexión saliente con el nodo.
- **T-1803 AC1** — `POST /api/v1/sessions` con `{"node_id":"<uuid>"}` devuelve **201** y `grep -c node_id /tmp/s.json` = **1** (la sesión lleva el nodo, no lo elige el sticky de `select.go:34-42`).
- **T-1803 AC2** — autenticación RFC1929 obligatoria: con `user=bot-session:<id>` y `pass=$NODE_SECRET` el verify sale **0**; con la contraseña alterada en 1 byte sale **≠ 0** y no suben las filas de `recruit_socks5_checks` de los últimos 5 min.
- **T-1803 AC3** — anti-replay de 30 s (`internal/proxy/session_auth.go:15`): repetir el mismo `bot-session` a los 10 s da `session_replay` en el log (`grep -c` ≥ **1**) y a los 31 s se acepta (2/2).
- **T-1803 AC4** — 20 verificaciones concurrentes sobre 20 nodos dejan `count(DISTINCT node_id)` = **20** y `count(*)` = **20** (hoy: 1 nodo y 20 reintentos), con `count(*) WHERE node_id IS NULL` = **0**.
- **T-1804 AC1** — `botctl recruit verify --node=<uuid> --json` sale **0** con las **7** claves `node_id|target|http_status|bytes_in|latency_ms|egress_ip|ip_match` (`grep -cE` = **7**) y `target` no vacío.
- **T-1804 AC2** — una fila por intento: dos ejecuciones del mismo candidato dejan `count(*) WHERE candidate_id=$1` = **2** con `attempt` **1** y **2**, sin violar `recruit_socks5_key`.
- **T-1804 AC3** — `ok` solo con evidencia dura `[injerto: V1]`: el fixture de portal cautivo (`http_status=200`, `bytes_in=0`) queda `ok=f` y el bueno `ok=t`; `count(*) WHERE ok AND (bytes_in<=0 OR latency_ms>1500 OR http_status<>200)` = **0**.
- **T-1804 AC4** — fallar el verify no mata al candidato: tras 3 intentos sigue `enrolled` (o `deferred`) con `fail_reason='socks_fail'` y `stage_attempts` = **3**, con **0** `discarded` por `socks_fail` en la primera oleada.
- **T-1804 AC5** — worker stateless: 5 ejecuciones no dejan ficheros nuevos en `$HOME/.botctl` (`ls | grep -c recruit` = **0**) y puede correr desde cualquier host con acceso al gw.
- **T-1805 AC1** — `count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id) WHERE s.ok AND s.ip_match AND s.egress_ip = n.public_ip AND s.egress_ip <> '74.208.94.91'` = **20** de 20 ganadoras (hoy **0**, nadie escribe `egress_ip`).
- **T-1805 AC2** — sabotaje del portal cautivo: poner `egress_ip='74.208.94.91', ip_match=false` en la última fila hace que `bash scripts/check-recruit.sh --only socks5` salga **≥ 1**, y revertirlo lo devuelve a **0**.
- **T-1805 AC3** — `botctl recruit verify --help | grep -c 'www.example.com'` = **1** (destino fijo del §6) y `--fixture testdata/recruit/socks5_checks.jsonl --json | grep -c '"node_id"'` = **20** con salida **0** sin tocar la red.
- **T-1805 AC4** — `--live` con flota vacía muerde: `botctl recruit verify --live; echo exit=$?` = **1** y `count(*) FROM recruit_socks5_checks WHERE bytes_in=0` = **20** (sabotaje 3 de A3 §3, hoy sale 0).
- **T-1805 AC5** — deny-list ampliada: `grep -cE '"127\.0\.0\.0/8"|"169\.254\.0\.0/16"' cmd/botnet-gw/denylist.go` = **2** y `go test ./cmd/botnet-gw/ -run TestDeniedConnect -v` menciona los 4 casos nuevos (`grep -cE '127\.0\.0\.1|169\.254|::1|fe80'` ≥ **4**, A1-08).
