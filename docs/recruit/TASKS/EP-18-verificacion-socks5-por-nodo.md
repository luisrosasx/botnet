---
artifact: tasks
epic: EP-18
created: 2026-11-17
---

### T-1801 — `-tags socks5` en la matriz para clase A con `MAX_A` nuevo
- **Épica:** EP-18
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `strings dist/botd-amd64 | grep -c 'bot-session malformado'` = **1** (hoy **0**) y lo mismo en `dist/botd-arm64` = **1**; en `dist/botd-armv7` y `dist/botd-mipsbe` = **0** (B/C conservan `internal/proxy/socks5_stub.go`).
  2. `grep -c 'MAX_A=8900000' scripts/cross/build_matrix.sh` = **1** (el tope viejo era 4.194.304 B) y `size dist/botd-amd64 | awk 'NR==2{print $1+$2+$3}'` devuelve un valor **> 4.194.304** y **≤ 8.900.000**; `bash scripts/cross/build_matrix.sh` sale **0** con las 6 arquitecturas.
  3. `grep -cE -- '-trimpath -tags socks5' scripts/cross/build_matrix.sh` ≥ **1** y el tag se aplica solo a `amd64`/`arm64`: `bash scripts/cross/build_matrix.sh --dry-run | grep -c 'tags socks5'` = **2**.
  4. B/C no se rompen: `size dist/botd-armv5 dist/botd-mipsle | awk '{s+=$5} END {print (s<=2*2726297)?"ok":"mal"}'` = `ok` (tope `MAX_BC_UPX=2.726.297 B` post-upx intacto) y `upx -t dist/botd-mipsle` sale **0**.
  5. El techo de flash queda escrito: `grep -cE '8\.388\.608|8388608' docs/blueprint/BLUEPRINT.md` ≥ **1** y `grep -c '8.900.000\|8900000' docs/blueprint/BLUEPRINT.md` ≥ **1**.
- **Depende de:** — (rama desde el día 1)
- **Estimación:** 6 h
- **DoD:** el binario que se instala en un router de clase A trae el SOCKS5 dentro y cabe en su flash; la matriz deja de prometer un listener que no está compilado.
- **Ficheros:** `scripts/cross/build_matrix.sh`, `docs/blueprint/BLUEPRINT.md`, `Makefile`
- **Cierra:** A1-01 (pata del binario), gate G3

### T-1802 — Front SOCKS5/CONNECT en `cmd/botnet-gw` con listener en `:1080`
- **Épica:** EP-18
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `ss -ltn | grep -c ':1080'` = **1** con el gw corriendo (hoy `nc: connection refused`) y `echo CONNECT 8.8.8.8:53 | timeout 3 nc 127.0.0.1 1080 | od -An -tx1 | head -1` imprime ` 05 02` o ` 05 00` (`grep -cE '05 0[02]'` = **1**).
  2. `botnet-gw --help | grep -c -- '--socks5-listen'` = **1** y `grep -cE 'socks5|CONNECT' cmd/botnet-gw/main.go` ≥ **2** (el frente es trabajo nuevo, no «tal cual» — A1-01).
  3. `go test ./cmd/botnet-gw/ -run TestSocks5Handshake -v 2>&1 | grep -c -- '^--- PASS'` ≥ **3** (versión 5 sin autenticación, versión 4 rechazada, y método no soportado rechazado con `05 02 ff`).
  4. El `CONNECT` no abre el stream hasta tener sesión válida: con `bot-session` inválido, `psql -tAc "SELECT count(*) FROM recruit_socks5_checks"` no sube y el log del gw imprime `socks5=rejected` (`grep -c 'socks5=rejected'` ≥ **1**) sin conectar con el nodo (`netstat` no muestra la conexión saliente durante el intento).
- **Depende de:** — (rama desde el día 1; EP-06 y EP-09 ya cerrados)
- **Estimación:** 8 h
- **DoD:** hay por dónde entrar: el gateway escucha SOCKS5 y responde la negociación antes de tocar un nodo, así que EP-09 AC4 se ejecuta de verdad.
- **Ficheros:** `cmd/botnet-gw/main.go`, `cmd/botnet-gw/socks5_front.go`, `cmd/botnet-gw/socks5_front_test.go`
- **Cierra:** A1-01 (pata del gateway)

### T-1803 — Sesión RFC1929 anclada al `node_id` con anti-replay de 30 s
- **Épica:** EP-18
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `curl -s -X POST 127.0.0.1:8080/api/v1/sessions -H "Authorization: Bearer $KEY" -d '{"node_id":"<uuid>"}' -o /tmp/s.json -w '%{http_code}'` = **201** y `grep -c node_id /tmp/s.json` = **1** (la sesión lleva el nodo, no lo elige el sticky de `cmd/botnet-gw/select.go:34-42`).
  2. Autenticación RFC1929 obligatoria: con `user=bot-session:<id>` y `pass=$NODE_SECRET` el verify sale **0**; con la contraseña alterada en 1 byte sale **≠ 0** y `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE checked_at > now()-interval '5 min'"` no sube (0 filas nuevas).
  3. Anti-replay de 30 s (`internal/proxy/session_auth.go:15`): repetir el mismo `bot-session` a los **10 s** ⇒ rechazado con `session_replay` en el log (`grep -c 'session_replay'` ≥ **1**); a los **31 s** ⇒ aceptado (2/2 casos).
  4. Ni sticky ni sesión compartida: 20 verificaciones concurrentes sobre 20 nodos dejan `psql -tAc "SELECT count(DISTINCT node_id) FROM recruit_socks5_checks WHERE checked_at > now()-interval '10 min'"` = **20** y `count(*)` = **20** (hoy: 1 nodo y 20 reintentos) con `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE node_id IS NULL"` = **0**.
- **Depende de:** T-1802, T-1504
- **Estimación:** 10 h
- **DoD:** un `200 OK` prueba el aparato bajo prueba: la sesión está anclada al `node_id`, autenticada y no se puede repetir.
- **Ficheros:** `cmd/botnet-api/handlers/sessions.go`, `internal/proxy/socks5.go`, `internal/proxy/session_auth.go`, `cmd/botnet-gw/select.go`
- **Cierra:** A1-03

### T-1804 — `botctl recruit verify --json` con evidencia por intento
- **Épica:** EP-18
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `botctl recruit verify --node=<uuid> --json` sale **0** e imprime las **7** claves: `grep -cE '"(node_id|target|http_status|bytes_in|latency_ms|egress_ip|ip_match)"'` = **7** con `target` no vacío.
  2. Una fila por intento: dos ejecuciones sobre el mismo candidato dejan `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE candidate_id=$1"` = **2** con `attempt` **1** y **2** (`psql -tAc "SELECT count(*) FROM (SELECT DISTINCT attempt FROM recruit_socks5_checks WHERE candidate_id=$1) x"` = **2**) sin violar `recruit_socks5_key`.
  3. `ok` solo con evidencia dura `[injerto: V1]`: con el fixture de portal cautivo (`http_status=200`, `bytes_in=0`) la fila queda `ok=f`; con el fixture bueno queda `ok=t` (2/2) y `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE ok AND (bytes_in<=0 OR latency_ms>1500 OR http_status<>200)"` = **0**.
  4. Fallar el verify no mata al candidato: tras **3** intentos fallidos `state` sigue en `enrolled` (o `deferred`) con `fail_reason='socks_fail'` y `stage_attempts` = **3**, y `psql -tAc "SELECT count(*) FROM recruit_candidates WHERE state='discarded' AND fail_reason='socks_fail'"` = **0** en la primera oleada.
  5. El worker es stateless: `botctl recruit verify` no deja estado local (0 ficheros nuevos en `$HOME/.botctl` tras 5 ejecuciones, `ls | grep -c recruit` = **0**) y puede correr desde cualquier host con acceso al gw.
- **Depende de:** T-1803, T-1504, T-1605
- **Estimación:** 8 h
- **DoD:** la evidencia del SOCKS5 la escribe un worker por intento, con sus números, y `ok=true` deja de ser una casilla que alguien rellena.
- **Ficheros:** `cmd/botctl/main.go`, `cmd/botctl/recruit_verify.go`, `cmd/botctl/recruit_verify_test.go`
- **Cierra:** V1-1, A3-03 (medición de la evidencia)

### T-1805 — Contraste de IP de salida, deny-list ampliada y doble modo fixture/`--live`
- **Épica:** EP-18
- **Estado:** PENDING
- **Criterios de aceptación:**
  1. `psql -tAc "SELECT count(*) FROM recruit_socks5_checks s JOIN nodes n USING (node_id) WHERE s.ok AND s.ip_match AND s.egress_ip = n.public_ip AND s.egress_ip <> '74.208.94.91'"` = **20** de 20 filas ganadoras (hoy **0** — nadie escribe `egress_ip`) `[injerto: V1]`.
  2. Sabotaje del portal cautivo: `psql -c "UPDATE recruit_socks5_checks SET egress_ip='74.208.94.91', ip_match=false WHERE check_id=(SELECT max(check_id) FROM recruit_socks5_checks)"` hace que `bash scripts/check-recruit.sh --only socks5; echo exit=$?` devuelva **≥ 1**, y revertirlo (mismo UPDATE con el valor previo) lo devuelve a **0**.
  3. `botctl recruit verify --help | grep -c 'www.example.com'` = **1** (destino fijo del blueprint §6) y `botctl recruit verify --fixture testdata/recruit/socks5_checks.jsonl --json | grep -c '"node_id"'` = **20** con salida **0** sin tocar la red.
  4. `--live` con flota vacía muerde: con `nodes` sin enrolados, `botctl recruit verify --live; echo exit=$?` = **1** y `psql -tAc "SELECT count(*) FROM recruit_socks5_checks WHERE bytes_in=0"` = **20** (sabotaje 3 de A3 §3 — hoy sale 0).
  5. La deny-list ampliada rechaza dentro del router: `grep -cE '"127\.0\.0\.0/8"|"169\.254\.0\.0/16"' cmd/botnet-gw/denylist.go` = **2** y `go test ./cmd/botnet-gw/ -run TestDeniedConnect -v 2>&1 | grep -cE '127\.0\.0\.1|169\.254|::1|fe80'` ≥ **4** (los 4 casos nuevos; A1-08).
- **Depende de:** T-1804, T-1904
- **Estimación:** 8 h
- **DoD:** el tráfico salió de casa del dueño: la IP de salida es la del nodo, no la del VPS, y ni `127/8` ni `169.254/16` se cuelan por nombre.
- **Ficheros:** `cmd/botnet-gw/denylist.go`, `cmd/botctl/recruit_verify.go`, `testdata/recruit/socks5_checks.jsonl`
- **Cierra:** V1-3, A3-03, A1-08, A3-09 (doble modo)
