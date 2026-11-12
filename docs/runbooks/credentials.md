---
artifact: runbook
epic: EP-08
created: 2026-11-14
---

# Matriz de credenciales (exigencia A3) — T-0802

Cada credencial del sistema con su emisor, almacen, rotacion y radio de
exposicion. La regla madre: **la clave privada nace en el dispositivo y
no sale de el**; el `node_secret` sale una sola vez (el 201 del enroll)
y rota con el certificado.

| Credencial | Emisor | Almacen en reposo | Rotacion | Radio si se filtra |
|---|---|---|---|---|
| Par clave/cert del nodo (CSR) | generado en el dispositivo; lo firma la **CA offline** (signer) | clave: `/data/botd` modo 0600 del router; cert: mismo fichero | renueva por el tunel; cert TTL **90 d** | quien la tenga suplantia **ese nodo** (mTLS del tunel), no la flota |
| `node_secret` (32 B, HMAC por nodo) | backend en el `201` del enroll (emision unica) | Postgres `node_secrets.secret` (bytea, `octet_length=32`) + `/data/botd` 0600 del nodo | rota **con el certificado a 90 d**; la baja revoca cert y secreto a la vez | forjar `bot-session` de **ese nodo** (A3-03: ya no de toda la red como con HMAC de flota) |
| `device_token` | sonda de intrusion (EP-02) al confirmar dispositivo | hash sha256 en `nodes.device_token_hash` (la cadena solo viaja en cabecera `X-Device-Token`, A3-01) | se renueva con re-sondeo | el portador puede intentarlo de alta **ese token** (gana quien porta ticket) |
| `X-Intrusion-Ticket` | sonda con HMAC secreto compartido con el backend (`TICKET_SECRET`) | Redis `ticket:{token_hash}` TTL 90 d (consumo unico) | un solo uso por token | re-emision: la sonda vuelve a sondear; el 409 avisa del consumo |
| CA de flota (clave) | ceremonia offline | signer del backend (`CA_KEY_PEM`), 0600 | 10 anos; rotar exige re-entregar raiz (decidido en F4) | firmar CSres nuevos: certificados **validos de flota** sin revocar los viejos |
| Api key del tenant | panel/alta comercial | hash sha256 en `api_keys.key_hash`; cache `apikey:{sha256}` Redis TTL 300 s | revocable (`status='revoked'`) sin tocar el tenant | abrir sesiones **de ese tenant** (402/429 siguen aplicando) |
| Token de administracion | despliegue (`API_ADMIN_TOKEN`) | entorno del backend/gateway | por release | `POST /v1/nodes/{id}/revoke` (quemar nodos) |
| Raiz ed25519 del self-update | release firmada (EP-14) | fijada en build del binario | con release | servir binarios al nodo (mitm ya no vale: injerto 11) |

## Cadena del enroll (orden de gates, contract-test incluido)

1. `X-Device-Token` conocido → **409** (el re-enroll no huerfa al agente).
2. Falta `X-Intrusion-Ticket` → **428**.
3. Ticket forjado o de otro token → **403**.
4. Ticket ya consumido → **409**.
5. CSR con firma forjada → **400**; CN fuera de politica (`botnet-node`) → **422**.
6. `201`: `{node_id, slug, cert_pem, ca_pem, node_secret, rendezvous, ttl_s}`
   — el `node_secret` (32 B) viaja **una sola vez**; `node_secrets` guarda
   su fila con `issued_at=now`, `expires_at=+90 d` y el UNIQUE parcial
   garantiza el secreto vivo unico (T-0801 AC2).

Verificacion: `go test ./cmd/botnet-api/handlers/` (openssl real en el
test de cadena; TTL ±1 d). La clave privada jamas se loguea: grep
`BEGIN PRIVATE KEY` sobre el buffer de log del backend = 0.
