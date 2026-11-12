---
artifact: runbook
epic: EP-07
created: 2026-11-10
---

# Malla WireGuard/headscale y regla de encapsulado (EP-07)

La malla es el **plano de operaciones**, best-effort: solo nodos con
`tun` entran, con dirección CGNAT `100.64.x.y` (rango `100.64/10`).
El plano de datos **nunca** la pisa: toda la sesión viaja por la única
TLS/H2 saliente del nodo (BLUEPRINT §5, §8 — lección del hub de P3).
Cae headscale y ni un byte de sesión se entera: dominio de fallo acotado.

## Qué viaja / qué nunca viaja

| Por la malla SÍ viaja | Por la malla NUNCA viaja |
|---|---|
| SSH de operaciones (bastión → nodo, puertos 22) | bytes de sesión del proxy |
| sonda iperf3 que siembra `bandwidth_cap_kbps` | heartbeats |
| sonda de geo del exit que fija `geo_verified` | flushes contables |
| | updates del binario |

La lista «nunca viaja» es contrato: `scripts/mesh/mesh_bytes_check.sh`
lo comprueba por pcap, no por fe, y el CI de config rechaza cualquier
flag que enrute dato por WG:

```bash
bash scripts/mesh/mesh_bytes_check.sh            # guard de config + doble pcap
bash scripts/mesh/mesh_bytes_check.sh --pcap-bytes 524288 --sessions-mb 100
```

El guard dura es `grep -c "data_plane=wg" deploy/` = **0**: si alguien
añade un flag tipo `data_plane=wg` al despliegue, el check rompe.

## headscale en el bastión

`deploy/headscale/config.yaml` fija el rango `100.64.0.0/10`, `listen_addr`
solo en `127.0.0.1` (el bastión no abre puerto externo por la malla) y
`randomize_client_port: false` para que la IP del nodo sea estable.
`deploy/headscale/aclpolicy.hujson` define la única regla aceptada:
`tag:ops-bastion → tag:node:22`. El rango de clientes no tiene regla
de entrada: SSH desde un cliente a la malla es **timeout** (A2-11).

Comprobación viva (requiere headscale en el bastión, fuera de F4):

```bash
headscale nodes list | grep -c managed   # == nº de nodos con tun
ip -4 addr | grep -c 100.64.             # == nº de nodos en la malla
```

Nodos clase C sin `tun`: no se enrolan, no reciben `tag:node`, y su
proxy SOCKS5 funciona igual (0 pérdida de producto: el dato nunca
dependió de WG).

## SSH de operaciones (T-0702)

Desde el bastión, por la IP de malla:

```bash
ssh -o ConnectTimeout=5 ops@100.64.x.y 'echo ok'   # < 5 s, 100 % de nodos con tun
```

El SSH **no abre puerto escuchante extra** en la interfaz externa:
el `ss -ltn` del nodo no gana ningún `0.0.0.0` por tener malla — el
acceso entra por el túnel WG (sshd ya escuchaba para el dueño o el
access del router). La política del runbook: **nunca** sesión,
heartbeat, flush ni update por la malla.

## Sonda iperf3 → `bandwidth_cap_kbps` (T-0703)

```bash
NODE_ID=<uuid> bash scripts/mesh/iperf3_probe.sh          # malla arriba
bash scripts/mesh/iperf3_probe.sh --self-test 4096        # doble sin iperf3
```

La sonda mide por la malla y siembra `nodes.bandwidth_cap_kbps` con
valor en `[medido × 0.8, medido]` (factor 0.9 en
`cmd/botnet-api/services/caps.go`: tope medido, no declarado; la
página de transparencia lo refleja). Nodos sin `tun`: no hay sonda y
queda el default del perfil de clase (A 5000 / B 3000 / C 2048 kbps);
el agente aplica el bucket con ese mismo valor. La sonda **no genera
sesiones SOCKS**: el delta de `botnet_sessions_total` durante la sonda
es 0.

## Sonda de geo del exit → `geo_verified` (T-0704)

```bash
NODE_ID=<uuid> bash scripts/mesh/geo_probe.sh             # una pasada
```

Cadencia **semanal**: el exit sondea su geo real a través del gateway
y el backend (`POST /v1/nodes/{id}/geo`, bearer admin) escribe
`country_probed`, `asn_probed`, `geo_checked_at` y `geo_verified`
(migración `0006_geo_verified`). `geo_verified = true` exige país
sondeado == país esperado y ASN **no** de datacenter: un VPS en DC
extranjero ya no vende «residencial» colado — sale del pool al
instante (select que lee `geo_verified`; el ciclo completo es < 24 h).
El país del pool se lee de la geo **sondeada**, no del autorreporte:
nodo self-report ES con sonda US no se vende como ES.

Presupuesto: ~700 B por sonda → 7 sondas/7 días → ≤ ~3 KB/nodo/mes
(muy por debajo del tope de 50 KB; lo asserts el test de `services`).

## Regla de encapsulado (T-0705)

Un encapsulado por byte: **prohibido WG-sobre-dato** (y su peor caso,
WG-sobre-TLS-sobre-WG). `tcpdump -i wg0` durante 100 sesiones
(≈100 MB de dato) debe capturar **< 1 MB** en la malla; el script lo
afirma con el pcap o con el doble `--pcap-bytes` cuando el host no
tiene tcpdump. El enlace del nodo muestra una sola cadencia de
destino (la TLS/H2 saliente, coherente con T-0403).

## Dominio de fallo

headscale es un servicio del bastión con estado mínimo (sqlite): su
caída solo quita SSH y sondas. Ninguna sesión, heartbeat, flush ni
update depende de él; el dominio de fallo de la malla queda acotado
a operaciones (< 1 % del plano). Lección del hub UDP único de P3,
aplicada: la malla tampoco es el plano de datos.

## Verificación pendiente en vivo (F5/soak)

Este host F4 no binario `headscale`, `iperf3` ni `tcpdump`: los
umbrales (CGNAT, < 1 MB por pcap, SSH < 5 s) los afirman los tests Go
con dobles (`deploy/headscale/mesh_config_test.go`,
`internal/agent/mesh_test.go`) y los scripts llevan modo doble. Al
montar el bastión real: correr los tres comandos de arriba y anclar
salidas aquí.

## Versión fijada (S-11)

El bastión corre **la versión de `deploy/headscale/VERSION`** y no otra: la
malla es plano de operaciones y una subida accidental rompe la cadena de
arranque sin avisar.

v0.29 rompió el formato anterior. Lo migrado en este repo:

| Antes (v0.23-0.26) | Ahora (v0.29) |
|---|---|
| `randomize_client_port` en config | `randomizeClientPort` en el fichero de política |
| `prefixes.v6: native` | prefijo explícito (`fd7a:115c:a1e0::/48`) |
| sin bloque `noise` | `noise.private_key_path` obligatorio |
| `database.driver/host/name` | `database.type` + `database.sqlite.path` |

La clave de ruido y el sqlite viven fuera del repo en el bastión
(`/var/lib/headscale`, 0600 y 0700); aquí solo se versiona el config y la
política. Arranque comprobado en local con `headscale -c deploy/headscale/config.yaml serve`.
