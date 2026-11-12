# Firma de releases — clave DEV

`keys/root_ed25519.key` es una clave de **desarrollo**, versionada a propósito: el build la necesita
para fijar la raíz pública en el binario con `-X …/internal/agent.RootPubKeyBase64`. Sin ella el
assert cruzado `botd --print-root` ↔ raíz no se puede verificar en CI.

## Antes de producción

1. Generar una raíz nueva fuera del repo: `./bin/botnet-build genroot --dir /ruta/segura`.
2. Publicar la raíz nueva en un release firmado de los nodos.
3. Rotar: los nodos aceptan la raíz vieja y la nueva durante una ventana, y luego solo la nueva.
4. Retirar `keys/root_ed25519.key` del repo si la raíz de producción pasa a gestionarse fuera
   (riesgo R-02 en `docs/state/DEUDA-Y-RIESGOS.md`).

## Qué NO es esta clave

- No es la CA de flota (esa se emite en el backend y su política vive en `ca_policy.md`).
- No protege nada si se filtra: quien la tenga puede firmar un manifiesto de update que los nodos
  aceptarán. De ahí el paso 4.

## Formato

Semilla ed25519 en hexadecimal (64 caracteres, 65 B con salto) y pública en base64 (45 B). No son
PEM: `grep 'BEGIN PRIVATE KEY'` no las encuentra, así que la revisión de secretos se hace por nombre
y tamaño, no por cabecera.

## Uso en build

`build_matrix.sh` y el `Makefile` inyectan la **pública** automáticamente. `botd --print-root`
devuelve la raíz embebida y es lo que asserta el test de T-1403.
