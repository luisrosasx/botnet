---
artifact: V3-juicio-correctitud-legal
wave: 4
author_role: judge
closes: "correctitud, seguridad y legal"
created: 2026-11-10
---

# V3 — Veredicto del juez J3: lente de correctitud, seguridad y legal

Juez único de la lente A3. Pontúo las tres propuestas con los seis criterios del WAVE-PLAN
(`WAVE-PLAN.md:26`), aplico el techo por S1 sin mitigar y cierro los 20 hallazgos de
`w3-attack/A3-correctitud-legal.md` (A3-01…A3-15 y A3-X1…A3-X5). Evidencia citada como
`fichero:línea`; todas las rutas relativas a `docs/blueprint/`.

## 1. Matriz de puntuación

Notas 0-10 por celda, dura y con evidencia de disco. Suma ponderada = Σ(nota × peso)/10, sobre 100.

| Criterio (peso) | P1 frugal | P2 modular | P3 thin+sidecar |
|---|---|---|---|
| Viabilidad en hardware pobre (20) — **8 / 7.5 / 6.5** | **8**: RSS pico post-parche ≤4,6 MB y flash 3,8 MB sobre los topes 6 MB/8 MB (`D1-defense.md:60,L64`); uniproceso reejecutado, sin segundo binario residente (`P1-frugal.md:89-91`). | **7.5**: PSS reposo 4,2 MB y pico 5.0 MB tras Parche 1 (`D2-defense.md:29-30`); clase C 3,6 MB (`D2-defense.md:31`); margen de pico solo 17 % (`D2-defense.md:30`). | **6.5**: total residente ≤9,5 MB (`D3-defense.md:63`) contra el tope ≤6 MB del BRIEF (`BRIEF-BASE.md:37`); D1 lo califica de «casi todo el hueco del dueño» (`D1-defense.md:88-89`). |
| Robustez frente a ataques (20) — **6.5 / 6 / 5** | **6.5**: De1-De5 cierran heap y presupuesto (`D1-defense.md:19-49`), pero ningún parche toca la cadena de credenciales: el S1 A3-01 sobrevive y At6 solo nombra la transparencia (`D1-defense.md:125`). | **6**: circuit breaker y `ipc_ver` contienen fallos (`D2-defense.md:19,L23`), pero el S1 A3-06 (origen del `node_secret`) no lo toca ningún parche y A3-10 es concesión de boquilla (`D2-defense.md:66`) — penalizada. | **5**: el guardián de memoria y la salida de NATS mejoran el OOM (`D3-defense.md:38-40,L52-54`), pero el S1 A3-11 ataca la promesa residencial y el reto ed25519 solo prueba posesión de clave (`D3-defense.md:117-118`). |
| Mantenibilidad a 12 meses (15) — **6.5 / 7 / 7.5** | **6.5**: el uniproceso es simple, pero P1 se escribe su propio túnel H2, handshake `OPEN_SESSION` y servicio de rendezvous (`P1-frugal.md:127-131`) — D3: «el mayor volumen de código propio del sistema» (`D3-defense.md:85-88`). | **7**: 24 artefactos por release son maquinaria (`P2-modular.md:136`) pero el grain por plugin con `ipc_ver` versionado madura bien (`D2-defense.md:19`). | **7.5**: dos binarios, transporte WG estándar (`P3-plano-desacoplado.md:145`), codec HB200 fijo (`P3-plano-desacoplado.md:102-104`); D3: binomio agente+proxy = 12 artefactos (`D3-defense.md:110`). |
| Coste operativo (15) — **9 / 7 / 5.5** | **9**: 185 USD/mes por 1.000 nodos = 0,185/nodo (`D1-defense.md:65`), sin headscale ni CA externa en el dispositivo. | **7**: plano de control 0,025 USD/nodo (`P2-modular.md:27`), pero firma ed25519 en KMS (`P2-modular.md:60`) y toolchain Rust/musl extra (`D2-defense.md:15`) gravan la operación. | **5.5**: ~0,58-0,60 USD/nodo/mes por 8 gateways + hub headscale (`P3-plano-desacoplado.md:44-47`); su §12 promete ≤0,40 y se contradice a sí misma (`D2-defense.md:51`). |
| Cobertura de reclutamiento (15) — **7.5 / 7.5 / 7** | **7.5**: matriz de 5 archs con binario upx 2,6 MB y slot único cabe en clase C (`D1-defense.md:64,L33-35`); TLS sobre 443 no depende de `tun` ni de WG en kernel. | **7.5**: bundle mínimo clase C 3,1 MB con WG relegado (`D2-defense.md:21`, Parche 2 `D2-defense.md:17`); la matriz llega a 6 archs (`P2-modular.md:67`). | **7**: agente 1,6 MB + proxy a `/tmp` (`P3-plano-desacoplado.md:27`), pero D3 D4 recorta hasta −60 % del ancho vendible en la clase MIPS débil (`D3-defense.md:46-47`) — se recluta mucho y se vende menos. |
| Time-to-first-paying-customer (15) — **7.5 / 6 / 7** | **7.5**: F1 compila y late en QEMU; F2 da SOCKS5 e2e por el gateway; Stripe en F5 (`P1-frugal.md:198-202`) — el camino más corto a un cobro real. | **6**: F1 exige 5 binarios × 6 archs y F2 la malla headscale con ping WG antes del primer dato (`P2-modular.md:128-129`). | **7**: F0 headscale+agente+ingestor y F1 `curl -x socks5h://…` por malla (`P3-plano-desacoplado.md:247-248`) — rápido, pero el hub hay que montarlo antes que nada. |

**Sumas ponderadas sobre 100 (antes del techo): P1 = 74,75 · P2 = 68,25 · P3 = 63,50.**
Desglose: P1 = 16+13+9,75+13,5+11,25+11,25 · P2 = 15+12+10,5+10,5+11,25+9 ·
P3 = 13+10+11,25+8,25+10,5+10,5.

## 2. Techo por hallazgo S1 sin resolver

La tabla de la propia A3 marca `estado_defensa: «no lo vio»` en los tres S1, y como juez lo
confirmo contra las defensas: ningún parche de D1, D2 ni D3 mitiga el S1 de su propuesta. El techo
a 60 es innegociable y **cae sobre las tres**:

| Propuesta | S1 superviviente | Nota antes del techo | Nota final tras el techo |
|---|---|---|---|
| P1 | A3-01: `device_token` es credencial bearer y URL pública a la vez (`P1-frugal.md:168,L212`); D1 solo la nombra (`D1-defense.md:125`) | 74,75 | **60** |
| P2 | A3-06: `node_secret` sin emisión, columna ni rotación (`P2-modular.md:107,L115,L95`); los 5 parches de D2 (`D2-defense.md:15-23`) van de RSS, WG, IPC, flash y breaker — de credenciales, nada | 68,25 | **60** |
| P3 | A3-11: alta sin credencial de ingreso, gana el primer `hwid` (`P3-plano-desacoplado.md:209-210,L162`); el reto ed25519 de D3 no prueba residencia (`D3-defense.md:117-118`) | 63,50 | **60** |

Con el techo puesto, el ranking no puede salir del empate: lo resuelven la suma ponderada previa y,
si hiciera falta, los desempates reglamentarios — Robustez (6,5 > 6 > 5) y Viabilidad en hardware
pobre (8 > 7,5 > 6,5) —, que ordenan igual. No hay empate triple posible.

## 3. Cierre de hallazgos de tu lente

Regla aplicada: `resuelto` exige un parche de defensa que cubra el hallazgo; `mitigado` débil
cuando solo hay cobertura parcial, marginal o «de boquilla» (con penalización en la matriz);
`descartado` solo si el hallazgo es erróneo.

| ID | Sev | Veredicto | Prueba |
|---|---|---|---|
| A3-01 | S1 | mitigado (débil) | D1 At6 (`D1-defense.md:125`) nombra `GET /t/{device_token}` pero no separa credencial de URL pública; la colisión con el 409 de re-enroll (`P1-frugal.md:212`) sigue en pie. Solo existe la mitigación del propio atacante (slug público, ~25 LOC). Detona el techo de P1. |
| A3-02 | S2 | mitigado (débil) | Ninguna defensa introduce CSR; el 201 sigue devolviendo `cert_pem, ca_pem` sin clave pública en el alta (`P1-frugal.md:163`) y la clave privada viajaría en claro, en fraude del «solo guarda el hash» (`P1-frugal.md:134`). TASK W5: CSR en el enroll (~40 LOC). |
| A3-03 | S2 | mitigado | §9 de P1 enumera la HMAC de flota como botín de exfiltración (`P1-frugal.md:190-191`) y D1 hereda ese marco sin contrato de reparto (`D1-defense.md:125`): cobertura marginal, tal como dictaminó A3. TASK W5: clave HMAC por nodo rotada con el certificado. |
| A3-04 | S2 | mitigado | El parche De2 fija admission control = mínimo entre 150 streams y la cuota concurrente del tenant (`D1-defense.md:30-31`): separa capacidad de nodo solo a medias (el `min` sigue mezclando magnitudes) y la carrera del cache TTL 30 s sobre ~30 s de tráfico cobrado de más (`P1-frugal.md:151`) queda abierta. |
| A3-05 | S2 | mitigado | De5 concede que el dueño same-uid mata procesos y responde con cron guard (`D1-defense.md:44-49`): cubre el matar, no el leer los ficheros 0600 de `/data/botd/state/` (`P1-frugal.md:85`). Aceptable como riesgo documentado — la propia A3 propone «aceptarlo documentado» —; cifrado bajo secreto de hardware queda como TASK coste 0. |
| A3-06 | S1 | mitigado (débil) | Ninguno de los cinco parches de D2 toca la emisión del `node_secret` (`D2-defense.md:15-23`); D2 lo cita como identidad existente en su refutación (`D2-defense.md:45`) sin darle columna (`P2-modular.md:95`) ni ruta de emisión (201 sin secret, `P2-modular.md:107`). Solo la mitigación del atacante (columna + emisión + rotación, ~30 LOC). Detona el techo de P2. |
| A3-07 | S2 | mitigado (débil) | Parche 3 añade `ipc_ver` al HELLO (`D2-defense.md:19`) pero ninguna defensa crea la clave Redis apikey→tenant que §5 exige (`P2-modular.md:79`) y el inventario Redis no la lista (`P2-modular.md:101`). TASK W5: `apikey:{sha256}` + warmup (~20 LOC). |
| A3-08 | S2 | mitigado (débil) | Nadie cubre la ausencia de prueba del edge ante el nodo ni el eco sin validar de `OPEN_STREAM` (`P2-modular.md:115,L80`); `ipc_ver` es versión, no autenticación. TASK W5: nonce de sesión ecotado — un campo de protocolo. |
| A3-09 | S2 | mitigado | D2 §4.4-4.5 absorbe tormenta WG y OOM citando el lease TTL 60 s como amortiguador (`D2-defense.md:58-59`), pero nadie nombra el failover de Redis de 60-90 s que expira todos los leases con el resto sano (`P2-modular.md:74,L101`). TASK W5: gracia de 2×TTL sin Redis. |
| A3-10 | S3 | mitigado (débil — boquilla, penalizada) | La transparencia entra en D2 solo copiando la página de P1 (`D2-defense.md:66`, la concesión), justo la «de boquilla» que dictaminó A3; la opt-out no sobrevive al reboot (`P2-modular.md:73`) ni al refrito de firmware (`P2-modular.md:124`) y no hay consentimiento registrado. Penaliza la nota de Robustez de P2. |
| A3-11 | S1 | mitigado (débil) | D3 §4.1 responde con el reto ed25519 del enroll (`D3-defense.md:117-118`): prueba posesión de clave, no residencia, y no aporta ticket de intrusión frente al `hwid UNIQUE` que da victoria al primer escritor (`P3-plano-desacoplado.md:162,L210`). El pool autodeclarado desde VPS sigue vivo. Detona el techo de P3. |
| A3-12 | S2 | mitigado | D3 §4.5 defiende las sesiones vivas con la caché de 60 s (`D3-defense.md:133-135`), tal como reconoce A3; quedan abiertos el replay del token en la ventana de 5 min sin nonce (`P3-plano-desacoplado.md:128-129`) y el cese de auth de conexiones nuevas con Redis caído. TASK W5: nonce SETNX + caché negativa. |
| A3-13 | S2 | mitigado | D3 §4.6 sí define el mecanismo de baja por heartbeat, a prueba de CGNAT (`D3-defense.md:137-142`): cubre el «cómo» de la puerta legal. No cubre lo que A3 señala: `node_id` bigserial enumerable sin código secreto ni rate-limit (`P3-plano-desacoplado.md:162,L218`) y ruta de transparencia implícita. TASK W5: código de baja + rate-limit. |
| A3-14 | S2 | mitigado (débil) | D3 §4.6 convierte el barrido `last_hb_at > 24 h` en virtud («recluta al zombie», `D3-defense.md:141-142`) justo donde A3 ve el vicio: re-cluta sin consentimiento al dueño que se bajó (`P3-plano-desacoplado.md:113-114,L261-263`). No hay estado `retired_consent` propuesto por el defensor. |
| A3-15 | S3 | mitigado | La concesión del spool (`D3-defense.md:153-156`) cubre el crash entre flushes, no la reconciliación con dedup entre `bytes_delta` HB200 (`P3-plano-desacoplado.md:102-104`) y `node.{id}.usage` NATS (`P3-plano-desacoplado.md:135-137`) con desvío ≤5 % facturable (`P3-plano-desacoplado.md:249`). TASK W5: fuente canónica HB200, NATS auditoría. |
| A3-X1 | S2 | mitigado (débil) | Solo P2 tiene contrato cliente↔gateway: `POST /api/v1/sessions` con api key y selección país/ASN/ciudad/sticky (`P2-modular.md:112`); P1 no tiene ni una ruta de tenant (`P1-frugal.md:161-169`) y el `pool/select` de P3 lo llama el gateway, no el cliente (`P3-plano-desacoplado.md:213`). Se cierra por injerto del contrato de P2 en la ganadora. |
| A3-X2 | S2 | mitigado (débil) | Cero cobertura defensora: ninguna filtra destino del CONNECT (`P1-frugal.md:103`, `P2-modular.md:78`, `P3-plano-desacoplado.md:126`) y ningún defensor nombra la LAN del dueño. El hallazgo es válido y sobrevive; la deny-list RFC1918/CGNAT del atacante (~200 LOC) va como TASK obligatoria W5. |
| A3-X3 | S2 | mitigado (débil) | Cada propuesta tiene un modo degradado parcial y distinto, nadie el failover de 60-90 s: P1 sobre-cobra con su cache 30 s (`P1-frugal.md:151`), P2 mata leases (`P2-modular.md:101`), P3 valida con caché de 60 s (`D3-defense.md:133-135`). TASK W5: modo degradado declarado + test de caos en F3. |
| A3-X4 | S3 | mitigado (débil) | La geo sigue autorreportada en las tres (`P1-frugal.md:142`, `P2-modular.md:115`, `P3-plano-desacoplado.md:213`); D3 D4 siembra ancho por medición (`D3-defense.md:46-47`) pero no geo. TASK W5: sonda de exit con flag `geo_verified`. Crítico si el S1 de P3 o el de P1 se explotan combinados. |
| A3-X5 | S3 | mitigado (débil) | Ninguna defensa toca webhook Stripe firmado, borrado RGPD ni consentimiento: `nodes` de P1 (`P1-frugal.md:142`), P2 (`P2-modular.md:95`) y P3 (`P3-plano-desacoplado.md:218`) conservan filas de retirados sin `deleted_at` ni `consent_at`. TASK W5: dos migraciones; el suelo legal con memoria va en BLUEPRINT. |

Recuento: **0 resueltos, 20 mitigados (11 de ellos «débil», 1 explícitamente de boquilla),
0 descartados.** El peso de la lente cae entero sobre las defensas: ningún S1 encontró parche.

## 4. Ranking

1. **P1 — frugal.** Gana la matriz antes del techo (74,75) porque es la única que vive dentro de
   los topes del BRIEF sin prestar la identidad a un hub: encaja en 32 MB con 4,6 MB de pico y
   cobra 0,185/nodo, y su S1 es el más barato de reparar del sistema (~25 LOC).
2. **P2 — modular.** Segundo (68,25): su grano de release y sus secretos por nodo son el modelo de
   credenciales más sano de las tres, pero paga la maquinaria (24 artefactos, KMS, Rust extra),
   su S1 deja sin origen a la credencial central y su única jugada legal fue una concesión de
   boquilla penalizada.
3. **P3 — thin+sidecar.** Tercera (63,50): el desacople de fallos es real y elegante, pero lo paga
   en la moneda más cara — 9,5 MB de RSS residente, 0,58-0,60 USD/nodo y cripto WG userspace en el
   MIPS que recluta — y su S1 ataca la promesa residencial que sostiene la venta del GB.

Desempates reglamentarios confirmados: Robustez (P1 6,5 > P2 6 > P3 5) y hardware pobre
(P1 8 > P2 7,5 > P3 6,5) ordenan igual que la suma ponderada; no hay empate triple.

## 5. Injertos obligatorios

La ganadora es P1 y necesita piezas de las rivales para aprobar desde esta lente:

1. `[injerto: P2 — secreto de sesión por nodo en esquema: columna + emisión en el alta + rotación con el certificado — porque la HMAC de flota de P1 (A3-03) es un secreto estático compartido y volcarlo de un router permite forjar sesiones de cualquier tenant con factura cruzada; P2 ya modela credenciales por nodo en contrato.]`
2. `[injerto: P2 — contrato cliente↔gateway estilo POST /api/v1/sessions (P2-modular.md:112) — porque P1 no tiene ni una ruta cara al cliente (A3-X1) y el backconnect del BRIEF (BRIEF-BASE.md:9) exige que el cliente exprese país/ASN/ciudad/sticky por un endpoint único; sin esta pieza dos implementadores no construyen la misma puerta.]`
3. `[injerto: P3 — baja por pull sin router vivo: el opt-out viaja en la respuesta del heartbeat con flag `bye` y borrado de claves (P3-plano-desacoplado.md:113-114, D3-defense.md:137-142) — porque el `POST /v1/nodes/{id}/leave` de P1 (P1-frugal.md:167) presupone router alcanzable y la baja del dueño del BRIEF (BRIEF-BASE.md:41) no puede depender de que el router siga vivo.]`
4. `[injerto: P2 — firma ed25519 del binario self-updated con raíz fijada en build (P2-modular.md:60; D1 ya la concedió en D1-defense.md:131-133) — porque la cadena de P1 es solo sha256 (P1-frugal.md:96): un MITM en la ruta de update sirve el binario que él mismo hasheó; es correctitud de suministro, no solo operación.]`
5. `[injerto: P3 — el suelo legal con memoria como pieza de primera clase: consentimiento y baja fechados en la fila del nodo y directorio de transparencia (P3-plano-desacoplado.md:85; estados y barrido de P3-plano-desacoplado.md:165,L261-263) — porque P1 solo tiene un endpoint y ninguna política de consentimiento, re-cluta o borrado (A3-X5), y W5 necesita dónde escribirlo.]`

Ganadora clara: sí (P1), y estos cinco injertos son condición de aprobación, no sugerencia.

## 6. Riesgo residual del ganador

Sobre P1 inyectada, los tres riesgos que sobreviven incluso con los injertos:

1. **El túnel H2 propio + handshake a medida + servicio de rendezvous**: P1 se escribe su propio
   plano de control (`P1-frugal.md:127-131,L171-176`), el mayor volumen de código propio del
   sistema (`D3-defense.md:85-88`), y en el heap único un bug de framing tumba datos y control a la
   vez. Lo haría aceptable que la puerta F2 de D1 (`D1-defense.md:151-153`) pase en MIPS reales:
   50 streams sobre una conexión, P99 < 1,5 s y `streams_saturated` < 10 % en soak de 7 días.
2. **El dueño same-uid lee clave y certificado, y el DPI correlaciona volumen↔sesión** (A3-05,
   `P1-frugal.md:179-181,L192`): sin root y sin secreto de hardware no hay defensa prometedora.
   Lo haría aceptable mantener el churn de bajas limpias < 5 %/mes y quemados < 5 %/mes
   (`P1-frugal.md:229-230`) y cifrar la clave bajo secreto del hardware solo si el campo prueba
   el robo; la exposición real es un certificado de 90 días revocable.
3. **CA de flota con certificado por nodo renovado cada 90 días en aparatos que reinician solos**
   (`P1-frugal.md:85`, crítica razonada de `D2-defense.md:45`): CA online, almacén sin root y
   revocación son tres superficies vivas sobre el hardware más pobre. Lo haría aceptable que la
   renovación viaje por el túnel existente sin endpoints nuevos y que el 409 de re-enroll
   (`P1-frugal.md:212`) absorba el reset de fábrica; si F3 muestra churn de renovaciones, el
   plan B es el injerto de identidad ed25519 sin CA al estilo P3
   (`P3-plano-desacoplado.md:92-93`).

## 7. Condena

**P3 no debe ganar.** Compra la promesa residencial con la moneda más cara: 9,5 MB de RSS donde el
BRIEF manda ≤6, ~0,60 USD/nodo donde P1 cobra 0,185, y su §12 se contradice prometiendo ≤0,40
(`P3-plano-desacoplado.md:47,L287`). Su S1 no es un detalle: un pool autodeclarado desde un VPS
rompe exactamente lo que se vende por GB. Y paga cripto WG userspace en el MIPS más pobre — el que
recluta — recortando hasta un 60 % del ancho vendible (`D3-defense.md:46-47`). Que sea la más
maintenible no la hace ganadora: aquí mantiene cara una arquitectura que en 32 MB reales puede no
caber.
