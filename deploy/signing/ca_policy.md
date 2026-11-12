---
artifact: signing
epic: EP-08
created: 2026-11-14
---

# Politica de la CA de flota (offline) — T-0802

## Ceremonia

- La CA de flota nace **offline**: par ECDSA P-256 (o superior) generado
  en la ceremonia, cert self-signed `CN=BOT.NET fleet CA`, `IsCA`,
  `KeyUsageCertSign|CRLSign`, vida 10 anos.
- La clave privada vive solo en el signer (`CA_KEY_PEM`) o en un HSM
  representado por ese signer; **nunca** en el path de un request del
  panel ni en el binario del nodo.
- El signer expone exactamente un servicio: firmar CSRs del enroll
  (`services.CA.SignCSR`). No emite CRLs distribuidas: la revocacion
  vive en Redis (`revoked:{serial}`, refresh 55 s del gateway).

## Politica de emision (lo que el signer acepta)

| Regla | Resultado si falla |
|---|---|
| CSR PEM parseable y autocfirma valida | `400` (firma forjada) |
| `CN == botnet-node` (CN unica de flota) | `422` (CN ajena) |
| Clave del dispositivo: ECDSA P-256/384 o RSA >= 2048 | `422` |
| TTL emitido | 90 dias exactos (± margen de reloj 5 min de `notBefore`) |
| Serial | aleatorio RFC5280; es la clave del CRL en Redis |
| EKU | `clientAuth` (el nodo autentica ante el túnel, no un servidor web) |

Descartada: CA por nodo (100k CAs = pesadilla de cadena) y CSR firmado
por el backend con clave generada en servidor (la clave en claro cruzaria
el TLS del alta — A3-02 exige que el par nazca en el dispositivo).

## Verificacion mecanica (la que ejecuta el test)

`openssl verify -CAfile ca.pem cert.pem` → `OK` y
`openssl x509 -noout -enddate` → `notAfter` = emision + 90 dias.
