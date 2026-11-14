# Host de desarrollo — higiene y el fallo 0xc0000142

## Síntoma

Los gates o cualquier `go build`/`go test` empiezan a fallar así:

```
C:\Users\...\pkg\tool\windows_amd64\compile.exe: exit status 0xc0000142
--- FAIL: [build failed]
```

`0xc0000142` es `STATUS_DLL_INIT_FAILED`: Windows no consigue inicializar
el proceso. **No es un fallo del código** y reintentar sin más lo repite.

## Causa principal: Windows Defender en cuarentena

**El fallo determinista era Defender, no la memoria.** El registro
`Microsoft-Windows-Windows Defender/Operational` lo deja por escrito:

| Hecho | Dato |
|---|---|
| Amenaza | `Trojan:Win32/Bearfoos.A!ml` — heurística ML, falso positivo típico de binarios Go |
| Fichero | `C:\Users\LAnge\AppData\Local\Temp\go-build*\b339\exe\a.out.exe`, el binario que `go.exe` acaba de enlazar |
| Cuándo | 9 detecciones el 2026-11-11 entre las 12:07 y las 16:00, en plena sesión de implementación |
| Origen y acción | `Protección en tiempo real`, detección `Concreto`, acción `Cuarentena`, proceso `C:\Users\LAnge\sdk\go\bin\go.exe` |

Defender comía el `a.out.exe` recién enlazado, así que el link fallaba
**siempre en los mismos paquetes**, incluso con `-p 1`. Eso es lo que la
hipótesis de memoria no explicaba: un problema de recursos no es
determinista por paquete.

### Arreglo

```powershell
# PowerShell elevado, una vez
powershell -ExecutionPolicy Bypass -File scripts/dev/defender-exclusions.ps1
```

El script excluye las rutas donde Go escribe y enlaza (caché `go-build`,
`go\pkg\mod`, `GOTMPDIR`, el toolchain y el proyecto), excluye los procesos
`go.exe`, `compile.exe`, `link.exe`, `asm.exe` y `cgo.exe`, y **reactiva la
protección en tiempo real** — apagarla era el síntoma, no el arreglo.

Además `scripts/env.sh` fija `GOTMPDIR=$HOME/go-tmp`: el temporal de Go deja
de vivir en `%TEMP%` y el binario nace ya en zona excluida.

Comprobado el 2026-11-14 con la protección **activada**: 10 iteraciones de
compilación de `internal/watchdog` y `pkg/geoip` (20 enlaces), cero rojos y
cero detecciones nuevas de `a.out.exe`.

Ruido aparte: `PUA:Win32/Vigua.A` señala un `unlocker-1-9-2.zip` viejo en
`D:`. No afecta al build pero hace trabajar a Defender en cada análisis; la
carpeta va excluida en el mismo script.

## Factor secundario: memoria y número de procesos

Go dimensiona las acciones de build en paralelo con `-p = nº de CPUs`. Esta
máquina tiene 24, así que un `go test ./...` puede abrir 24 `compile.exe` a
la vez, cada uno una consola nueva. Sobre una sesión ya cargada (cientos de
procesos entre agentes, `powershell.exe`, `OpenConsole.exe` y binarios de
test que nadie retiró) eso agota el escritorio/heap de la sesión.

Medido el 2026-11-12: 443 procesos vivos, 22 `powershell.exe`, 23
`OpenConsole.exe`, 2 GB libres de 16 GB. Y en la repetición: 439 procesos
pero **1,8 GB libres de 16 GB**, con Chrome (936 MB), el editor (780 MB) y
`vmmemWSL` (626 MB) comiéndose la máquina.

Sigue siendo real, pero es el segundo plato: con Defender arreglado se
manifiesta como lentitud o como fallos esporádicos, no como un rojo clavado
en los mismos dos paquetes.


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
- Objetivo: ≥ 2,5 GB disponibles antes de empezar. **El preflight mide
  memoria y procesos**: avisa por debajo de 1,2 GB y solo es rojo por debajo
  de 700 MB, en vez de dar un rojo que parece del código.
- **No compiles dos cosas pesadas a la vez**: el único fallo por memoria de
  la sesión salió compilando la suite y una prueba de 20 enlaces en paralelo.
- Tras una sesión de agentes: lo mismo.
- Si aparece `0xc0000142` en paquetes **distintos cada vez**, es memoria:
  `reap.sh`, `wsl --shutdown` y reintentar. Si sale siempre en el **mismo**
  paquete, sospecha primero de Defender (ver arriba).
- No apagar la protección en tiempo real como atajo: fue el síntoma, no el
  arreglo, y deja la máquina entera sin antivirus.
- No subir `-p` sin medir: el techo lo pone el escritorio de Windows, no
  la CPU.

## Segunda vuelta medida (2026-11-14)

Con Defender ya excluido, la suite volvió a fallar una vez con `0xc0000142`
en `internal/state` — un paquete distinto del de las veces anteriores.
Medido en ese momento: **1.365 MB libres de 16 GB y 466 procesos**, con
Chrome, el editor y cuatro procesos `node` del harness entre los mayores
consumidores. La causa inmediata fue haber lanzado **dos compilaciones
pesadas a la vez** (la suite y una prueba de 20 enlaces). El paquete compiló
5 de 5 veces al repetirlo en frío y en solitario.

Por eso el preflight mide memoria disponible y procesos, y avisa antes de
dar un rojo: la diferencia entre «falta memoria» y «el código está roto».


## Compilación completa

Un solo comando, con evidencia guardada:

```bash
bash scripts/build/all.sh          # todo: build, vet, suite, matriz, firma, humo, panel, gates
bash scripts/build/all.sh --fast   # solo build, vet, suite y gates (para iterar)
```

Deja el log en `docs/verification/evidence/build-<fecha>.log`. `make` no
existe en este host: el script es la interfaz.

