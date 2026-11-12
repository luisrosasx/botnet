---
artifact: runbook
topic: threat-model
owner_layer: agent
epics: [EP-03]
created: 2026-11-14
---

# Threat model del agente — riesgo same-uid (A3-05 / R2)

El estado del agente vive en `/data/botd` (kv plano) con permisos **0600**
y directorio 0700: `find /data/botd/state -perm /077 | wc -l` = 0. Aun
asi, el **dueño same-uid** del router (intrusión típica: admin del panel
+ command injection, o Telnet/SSH sin segregación de usuarios) puede:

1. **leer** `state/secret.bin`, `state/key.pem` y `state/cert.pem` — el
   0600 no protege contra la misma cuenta dueña del proceso; y
2. **matar procesos** (`pkill botd`), incluida la pata de control.

## Riesgo aceptado: lectura de clave y secreto

`node_secret` (32 B) y la clave privada del certificado viajan y residen
sin cifrar en flash. Su exposición queda **topada por la rotación con el
certificado a 90 días** (BLUEPRINT §6, R2): robar el estado da una
credencial que caduca sola en ≤90 días y que la CRL revoca en <60 s
cuando el backend lo decide. Con churn <5 %/mes, la ventana robada es
fracción pequeña de la vida del nodo.

## Opción de coste 0 declarada: cifrado con secreto de hardware

Cifrar `secret.bin`/`key.pem` con un secreto de hardware (SPROM/OTP del
SoC o, en su defecto, una semilla fijada en fábrica) cuesta **0 en
presupuesto**: ni RSS extra (se descifra en el heap ya presupuestado),
ni CPU MIPS apreciable (AES/ChaCha sobre ≤3 KB), ni flash (sin
dependencias: stdlib). No entra en F1 porque exige demostrar en campo
que el chip expone el secreto al SO recortado de 2009; si el campo
confirma lectura fiable, es un cambio de lectura de fichero, no de
contrato. Hasta entonces la lectura same-uid queda como riesgo
documentado, no como sorpresa.

## Matriz mínima

| Agente | Puede | Mitigación presente | Residual |
|---|---|---|---|
| Dueño same-uid | leer claves, matar procesos | 0600 + rotación 90 d + CRL <60 s + watchdog/cron guard | credencial legible válida ≤90 d |
| JTAG / root persistente | extraer flash completo | confesado sin defensa (BLUEPRINT §14) | — |
| Intruso remoto | lo que permitió la intrusión | el agente no escucha (CGNAT) | superficie = 0 puertos |
