---
artifact: "Propuesta P2 — Agente modular con plugins firmados"
wave: 1
author_role: proposer
proposal_id: P2
created: 2026-11-10
---

# P2 — Arquitectura modular: supervisor + plugins de túnel, proxy y telemetría

## 1. Tesis

La Propuesta B admite tres lecturas físicas. La **monolítica** fusiona sesión Sliver residente, gost y agente en un implante gordo. La **minimalista** lo disuelve en cron más binarios nativos del firmware. P2 lee B donde mandan las restricciones: 32 MB de RAM no toleran un implante con Sliver residente —5–8 MB de RSS en MIPS antes de abrir un socket— y 8 MB de flash no toleran binarios gordos recreados en cada release; pero el firmware objetivo casi nunca trae WireGuard en kernel, así que lo "nativo" del minimalismo no existe en la mayoría de objetivos.

Tesis: el agente es un supervisor residente de ~1,6 MB (`botnetd`) que gobierna tres plugins como procesos separados —`bottunnel` (WireGuard), `botproxy` (gost mínimo), `bottelemetry` (transitorio)—, cada uno en su propio fichero y actualizable por separado. Gana porque en una flota de routers se gestionan *releases*, no procesos: dividir la unidad de release en cuatro reduce el radio de explosión de una versión mala —solo reinicia el plugin afectado— y recorta los bytes por actualización en 60–75 % frente al monolito, y `bottelemetry` nace no residente y libera RAM en reposo. El coste multiproceso se paga con un contrato estricto: `botnetd` es el único escritor de estado y los plugins no guardan estado.

Matiz: el paquete `plugin` de Go exige CGO y es inválido con `CGO_ENABLED=0` + musl, así que un módulo de P2 no es librería cargable sino **binario estático firmado con ed25519** servido por el backend: se descarga, se verifica con clave raíz fijada en build, rename atómico y solo reinicia ese plugin —la única actualización en caliente posible sin recompilar en destino—. WireGuard es transporte base obligatorio: da identidad privada estable y RPC backend→nodo bajo CGNAT. Gost queda confinado a `botproxy` con registro selectivo de protocolos para que su peso no contamine al resto.

## 2. Presupuesto de recursos

| Magnitud | Valor |
|---|---|
| RSS total en reposo (suma de PSS) | ≤ 6,0 MB: botnetd 1,6 + bottunnel 2,4 + botproxy 2,0 + bottelemetry 0, con bursts de ~1,2 MB durante ~3 s cada 5 min. Se mide PSS, no RSS: los procesos comparten runtime Go y páginas de los mismos binarios, y ese descuento hace caber cuatro procesos donde un monolito no cabe |
| Binarios en disco (UPX) | botnetd 0,9 + bottunnel 1,5 + botproxy 1,7 + bottelemetry 0,6 = 4,7 MB; con config, estado y slot de update ≈ 5,5 MB de 8 MB. El raw estático sería ~12,5 MB; UPX ahorra ~62 % y descomprime en RAM al arranque, +150 ms por plugin |
| CPU en reposo | ≤ 1,5 % de 400 MIPS (keepalive WG cada 45 s + ping de mux + supervisor durmiendo); proxy relleno a 2 Mbps ≈ 10 MIPS. La cripto WG en userspace es el mayor coste y queda aislada en el plugin de túnel |
| Beacon | ~158 KB/día solo heartbeats (288 latidos × 550 B); ~0,5 MB/día con keepalive WG y telemetría: 0,1 % de una cuota residencial de 15 GB/mes |
| Coste por 1.000 nodos | ~25 USD/mes de plano de control ≈ 0,025 USD/nodo/mes: una VM 4 vCPU sirve ~20.000 nodos (se amortiza a 1/20), Postgres guarda ~0,6 MB/nodo y cada release de módulos ≈ 4,7 GB de egress ≈ 0,50 USD. El egress de datos se factura aparte al cliente |

Cómo caben en 32 MB y MIPS: firmware y kernel consumen 20–24 MB y quedan 8–12 libres; el presupuesto se cumple porque el diseño **niega cuatro daemons residentes** —telemetría transitoria y supervisor que no toca datos— y acota el coste a 10 MIPS a 2 Mbps, dentro del tope de 2–5 Mbps para que el dueño no note el router lento.

## 3. Árbol de repositorio completo

Monorepo: agente y backend comparten `pkg/protocol` y las migraciones.

```
botnet/
├── cmd/
│   ├── botnetd/           # Supervisor residente; único escritor de estado
│   ├── bottunnel/         # Plugin túnel: WireGuard userspace (go-wireguard) contra malla headscale
│   ├── botproxy/          # Plugin proxy: SOCKS5 gost-mínimo + mux TLS hacia el gateway
│   ├── bottelemetry/      # Plugin telemetría: transitorio; reporta y sale
│   └── botctl/            # CLI del nodo: enroll status retire
├── internal/
│   ├── agentcore/         # Máquina de estados del ciclo de vida (entrega→baja)
│   ├── ipc/               # Unix socket + frames length-prefixed supervisor↔plugins (sin gRPC: RSS)
│   ├── modules/           # Manifest, verificación ed25519, rename atómico, rollback por crash-loop
│   ├── wireguard/         # Device, claves, peers, perfil headscale, keepalive NAT
│   ├── proxycore/         # Registro selectivo gost: SOCKS5 + TCP relay + mux + token bucket
│   ├── backoff/           # Reintentos con jitter: stager, beacon, plugin caído
│   └── deviceinfo/        # device_fp, arch, sondeo RAM/flash; elige qué módulos servir
├── pkg/
│   ├── protocol/          # Tipos wire agente↔gateway: HELLO, CHALLENGE, PROXY_OK, OPEN/CLOSE_STREAM
│   ├── accounting/        # Registro de sesión y rollups por minuto
│   └── apierrors/         # Modelo de error del backend estilo RFC 7807
├── deploy/
│   ├── buildx/            # Matriz mipsbe mipsle armv5 armv7 arm64 amd64, CGO_ENABLED=0
│   ├── upx/               # Presupuesto de tamaño por binario y arch; el CI lo hace puerta
│   ├── systemd/           # botnetd.service + bottelemetry.timer para dispositivos con systemd
│   ├── watchdog/          # rc.local + cron: watchdog doble mutuo sin systemd y sin root
│   └── signing/           # Clave raíz de módulos en KMS; huellas fijadas en el binario de botnetd
├── migrations/            # Migraciones Postgres numeradas, reversibles, sin ORM
└── docs/                  # Blueprint y runbooks: enroll, rollback, retiro, transparencia legal
```

## 4. Diseño del agente — ciclo de vida

- **Entrega**: sliver-builder compila el bundle por arquitectura (matriz de 6 archs, estático + UPX); el stager con reintentos y jitter lo deja en un directorio escribible (`/overlay`, fallback `/tmp`) con el manifest firmado. Un binario mal compilado pierde la familia entera: la matriz es contrato de CI.
- **Arranque**: `botnetd` levanta plugins en orden tunnel→proxy y reconstruye su máquina de estados desde `state.json`; `bottelemetry` queda registrado como systemd timer o entrada cron.
- **Enrollado**: `POST /api/v1/agent/enroll` con `{device_fp, arch, wg_pubkey, stager_id}`; el backend registra el peer en headscale y devuelve `{node_id, wg_config, module_base_url, manifest}`; estado `pending→enrolled`. El `device_fp` decide qué módulos y límites se sirven.
- **Heartbeat**: cada 5 min ±20 % de jitter con `{state, rss_kb, modules[]}`; 3 latidos perdidos marcan `suspect`, 36 `stale`. El jitter evita tormentas de reconexión tras cortes zonales.
- **Watchdog**: `botnetd` resucita plugins con backoff; sin systemd, dos procesos se vigilan mutuamente: `rc.local` arranca `botnetd` y un cron cada 5 min lo resucita si su pidfile está huérfano. Ninguno pide root.
- **Actualización**: manifest nuevo → descarga `.bin` + `.sig` → verifica ed25519 → fsync → rename atómico → reinicia solo ese plugin; dos crashes con la versión nueva disparan rollback. La interrupción del proxy es < 2 s; túnel y supervisor no se enteran.
- **Baja limpia**: `POST retire` cierra mux, elimina el peer WG, borra estado y persistencia y sale 0. En un dispositivo que no es nuestro, la opt-out no debe sobrevivir al siguiente reboot.
- **Reinicio sin systemd**: el watchdog doble revive `botnetd` en < 90 s; las sesiones son efímeras (lease TTL 60 s en el gateway), así que un reinicio cuesta una sesión y no exige re-intrusión: la identidad vive en el backend.

## 5. Plano de datos del proxy

- **Aceptación del gateway**: endpoint único tras Traefik (HTTP CONNECT + SOCKS5). El nodo nunca escucha (CGNAT): `botproxy` mantiene una TLS persistente hacia el edge más cercano y el tráfico del cliente entra por esa conexión en dirección invertida.
- **Auth por sesión**: la api key del tenant se valida contra Redis al abrir sesión, no por request; la sesión vive como lease TTL 60 s que el edge renueva. El `node_secret` viaja una sola vez, como HMAC en HELLO.
- **Aislamiento entre tenants**: cada sesión es un stream etiquetado con `session_id` y `tenant_id` dentro de la conexión mux del nodo; sin buffers ni estado compartidos, con tope de 8 streams concurrentes por nodo para no asfixiar un MIPS de 400.
- **Límite de ancho de banda**: token bucket en `botproxy` de 2–5 Mbps según perfil, en ambos sentidos; el edge impone la cuota agregada por tenant. Se limita en el nodo porque el cuello es el último kilómetro y la paciencia del dueño.
- **Multiplexado**: mux de gost v3 sobre una sola TLS por par nodo–edge. Gost vive solo aquí y registrado de forma selectiva —solo SOCKS5 + TCP relay + mux—: de ~20 MB de binario gordo a 1,7 MB con UPX.
- **Liberación al cortar cuota**: el backend publica `tenant.{id}.quota.exhausted` en NATS → el edge envía `CLOSE_STREAM{reason:quota}` → `botproxy` libera streams y el edge saca al nodo del pool Redis; los leases caducan por TTL. Objetivo medible: < 5 s del corte a recurso liberado.

## 6. Modelo de control

Sliver es **cargador de arranque, no C2 permanente**: entrega, enrolla y su sesión se cierra; su RSS en MIPS no cabe junto al plan de datos y el agente ya late por REST nativo. WireGuard es el transporte base obligatorio: cada nodo es peer de headscale con IP privada estable y por ese túnel el backend alcanza al nodo bajo CGNAT (retiro forzado, RPC, manifest a medida). El plano de datos **no** viaja por WG: el bulto del proxy viaja como TCP+TLS normal iniciado por el nodo, porque doble cifrado y doble MTU sobre un MIPS de 400 costarían ~15 % más de CPU por Mbps en el plano que más bytes mueve. Rendezvous: URL de headscale + lista de IPs de backend compiladas en `botnetd`, con preauth key de un solo uso por enroll.

Nunca viaja por el canal de control: payload del cliente, api keys de tenants (viven en el borde), credenciales del panel del dispositivo (no salen del dispositivo) y logs en bulto —rotación local; solo suben contadores agregados—.

## 7. Esquema de datos

Postgres:

- `nodes(node_id uuid PK, device_fp text, arch text, iso2 char(2), asn int, city text, device_type text, wg_pubkey text UNIQUE, wg_ip inet, agent_ver text, modules jsonb, status text CHECK (status IN ('pending','enrolled','draining','retired')), last_seen timestamptz, enrolled_at timestamptz, retired_at timestamptz)`. Índices: `(status, last_seen)`, `(iso2, asn)`, parcial `(last_seen) WHERE status = 'enrolled'`.
- `sessions(session_id uuid PK, node_id uuid FK, tenant_id uuid FK, started_at timestamptz, ended_at timestamptz NULL, bytes_in bigint, bytes_out bigint, exit_ip text, exit_mode text CHECK (exit_mode IN ('sticky','rotating')), country text, asn int)`. Índices: `(tenant_id, started_at DESC)`, `(node_id, started_at DESC)`.
- `consumption_1m(tenant_id uuid, node_id uuid, minute timestamptz, gb numeric(10,4))` particionada por mes, índice `(tenant_id, minute)`. Rollup por minuto: la factura es por GB pero la conciliación con el gateway necesita el minuto por nodo.
- `tenants(tenant_id uuid PK, name text, stripe_customer_id text, plan text, gb_quota numeric, concurrent_limit smallint, api_key_hash bytea, created_at timestamptz)`.
- `quotas(quota_id uuid PK, tenant_id uuid FK, period_start timestamptz, period_end timestamptz, gb_used numeric, gb_limit numeric, sessions_used int, overage_policy text CHECK (overage_policy IN ('block','throttle','bill')), UNIQUE (tenant_id, period_start))`.

Redis (clave → TTL, propósito): `node:{id}:hb` TTL 15 min, latido de vida; `node:{id}:lease:{session_id}` TTL 60 s, sesión viva mantenida por el edge; `pool:exit:{country}:{asn}`, set de nodos para selección; `sticky:{tenant}:{customer_key}` TTL = TTL sticky, exit fijado; `ratelimit:{tenant}:{window}` TTL la ventana; `quota:gb:{tenant}:{period}`, contador hasta cerrar periodo.

NATS: `node.{id}.hb`, `node.{id}.state`, `node.{id}.telemetry`, `tenant.{id}.quota.exhausted`, `gateway.{edge}.sessions`, `ctl.node.{id}.command` (backend→nodo vía WG).

## 8. Contratos de API

- `POST /api/v1/agent/enroll` `{device_fp, arch, wg_pubkey, stager_id}` → 201 `{node_id, wg_config, module_base_url, manifest}`; 409 si `stager_id` ya enrollado; 400 arch desconocida.
- `GET /api/v1/agent/manifest?arch=&ver=` → 200 manifest con módulos, versiones y firmas; 304 si `ver` vigente.
- `GET /api/v1/agent/modules/{name}/{version}` → 200 `application/octet-stream` con `Accept-Ranges` para reanudar en enlaces inestables; 404 si el arch no aplica.
- `POST /api/v1/agent/{node_id}/hb` `{state, rss_kb, modules[]}` → 204; 404 obliga re-enroll; 410 retiro ordenado.
- `POST /api/v1/agent/{node_id}/retire` `{reason}` → 200; 410 si ya retirado. Endpoint de transparencia y baja del BRIEF.
- `POST /api/v1/sessions` (Bearer api key) `{country?, asn?, city?, mode, sticky_ttl}` → 201 `{session_id, endpoint, expires_in}`; 402 cuota agotada; 429 rate-limit; 403 key inválida.
- `DELETE /api/v1/sessions/{id}` → 204; 404. `GET /api/v1/cons/{tenant}/usage?from=&to=` → 200 con rollups.

Handshake agente↔gateway: el nodo abre TLS al edge y manda `HELLO{node_id, wg_ip, caps{bw_mbps, iso2, asn}, mac = HMAC(node_secret, nonce)}`; el edge responde `CHALLENGE{nonce}` y luego `PROXY_OK{mux_id}`; por sesión: `OPEN_STREAM{session_id, tenant_id}` → `STREAM_DATA` → `CLOSE_STREAM{reason}`. Con HMAC el `node_secret` no se transmite tras el enroll: una captura pasiva no da material para suplantar el nodo.

## 9. Modelo de amenaza

- **Atacante** (competidor con botnets, EDR/IDS): observa patrón de red saliente y nombres de proceso; no puede escribir en el dispositivo. La huella se asume detectable y se mitiga con tráfico saliente uniforme, no con sigilo extremo.
- **ISP**: DPI y clasificación de flujos. Todo es saliente —el nodo inicia siempre— y no hay listener entrante que perfilar; WG y mux viajan por UDP/TLS habituales, sin imitar el tráfico del dueño.
- **Dueño**: apaga, resetea de fábrica, actualiza firmware; el tope de 2–5 Mbps es la defensa de que no note el router lento y el retiro limpio es su derecho de baja.
- **Cliente**: puede abusar de los exits (scraping, spam); sesiones concurrentes topadas por tenant y contabilidad por sesión. Un cliente ruidoso no degrada a los demás porque los streams van etiquetados y el nodo va topado.

Qué **no** protege: un tenant martillando un solo exit (los límites mitigan, no eliminan); el overwrite de `/overlay` por actualización de firmware que borra los módulos —cuesta reentregar 4,7 MB, no la identidad—; la fragmentación UDP de WG con MTUs raros de algunos ISPs; y el gateway, donde el cliente ve la "IP del bot": es el objetivo de mayor valor y su escalado horizontal es problema nuestro, no del plano modular.

## 10. Plan de fases de implementación

1. **F1 Esqueleto en hueso**: compilar los 5 binarios en la matriz de 6 archs, UPX, sin red. Entregable: `file` dice "statically linked" y `ls -l` muestra tamaños ≤ presupuesto §2 en un router MIPS de 32 MB real con ping estable.
2. **F2 Enroll y malla**: enrollar 10 dispositivos reales. Entregable: IP de headscale asignada, `ping -c3` por WG desde el backend, fila `status='enrolled'` en `nodes` y heartbeat en < 5 min.
3. **F3 Plano de datos**: SOCKS5 de `botproxy` + mux + edge. Entregable: `curl -x` por el gateway devuelve la IP pública del dispositivo y deja fila en `sessions` con `bytes_in` no nulo.
4. **F4 Cuotas y contabilidad**: agotar cuota bajo carga simulada. Entregable: sesión cortada en ≤ 5 s, evento NATS consumido y `consumption_1m` cuadra con los bytes de curl en ±2 %.
5. **F5 Update en caliente y resiliencia**: servir módulo v(n+1) firmado a 100 nodos. Entregable: solo el plugin objetivo reinicia —el ping del túnel no corta—, rollout en < 48 h con rollback visible en ≥1 fallo; y 20 reinicios físicos sin systemd devuelven `botnetd` en < 90 s sin re-intrusión.

## 11. Riesgos y alternativa descartada

Top 5 riesgos con mitigación: (1) **suma de PSS > 6 MB en hardware real** —medir en F1 con PSS; telemetría ya es transitoria y el último recurso es fusionarla en `botnetd` conservando el contrato—. (2) **gost v3 no encoge** —el registro selectivo puede seguir pesado; fallback: relay SOCKS5 propio ~800 líneas dentro de `botproxy`, que el diseño confina precisamente para poder sustituirlo sin tocar nada—. (3) **8 MB de flash sin partición USB** —UPX obligatorio, arrancar desde tmpfs y no servir telemetría a esa clase de dispositivo—. (4) **matriz 6 archs × 4 módulos = 24 artefactos por release** —el CI publica el manifest con las 24 huellas; un hash fuera de matriz rompe el build, no la flota—. (5) **carreras multiproceso** —`botnetd` escribe estado y los plugins no guardan estado; test de integración como criterio de salida de cada fase—.

Lecturas rivales descartadas: **la monolítica** porque actualizar una línea del proxy obliga a reentregar el implante entero —~12 MB raw— y reiniciar la sesión de toda la flota a la vez: radio de explosión 100 %, y la sesión Sliver sola consume el presupuesto de RSS en MIPS. **La minimalista** porque el WireGuard nativo en kernel casi no existe en los firmwares objetivo —hay que llevar userspace sí o sí—, la granularidad de cron no sostiene un watchdog de 90 s ni una baja limpia, y la telemetría con estado entre beats no cabe en un script sin inventar un agente de segunda clase.

## 12. Métricas de éxito

**A 30 días**: p95 de PSS total ≤ 6 MB en ≥ 95 % de los nodos (medida por el propio heartbeat con `rss_kb`); crash-loop rate < 2 nodos/semana/1.000; ≥ 90 % de la flota enrollada con heartbeat en la última hora; rollout completo de módulo en < 48 h con < 3 % de fallo y rollback observado; embudo discovery→enrolled ≥ 70 %.

**A 90 días**: uptime p95 de nodos en ventanas de sesión ≥ 96 %; conciliación gateway↔`consumption_1m` dentro de ±1 %; latencia p50 de establecimiento de sesión < 800 ms; churn de retiro por dueño < 15 % mensual; coste de plano de control ≤ 0,03 USD/nodo activo/mes; y cero derivaciones de bytes entre tenants en auditoría: el aislamiento por stream se demuestra con contabilidad separada, no se afirma.
