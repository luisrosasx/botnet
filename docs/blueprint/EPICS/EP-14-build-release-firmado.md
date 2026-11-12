---
artifact: epic
id: EP-14
title: Build matrix, release y suministro firmado
owner_layer: infra
closes: A1-13, A2-15, A2-10, A2-05, injerto 11
estimate_days: 6
risk: medium
phase: F1
---

## Objetivo

Que una release sea aburrida y segura: matriz cross de `CGO_ENABLED=0` + upx por arquitectura con puerta de tamaño (≤ 4,0 MB; 2,6 MB upx en clases B/C), self-update con manifiesto firmado en ed25519 con raíz fijada en el build (injerto 11 — un MITM ya no sirve el binario que él mismo hasheó), rollback server-side por `stable_version` y descubrimiento de destino por DNS en lugar de IPs compiladas.

## Alcance

- `make cross`: amd64, arm64, armv5, armv7, mipsbe, mipsle — 1 binario × 6 archs; `file` sin enlaze dinámico; puerta `size` por clase.
- Humo QEMU por arch: arranca, enrolla y late contra stub (puerta F1).
- `deploy/signing`: raíz ed25519 fijada en build; manifiesto `{version, sha256, url, sig}`; verificación antes de ejecutar; el manifest sigue el renaming (A1-11).
- Flujo de self-update: descarga a `botd.new`, rename atómico, `exit 75`, `--commit` tras 5 min sanos; rollback: 3 salidas 75 en 15 min → backend fija `stable_version` (A2-05, acotado por D1 De3).
- Resolver por DNS con TTL+jitter para los destinos de control (A2-10/A1-10): mover un backend de IP no exige re-intrusión.
- Multi-SNI, jitter de puerto destino y presupuesto de egress por release a 100k (A1-13/A2-10: ~150 GB por release mal dimensionada es la pesadilla a evitar).
- Conciliación horaria de `bytes_delta` del heartbeat contra `usage` sin bytes extra (A2-15: el 5 % de tolerancia estructural se vigila por hora, no se acepta).

## Fuera de alcance

- La firma del manifest de P2 con KMS y los 24 artefactos: aquí hay 6 artefactos y la raíz vive en el binario; no se importa la maquinaria de release de P2. La medición del egress por provedor se hace en EP-09; aquí su presupuesto por release. El codec y el keepalive (EP-04) no cambian por release: el `codec_ver` da la compatibilidad.

## Hallazgos que cierra

- Injerto 11 y A1-11 (S4): suministro ed25519 con raíz fijada; el renaming deja de romper el manifest.
- A2-05 (S4): crash-loop acotado con rollback server-side a la versión estable.
- A2-10 (S4): descubrimiento por DNS — mover IP no pide reentregar binarios.
- A2-15 (S4): conciliación horaria de deltas sin gastar bytes — la tolerancia del 5 % no se hereda, se mide.
- A1-13 (S2, pata del ganador): destinos dispersos y multi-SNI — la correlación cadencia↔volumen deja de tener un solo destino al observador del enlace.

## Criterios de aceptación de la épica

1. `make cross` → 6 ELFs; `file` no dice `dynamically linked` en ninguno; `size -t` ≤ 4,0 MB por binario (upx ≤ 2,6 MB en clases B/C); `go env CGO_ENABLED` = 0 en el build (aserción del Makefile).
2. `make qemu-smoke` → por cada arch: `HEARTBEAT 200 ok` en log en < 60 s; en mipsbe, assert de endianness (magic de handshake big-endian) en el test de arranque.
3. `botd --print-root` imprime la pubkey raíz; `scripts/signing/verify_release.sh` sobre el manifiesto → exit 0; con `sig` alterado en un byte → exit ≠ 0 (0 scripts, 1 test en CI).
4. Update e2e: binario firmado nuevo → `exit 75` → `--commit` a los 5 min → `botd --version` nueva; crash de 3 salidas 75 en 15 min → backend fija `stable_version` y el agente vuelve a la versión anterior en < 20 min (test con reloj acelerado).
5. Zona DNS de test: cambio de registro del destino → el 100 % de los nodos simulados re-asignan en ≤ TTL + jitter (24 h) sin reentrega de binario; `scripts/release_egress.sh` a 100k → egress de release ≤ 500 GB y `update.log` por nodo ≤ 4,5 MB.
6. `make reconcile-hourly` sobre fixture de 24 h: desvío `bytes_delta`↔`usage` ≤ 0,5 % en cada hora (0 horas en rojo).

## Dependencias

EP-03 define el contrato de enrol/estado que la matriz compila; EP-04 consume DNS dinámico para el rdv; EP-12 ejecuta el fuzz y soak sobre los binarios de esta matriz; EP-10 aloja `stable_version` en `nodes` y el presupuesto que esta épica verifica por release.

## Definición de hecho

Publicar versión es: `make cross && make sign && make qemu-smoke` verdes, la raíz ed25519 verificada, el egress de la release presupuestado a 100k, y un rollback que no espera a un humano.
