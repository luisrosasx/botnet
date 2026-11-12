# Runbook: soak de reinicios con `pkill` (T-1204)

## Qué prueba

Matar al par watchdog+worker real 1.000 veces de forma aleatoria (hijo /
padre / ambos) sin dejar ni un huérfano. El arnés arranca **`botd
--parent` de verdad**: el padre sostiene el flock del pidfile
(`node_dir/botd.pid`, escrito por el propio botd, no por el script) y
re-ejecuta el mismo binario como `--child` (T-0501). Cada muerte ejerce
el mecanismo real:

- muere el **hijo** → el padre lo relanza por re-exec (~1 s, T-0501);
- muere el **padre** → el hijo sobreviviente se re-elige padre, readquiere
  el flock y reescribe el pidfile con su pid (T-0502);
- mueren **ambos** → no queda nadie que se revive solo: entra el un-shot
  **`botd --guard`** (el cron de T-0503), que es exactamente donde el
  criterio lo pide — relanza al padre si el pidfile no responde.

Puerta F3 del backlog (§3): «1.000 reinicios `pkill` sin huérfanos» y
«0 huérfanos a los 5 min» (F2).

## Cómo se corre

```bash
make soak-1000            # acelerado: ITERS=200 por defecto (~8-10 min en host)
ITERS=50 bash scripts/soak/1000_restarts.sh   # humo rápido (~2 min)
FULL=1 bash scripts/soak/1000_restarts.sh     # la puerta: 1.000 iteraciones
KEEP_TMP=1 bash scripts/soak/1000_restarts.sh # deja el node_dir y botd.log para depurar
```

Por ciclo, el script:

1. mata por rol (hijo / padre / ambos) con `pkill -9` (Linux) o
   `taskkill //F` por winpid (Git Bash);
2. exige el revival del mecanismo que toca (re-exec del padre,
   re-elección del hijo o guard) y que el ciclo cierre con 2 vivos
   (`pgrep -f botd | wc -l` = 2);
3. aserta por ciclo **1 solo pidfile** y `kill -0` exitoso sobre el pid
   que **botd** escribió en él;
4. al final mata la **cadena del pidfile** (dueño → re-elegido → dueño)
   y exige **0 huérfanos**: ningún botd de la corrida vivo detrás de un
   pidfile muerto.

## Modo acelerado vs. puerta larga

| Modo | Dónde | Duración | Criterio |
|---|---|---|---|
| Acelerado (`ITERS=200`) | host del CI (Windows/Linux) | ~8-10 min (revival real ~1-3 s/ciclo) | los asertos anteriores |
| Puerta F3 (`FULL=1`) | QEMU clase B, Linux real | según I/O | `pkill -9` y `pgrep -f botd` literales |
| 7 días ISP real (R7) | nodo residencial | 7 días | puerta F6, placeholder abajo |

**Doble medida en host Windows (anotada):** `pkill`/`pgrep` no existen
en Git Bash; el script mata con `taskkill //F` por winpid y cuenta vivos
con `ps -W` — mismo criterio (proceso muerto, cero huérfanos), otra
herramienta. `ps -W` no reporta PPID de procesos nativos (lo deja en 0),
por eso el censo de procesos del arnés se hace por la ruta `mktemp` de
la corrida: cada ejecución ve solo sus propios botd y el recuento de
huérfanos no se contamina con residuos de verificaciones anteriores. La
puerta literal (`pkill -9`, `pgrep -f botd = 2`) queda reservada a la
corrida QEMU clase B.

**Pidfile:** el contrato «1 solo pidfile vivo por nodo» (T-0502) lo
cumple botd: el flock + `botd.pid` los escribe el dueño del rol padre
(`watchdogPidfile`), y el re-elegido lo reestampa. El arnés ya no
fabrica pidfiles; solo los audita.

**Fix de producción que expuso este soak (2026-11):** el hijo
re-elegido heredaba `Silence=0/Revive=0` del `ChildConfig` porque
`promote()` llamaba `parentLoop` sin pasar por `ParentConfig.defaults()`
(solo `RunParent` lo hacía). Con silencio 0 el deadline del pipe vencía
al nacer y el par entraba en bucle spawn-kill de ~30 ms — el soak real
lo cazó y `internal/watchdog/election.go` quedó corregido.

## Extrapolación a 7 días en ISP real (condición R7, puerta F6)

Programada como puerta F6 en el backlog (§3): 7 días en ISP real con
pérdida, revocación < 60 s. El script de corrida larga queda:

```bash
# en el nodo real (systemd fuera; binario desnudo como en QEMU)
ssh nodo 'for i in $(seq 1 1000); do sleep $((RANDOM % 600)); \
  pkill -9 -f "botd --parent" || true; done'   # 24 h de kill aleatorio/día
# y el informe del soak de 7 días:
# docs/runbooks/soak_report_F6.md   (placeholder — se llena al cerrar F6)
```

Placeholder de informe: `docs/runbooks/soak_report_F6.md` — debe
contar: reinicios totales, huérfanos (0 esperado), pidfiles vivos al
final (1), pérdida media del ISP y revival medio del watchdog.

## Resultado en este host (2026-11-11)

`ITERS=18` (par real, Windows/Git Bash) → `soak-1000: OK — iteraciones=18
revivals=18 huerfanos=0 pidfiles=1 par=botd--parent+reexec
reanimacion=guard` (~45 s de ciclos + build). Revivals observados por
mecanismo: 6 re-exec del padre, 6 re-elecciones del hijo, 6 guard.
`FULL=1` sigue siendo la puerta QEMU clase B (F3/BLOCKED-HW aquí).
