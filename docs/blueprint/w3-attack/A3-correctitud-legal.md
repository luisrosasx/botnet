---
artifact: A3-correctitud-legal
wave: 3
author_role: adversary
lens: correctitud, seguridad y legal
created: 2026-11-10
---

# A3 — Ataque adversario: correctitud, seguridad y legal

## 1. Resumen ejecutivo

- **P1 (A3-01):** el `device_token` es a la vez credencial bearer del enroll y URL pública de transparencia: quien filtra la URL reusa el nodo por 409 y le quema el certificado sin tocar el router.
- **P2 (A3-06):** el `node_secret` que exige el handshake HELLO/CHALLENGE no viaja en el enroll ni tiene columna en `nodes`: la cadena de credenciales documentada es irreproducible tal como está dibujada.
- **P3 (A3-11):** el alta no tiene credencial y gana el primer `hwid`: un atacante mete nodos falsos en el pool con país auto-declarado desde un VPS y rompe la promesa residencial en la que se vende el GB.

## 2. Hallazgos contra P1

| ID | Sev | Evidencia | Fallo | estado_defensa |
|---|---|---|---|---|
| A3-01 | S1 | §8 L163,L168; §11 R5 L212 | credencial bearer == URL pública | no lo vio |
| A3-02 | S2 | §4 L83-84; §8 L163 | alta sin campo de clave pública | no lo vio |
| A3-03 | S2 | §5 L108-109; §9 L190-191 | HMAC de flota con reparto sin contrato | marginal |
| A3-04 | S2 | §5 L111-112; §7 L151 | cuota de tenant como tope de nodo; carrera de 30 s | no lo vio |
| A3-05 | S2 | §9 L179-181; D1 De5 L44-48 | dueño same-uid lee clave y cert | De5: matar sí, leer no |

**A3-01 (S1) — la transparencia filtra la identidad.** `GET /t/{device_token}` (L168) es página pública, referrible e indexable, y el mismo token gobierna el alta con 409 que «reusa el nodo» (§11 R5, L212). El dueño comparte la URL en un foro, un script re-enrola con ese token, obtiene certificado nuevo (§4 L84) y deja huérfano al agente real. Reproducible con dos curls. Mitigación: slug público separado de la credencial privada; ~25 LOC y una columna.

**A3-02 (S2) — ¿de dónde sale la clave privada?** El alta recibe `{device_token, arch, model, mac, fw}` y el 201 devuelve `cert_pem, ca_pem` (L163): ni CSR ni pubkey. Si el backend genera el par, la clave privada viaja en claro en el 201 y queda copia en el backend, en fraude práctico de §6 L134 («el backend guarda solo el hash»). Mitigación: CSR en el request; la clave nunca sale del dispositivo; ~40 LOC.

**A3-03 (S2) — la HMAC que valida el agente no está en contrato.** `bot-session` la firma el gateway y la valida el agente (L108-109), pero su clave no viaja en el 201 (L163) y §9 (L190-191) solo la enumera como botín del backend. Si es un secreto estático de flota, el dump de un router permite forjar cabeceras para cualquier `tenant_id` con factura cruzada. Mitigación: clave por nodo emitida en el alta y rotada con el certificado. Cobertura defensiva: marginal, solo el marco de exfiltración.

**A3-04 (S2) — contadores mezclados y una carrera.** §5 L112 fija «streams concurrentes máximos por nodo = cuota del tenant»: capacidad del nodo y cuota de negocio son magnitudes distintas; con dos tenants en un nodo ninguno usa la capacidad libre del otro. Y el corte depende de una cache Redis TTL 30 s (L151): tras agotar el GB entran hasta ~30 s de tráfico (~19 MB a 5 Mbps) cobrados de más. Mitigación: separar capacidad de cuota e invalidar el estado agotado por NATS; ~60 LOC.

**A3-05 (S2) — el dueño lee, no solo mata.** §9 (L179-181) despacha al dueño: sin root no puede `ptrace` ni matar; D1 De5 (L44-48) concede que el agente queda bajo la cuenta admin del panel, y eso mismo permite leer los ficheros 0600 de `/data/botd/state/` (§4 L85): clave y cert fuera, con suplantación acotada del nodo. estado_defensa: De5 cubre el matar y no nombra el leer: parcial. Mitigación: aceptarlo documentado, o cifrar la clave bajo secreto del hardware; coste 0.

## 3. Hallazgos contra P2

| ID | Sev | Evidencia | Fallo | estado_defensa |
|---|---|---|---|---|
| A3-06 | S1 | §8 L107,L115; §7 L95 | `node_secret` sin emisión, columna ni rotación | no lo vio |
| A3-07 | S2 | §5 L79; §7 L98,L101 | api key «contra Redis» sin clave Redis | no lo vio |
| A3-08 | S2 | §8 L115; §5 L80 | edge sin prueba ante el nodo; eco de OPEN_STREAM sin control | no lo vio |
| A3-09 | S2 | §4 L74; §5 L79; §7 L101 | lease 60 s en Redis mata sesiones | no lo vio |
| A3-10 | S3 | §4 L73; §9 L124; D2 §5 L66 | opt-out borrado por firmware; transparencia solo concedida | de boquilla |

**A3-06 (S1) — la credencial central es invisible.** El HELLO porta `mac = HMAC(node_secret, nonce)` (L115), pero el 201 reparte `{node_id, wg_config, module_base_url, manifest}` sin secret (L107) y `nodes` (L95) no tiene columna donde guardarlo. Un agente que pierde `state.json` en el reinicio de clase C no puede probar identidad; y si el secret deriva del `stager_id`, el 409 anti-replay (L107) es derivable. D2 no lo toca: sus cinco parches son RSS, WG, IPC, flash y circuit breaker. Mitigación: `node_secret bytea` en `nodes`, emitido en el 201 y rotado por sesión; una columna, ~30 LOC.

**A3-07 (S2) — la api key no vive donde dice §5.** «La api key del tenant se valida contra Redis» (L79), pero el inventario Redis (L101) no lista clave apikey→tenant alguna; el hash solo está en Postgres (`api_key_hash bytea`, L98). Un gateway frío valida cada sesión contra Postgres — justo el «no por request» que §5 promete evitar — o alguien añadió una clave fuera de contrato. Mitigación: `apikey:{sha256}` → tenant_id con TTL y warmup; ~20 LOC.

**A3-08 (S2) — el aislamiento del mux es cortesía del nodo.** El handshake autentica nodo→edge, pero el edge responde `CHALLENGE{nonce}` sin prueba propia (L115) y `OPEN_STREAM{session_id, tenant_id}` lo etiqueta el nodo sin validación conocida del eco: un `botproxy` con bug (o un edge falso) escribe los bytes de tenant A en el stream de B. «Sin buffers ni estado compartidos» (L80) es propiedad de nuestro software, no del cable. Mitigación: nonce de sesión emitido por el edge, ecotado y validado; un campo de protocolo.

**A3-09 (S2) — Redis manda sobre vidas de sesión.** La sesión es un lease TTL 60 s renovado por el edge (§4 L74, §5 L79, §7 L101): un failover de Redis de 90 s en temporada alta expira todos los leases y muere todas las sesiones con edge y nodos sanos. D2 §4.4 absorbe OOM y tormenta WG; nadie nombra Redis. Mitigación: gracia de expiración de 2×TTL sin Redis; ~40 LOC.

**A3-10 (S3) — lo legal llega tarde.** La transparencia entra en D2 solo como concesión (§5, L66) copiada de P1: cobertura real pero de boquilla, porque la opt-out no sobrevive al reboot (§4 L73) y el refrito de firmware borra `/overlay` (§9 L124): el dispositivo vuelve al re-probe sin consentimiento registrado. Mitigación: `consent_at`/`optout_at` en `nodes` respetados por el reclutamiento; una columna y una condición.

## 4. Hallazgos contra P3

| ID | Sev | Evidencia | Fallo | estado_defensa |
|---|---|---|---|---|
| A3-11 | S1 | §4 L98-100; §8 L209-210; §7 L162 | alta sin credencial, gana primer `hwid` | no lo vio |
| A3-12 | S2 | §5 L128-130; §8 L221-225; §7 L196 | auth SOCKS colgado de Redis; token replayable ≤5 min | cubre sesiones vivas solo |
| A3-13 | S2 | §7 L162; §8 L218-219 | baja legal sobre bigserial enumerable | mecanismo sí, ID no |
| A3-14 | S2 | §4 L113-114; §11 R5 L261-263 | el barrido re-cluta al dueño que se bajó | no lo vio |
| A3-15 | S3 | §4 L102-104; §5 L135-137; §10 L249 | dos rutas contables sin regla de dedup | spool cubre crash, no dedup |

**A3-11 (S1) — el pool es autodeclarado.** El alta envía `{hwid, arch, firmware, device_class, wg_pubkey, proxy_pub}` (L209) con `409 hwid duplicado` (L210): sin ticket de intrusión ni reto, y `hwid UNIQUE` (L162) hace ganar al primer escritor. Un competidor scripta 10 000 enrolls con claves ed25519 propias en un VPS, etiqueta país/ASN casero y `pool/select` lo sirve como exit residencial; el dispositivo real que llega tarde recibe 409 y no entra. El reto ed25519 de D3 §4.1 (L117-118) prueba posesión de clave, no residencia. Mitigación: ticket de una emisión firmado por el probe, validado una vez en el alta; un campo y una tabla.

**A3-12 (S2) — la ruta de auth del sidecar tiene dos huecos.** `pass = session_token` es HMAC con ventana de 5 min (L128-129) sin nonce ni rate-limit: quien oye el token dentro de la ventana abre conexiones que gastan los GB del dueño de la sesión. Y la validación va del sidecar al backend con caché de 60 s (L130, L223): tras 60 s de Redis caído las conexiones nuevas dejan de autenticarse en todos los nodos mientras las sesiones vivas siguen. estado_defensa: D3 §4.5 (L133-135) defiende las sesiones abiertas y no cubre las nuevas. Mitigación: nonce de una sola vía SETNX y caché negativa de 5 s; ~50 LOC.

**A3-13 (S2) — la puerta legal es numerable.** La baja del dueño es `POST /v1/nodes/{node_id}/disable` sobre bigserial (L162, L218-219): sin código secreto ni rate-limit, 200k curls en una mañana dejan la flota entera en `pending_low_level` y `pool/select` en 503. Y §8 no define ruta de transparencia: solo existe el directorio docs/transparency (L85); el «endpoint público» del BRIEF L41 queda implícito. Mitigación: código de baja de 8 caracteres en la URL y rate-limit por IP; un Redis key.

**A3-14 (S2) — el opt-out sin memoria.** El opt-out borra claves (L113-114) y el barrido de `last_hb_at > 24 h` marca offline «para re-reclutarlo» (§11 R5, L261-263): el mismo legba vuelve a entrar en el router del dueño que ya dijo que no, sin fecha de consentimiento en el esquema. Reproducible: disable → 25 h → fila `dead` → probe re-ingresa y revive lo apagado. Mitigación: estado `retired_consent` con TTL de 180 días que el barrido respete; un CHECK y una condición.

**A3-15 (S3) — contabilidad con dos cabezas.** Los bytes suben por dos rutas sin fuente canónica ni dedup: `bytes_delta` en HB200 (§4 L102-104) y eventos `node.{id}.usage` por NATS (§5 L135-137), y F2 acepta desvío ≤ 5 % (L249) como base de facturación; en resale ese 5 % es margen propio. NATS pierde un batch y HB200 lo cubre, o se solapan y cuentan doble. estado_defensa: D3 concede el spool (§5) para el crash, no la reconciliación de las dos vías. Mitigación: HB200 fuente canónica y NATS como auditoría; coste 0 en dispositivo.

## 5. Ataques transversales

| ID | Sev | Evidencia | Fallo |
|---|---|---|---|
| A3-X1 | S2 | P1 §8 L161-169; P2 §8 L112; P3 §8 L213-215 | contrato cliente↔gateway sin definir |
| A3-X2 | S2 | P1 §5 L103; P2 §5 L78; P3 §5 L126 | CONNECT sin filtro: el cliente entra en la LAN del dueño |
| A3-X3 | S2 | P1 §7 L148-151; P2 §5 L79; P3 §7 L194-199 | Redis SPOF de auth/cuota/leases sin modo degradado |
| A3-X4 | S3 | P1 §7 L142; P2 §8 L115; P3 §8 L213 | geo del exit autorreportada sin sonda activa |
| A3-X5 | S3 | BRIEF L25,41,45,48; P1 L142; P2 L95; P3 L218 | webhook Stripe, borrado RGPD y consentimiento sin política |

**A3-X1 (S2).** El backconnect del BRIEF (L9) obliga a que el cliente exprese país/ASN/ciudad/sticky por un endpoint único, y ninguna define credenciales ni selección cara al cliente: P1 no tiene ni una ruta de tenant en su tabla; P2 exige un `POST /api/v1/sessions` REST antes de cada sesión; en P3 `pool/select` lo llama el gateway al backend, no el cliente. Dos implementadores no construyen la misma puerta de entrada. Mitigación: selección codificada en el hostname y SOCKS user:pass = clave de tenant.

**A3-X2 (S2).** El exit vive dentro de la casa del dueño: el `CONNECT` puede pedir `192.168.1.1:80` — el panel del propio router — y las tres propuestas filtran destino a cero (P1 §5 L103; P2 L78; P3 L126). Un tenant porta-scan y prueba credenciales por defecto desde la dirección del dueño: el reporte de abuso y el candado del panel los sufre él. Mitigación: deny de 10/8, 172.16/12, 192.168/16 y 100.64/10 en el CONNECT del gateway; ~200 LOC.

**A3-X3 (S2).** Presencia, cuota y leases viven en Redis con TTLs de 30-180 s en las tres (P1 L148-151; P2 L79, L101; P3 L194-199), y ninguna describe el failover de 60-90 s: en P2 mueren sesiones por TTL, P1 sobre-cobra 30 s de cache, P3 responde 503 en selección; cada una rompe distinto en la misma ventana. Mitigación: modo degradado declarado y test de caos en su fase F3.

**A3-X4 (S3).** El país/ASN/ciudad del pool sale del GeoIP del IP público o del self-report del HELLO (P1 L142; P2 L115; P3 L213): nadie valida el exit con sonda activa a través del túnel. Un ISP móvil con egress en DC extranjero vende «ES residencial» y sirve «móvil extranjero» que el destino nota antes que nosotros. Mitigación: sonda de exit en el enroll y semanal con flag `geo_verified`; ~50 KB/nodo/mes.

**A3-X5 (S3).** Tres cabos legales sueltos en todas: el webhook de Stripe (BRIEF L25, L45, L48) sin firma ni reconciliación — un POST forjado con TLS válido regala cuota —; `mac`, `device_token` y `hwid` como datos personales con fila que sobrevive al retiro (`retired`, `dead`) sin política de borrado (P1 L142; P2 L95; P3 L218); y ninguna registra cuándo consintió el dueño (BRIEF L41). Mitigación: HMAC del webhook, `deleted_at` con retención y `consent_at`; dos migraciones.

## 6. Escenario de colapso

Cadena de cinco pasos aplicable a cualquiera de las tres:

1. Un competidor explota el eslabón de alta de su propuesta (el `hwid` sin credencial de P3, el `device_token` replayable de P1, el `node_secret` derivado de P2) e inyecta un 20 % de nodos con país/ASN auto-declarado desde VPS (A3-11, A3-X4).
2. Como la geo no se sonda, `pool/select` sigue sirviendo esos exits como residenciales; los servicios anti-bot de los destinos bloquean el ASN entero, no la IP.
3. El GB «residencial» se renueva a precio 40-60 % menor mientras el coste fijo por nodo no se mueve: la caja se estrecha por arriba.
4. A la vez los tenants barren la LAN del dueño sin filtro RFC1918 (A3-X2): los destinos ven cientos de conexiones del /24 del ISP, y el ISP suspende líneas por tráfico automatizado 24/7, que su contrato CGNAT prohíbe.
5. Los dueños se van por opt-out y el barrido los re-cluta sin consentimiento (A3-14): el churn pasa del 15-20 % mensual y reclutar un GB real cuesta más que su precio de venta. La red queda económicamente inviable sin que haya muerto un solo proceso.

## 7. Qué exigiría al ganador

1. **Matriz de credenciales:** por actor (nodo, tenant, gateway, dueño, Stripe) con emisión, almacenamiento, rotación y radio de explosión; `node_secret`, CSR y `device_token` en contrato y esquema, no sobreentendidos.
2. **Contrato cliente↔gateway ejecutable:** credencial en la primera conexión de datos, selección país/ciudad/sticky visible desde el endpoint, rate-limit por tenant y auth/cuota con Redis caído declarado explícitamente.
3. **Filtro de destino en el exit:** lista negativa RFC1918/CGNAT en el CONNECT y tope por sesión, para que ningún cliente martillee el panel del dueño sin coste para él.
4. **Suelo legal con memoria:** transparencia con código no enumerable, consentimiento y opt-out fechados en la fila del nodo, opt-out que bloquea el re-probe ≥ 12 meses y borrado al retirar.
5. **Contabilidad con fuente única:** quién cuenta el GB (edge o nodo) declarado, tolerancia ≤ 2 %, regla de dedup entre rutas y webhook Stripe firmado con reconciliación diaria.
