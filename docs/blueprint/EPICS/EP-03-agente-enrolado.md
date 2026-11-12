---
artifact: epic
id: EP-03
title: "Agente: esqueleto, enrollado, entitlements y credenciales por nodo"
owner_layer: agent
closes: A3-01, A3-06, A3-11, injerto 8
estimate_days: 7
risk: high
phase: F1
---

## Objetivo

Dejar en pie el binario único `botd` (agente + SOCKS5 + watchdog reexec) con su enrollado con CSR y su trío de credenciales por nodo: certificado de 90 días firmado por la CA offline, `node_secret` de 32 B con columna propia, y `device_token` en cabecera — nunca en la URL. Es condición de aprobación: aquí mueren los S1 A3-01 y A3-06 y el S1 heredado A3-11.

## Alcance

- `cmd/botd` con arranque por estados (`node.json` → agente; si no, enrollado), `GOMEMLIMIT=5MiB`, `GOGC=40`, `GOMAXPROCS=1` y supervisor in-process con `recover()`.
- `POST /v1/enroll` con `X-Device-Token` en cabecera y body `{arch, model, mac, fw, csr}`; `201 {node_id, slug, cert_pem, ca_pem, node_secret, rendezvous[], ttl_s}`; `409` idempotente de re-enroll.
- `node_secret` (injerto 8): emisión única en el `201`, estado 0600, rotación con el certificado a 90 días.
- Slug público de 12 caracteres para transparencia, separado de la credencial.
- Puerta de alta: ticket de intrusión firmado por el probe + `geo_verified` (anti-pool-autodeclarado, A3-11).
- Entitlements versionados por `config_rev`: `bandwidth_cap_kbps`, `monthly_gb_cap`, tope de streams, `stable_version`.

## Fuera de alcance

- El transporte que lleva el heartbeat (EP-04) y el codec (EP-04): en F1 el enroll y el latido corren contra un stub HTTPS.
- Tabla `node_secrets` y CA: el lado backend vive en EP-08; esta épica fija el contrato del agente.
- Watchdog como proceso (EP-05) y self-update (EP-14).

## Hallazgos que cierra

- A3-01 (S1): credencial bearer separada de la URL pública (slug); el `409` ya no deja huérfano al agente.
- A3-06 (S1): emisión, columna y rotación del `node_secret` — la HMAC por nodo nace con origen.
- A3-11 (S1 de P3, mandado al ganador): el alta no la gana el primer `hwid`, sino un ticket de intrusión validado.
- Injerto 8 y A3-03: `node_secret` rota con el certificado; la HMAC de flota estática muere.

## Criterios de aceptación de la épica

1. `QEMU=<arch> ./botd --stage` tras entrega EP-02 → log `HEARTBEAT 200 ok` contra stub en < 60 s y `ps -o rss= -p $(pidof botd)` ≤ 3.1 MB en reposo.
2. `curl -s -X POST -H "X-Device-Token: $TOKEN" .../v1/enroll -d @csr.json` → `201` con las 7 claves del contrato; repetir el mismo token → `409`; el body del `201` es el único lugar donde viaja `node_secret` (capture mitmproxy: 0 ocurrencias en cualquier otro request posterior).
3. `GET /t/{slug}` → `200` con 12 caracteres alfanuméricos; `grep -c "$TOKEN" access.log` = 0 (el token nunca aparece en URL ni logs).
4. Alta con ticket forjado → `403`; ticket usado dos veces → `409`; `nodes.geo_verified` refleja la sonda (EP-07) en < 7 días.

## Dependencias

EP-02 entrega el binario en el dispositivo. La CA offline y la tabla `node_secrets` las materializa EP-08 (migrations). EP-04 consume `rendezvous[]` del `201`.

## Definición de hecho

Un QEMU por las 5 archs de F1 llega a `live` con un certificado propio, un `node_secret` propio y una slug propia; volcar ese router (bin+estado) no otorga credencial válida para otro nodo, y compartir la página pública no filtra ninguna credencial.
