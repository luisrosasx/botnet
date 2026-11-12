---
artifact: epic
id: EP-02
title: Intrusión y entrega (legba, RouterSploit, YAML de CVE por modelo)
owner_layer: infra
closes: —
estimate_days: 5
risk: medium
phase: F1
---

## Objetivo

Convertir un candidato descubierto en un binario `botd` corriendo sobre el dispositivo: legba prueba credenciales multi-protocolo, RouterSploit ejecuta el CVE del modelo cuando existe, y el stager de sliver-builder copia el binario estático correcto (por clase de hardware) y muere.

## Alcance

- Runner legba (http/ssh/telnet/ftp) con salida JSON `{protocol, cred, model}`.
- Runner RouterSploit sobre `scripts/intrusion/cve-models.yaml` (modelo → CVE → arch → device_class), ≥ 20 entradas iniciales.
- Compilación sliver-builder de `botd` por arquitectura (`CGO_ENABLED=0`, upx) — compartida con EP-14, que fija la matriz de release.
- Stager con reintentos y jitter: destino `/data/botd` (clases A/B) o `/tmp` (clase C tmpfs-only), lanza con `--stage` y muere.

## Fuera de alcance

- El binario en sí (EP-03) y su enrollado (EP-03); aquí el binario es un artefacto opaco que se copia.
- Persistencia tras reinicio (EP-05). La matriz formal de release y su egress (A2-10) se gobiernan desde EP-14; aquí solo se compila lo que el stager necesita, por eso la épica no requiere más de 6 tasks reales.

## Hallazgos que cierra

Ninguno del veredicto (marco sección 6: `—`). Soporta A1-11/A2-10 al entregar por clase sin artefactos sobrantes.

## Criterios de aceptación de la épica

1. `scripts/intrusion/legba_run.sh TARGET=qemu-lab` → JSON con `protocol` no nulo en < 30 s sobre target vulnerable; sobre target sano → salida ≠ 0 sin crash.
2. `yamllint scripts/intrusion/cve-models.yaml` pasa y `go test ./scripts/intrusion/... -run TestCVEModelSchema` valida ≥ 20 entradas con `arch ∈ {mipsbe,mipsle,armv5,armv7,arm64,amd64}` y `device_class ∈ {A,B,C}`.
3. `scripts/intrusion/routersploit_run.sh --dry-run MODEL=TL-WR841N` → JSON con `exploit_id` no nulo en < 5 s por modelo del YAML.
4. Entrega e2e en QEMU clase C (8 MB de overlay): stager con pérdida netem 5 % instala en ≤ 3 intentos y `df /overlay` muestra ≥ 4 MB libres tras la copia.

## Dependencias

EP-01 entrega candidatos clasificados. Comparte la cadena de build con EP-14 (matriz de arquitectura); en F1 basta con compilar bajo demanda, la matriz de release se cierra en EP-14.

## Definición de hecho

Un candidato EP-01 pasa de IP descubierta a `botd --stage` corriendo sobre el dispositivo sin intervención humana, con evidencia JSON de qué credencial/CVE abrió la puerta y de qué binario se entregó.
