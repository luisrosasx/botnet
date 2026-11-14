# Host de desarrollo — higiene y el fallo 0xc0000142

## Síntoma

Los gates o cualquier `go build`/`go test` empiezan a fallar así:

```
C:\Users\...\pkg\tool\windows_amd64\compile.exe: exit status 0xc0000142
--- FAIL: [build failed]
```

`0xc0000142` es `STATUS_DLL_INIT_FAILED`: Windows no consigue inicializar
el proceso. **No es un fallo del código** y reintentar sin más lo repite.

## Causa

Go dimensiona las acciones de build en paralelo con `-p = nº de CPUs`. Esta
máquina tiene 24, así que un `go test ./...` puede abrir 24 `compile.exe` a
la vez, cada uno una consola nueva. Sobre una sesión ya cargada (cientos de
procesos entre agentes, `powershell.exe`, `OpenConsole.exe` y binarios de
test que nadie retiró) eso agota el escritorio/heap de la sesión y la
creación de procesos deja de inicializar DLLs.

Medido el 2026-11-12 antes del arreglo: 443 procesos vivos, 22
`powershell.exe`, 23 `OpenConsole.exe`, 2 GB libres de 16 GB.

## Segunda vuelta: no era solo el número de procesos

El tope `-p=4` no bastó: el fallo volvió. Medido en la repetición: 439
procesos pero **1,8 GB libres de 16 GB**, con Chrome (936 MB), el editor
(780 MB) y `vmmemWSL` (626 MB) comiéndose la máquina. Aun con 4 compiladores
a la vez, cada `compile.exe` puede pedir cientos de MB y el margen no llega.

Conclusión: el recurso escaso es la **memoria**, no los procesos. El
preflight y el tope solos no lo cubren.

## Arreglo definitivo

1. `scripts/env.sh` fija `-p=2` en `GOFLAGS` y el gate fuerza `-p 1` en
   `go build`, `go vet` y `go test`: una acción de build a la vez. Es más
   lento (15 s de build, 30 s de vet) y es lo único que sostiene el pico.
2. `scripts/dev/reap.sh` retira residuos de toolbox (agentes botd, dobles
   de test, qemu, motores portables). Solo imágenes conocidas: no toca
   aplicaciones del usuario. `--dry-run` para verlo antes.
3. El gate `check-implementation.sh` lleva preflight: con más de 600
   procesos vivos falla con el mensaje de qué hacer, en vez de mentir
   sobre el código.

## Rutina

- Antes de una tanda larga: `bash scripts/dev/reap.sh` y `wsl --shutdown`
  si WSL está levantado (libera ~600 MB; vuelve sola al usarla).
- Objetivo: ≥ 2,5 GB libres antes de empezar. Por debajo de 1,2 GB, el
  gate falla en el preflight con el mensaje de qué hacer.
- Tras una sesión de agentes: lo mismo.
- Si aparece `0xc0000142`: `reap.sh` y reintentar; si persiste, cerrar
  sesiones de terminal abiertas y volver a medir.
- No subir `-p` sin medir: el techo lo pone el escritorio de Windows, no
  la CPU.
