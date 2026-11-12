# Runbook: persistencia multi-firmware (EP-05)

El agente sobrevive al reboot y al dueño que mata procesos con la
cadena **systemd → rc.local → `@reboot`** y un doble proceso del MISMO
binario: `botd --parent` vigila y relanza `botd --child` (reexec, 0
segundo binario en disco). Cero root: todo corre como el usuario admin
del panel.

## La cadena, en orden de intención

| Eslabón | Qué hace | Fichero |
|---|---|---|
| systemd | unit que lanza `botd --parent`; `botd` loguea `persist=systemd` (lo delata `INVOCATION_ID`) | `deploy/systemd/botd.service` |
| rc.local | firmware busybox: `BOTNET_PERSIST=rc setsid botd --parent &`; loguea `persist=rc` | `deploy/rc/botd.rc.local` |
| `@reboot` + guard | una sola línea `* * * * *` que hace de `@reboot` (primer tick sin pidfile vivo → lanza al padre) y de guard (relanza si el pidfile no responde) | `deploy/cron/botd-guard.cron` |

La detección se loguea en cada arranque: `persist=systemd|rc|reboot`.
Sin systemd ni rc, la línea de cron cubre sola: es el perfil mínimo
(`crontab -l | wc -l` = 1). `botd optout` la borra sin root
(`crontab -l | wc -l` = 0) y deja el nodo inofensivo.

## Watchdog: un binario, dos procesos

- `botd --parent`: pidfile `/data/botd/botd.pid` con `flock` exclusivo;
  pipe mutuo loopback TCP; el hijo calla > 30 s (doble de `kill
  -STOP`) → lo mata y relanza; el hijo muere → relanza en ~1 s
  (presupuesto T-1301: 3 revives/h → 1/h).
- `botd --child`: late el pipe cada 2 s; el padre muere → **se
  re-elige padre** (log `role=elected`, < 15 s), readquiere el flock
  (el kernel lo soltó al morir el anterior) y relanza un hijo. Matar
  al padre es irrelevante: 0 zombis (el padre hace `Wait`), 0
  huérfanos.
- Segundo arranque con el flock tomado → `exit ≠ 0` (nunca dos padres).
- El guard solo actúa si el pidfile no responde: con agente sano, 60
  ticks de cron no crean ni un proceso extra.

## Dueño same-uid que mata (A3-05)

Matar desde la cuenta del dueño cuesta disponibilidad, no elimina el
nodo: con cron vivo, el par revive en ≤ 70 s (cron 60 s + spawn) y la
mediana del soak es < 60 s:

```bash
bash scripts/soak/owner_kill_soak.sh --kills=30            # puerta real (~15 min)
bash scripts/soak/owner_kill_soak.sh --kills=5 --cron=10   # smoke en host (~2 min)
```

Si el dueño desactiva su propio crontab, el nodo vuelve en el
siguiente `@reboot` — riesgo aceptado (mismo uid puede leer la clave
0600; véase `docs/runbooks/threat-model.md`).

## Soak de huérfanos

```bash
bash scripts/soak/orphan_check.sh              # smoke acelerado (ITERS=5, ~1 min)
FULL=1 bash scripts/soak/orphan_check.sh       # 1.000 ciclos — puerta QEMU/Linux
```

Aserta: 2 procesos vivos al cerrar cada ciclo, recuperación < 10 s
(hijo) / < 15 s (padre), y al final 1 solo pidfile vivo, 0 procesos
fuera del pidfile y `ps | grep -c '[d]efunct botd'` = 0. La medida
completa de 1.000 reinicios es la de EP-12
(`scripts/soak/1000_restarts.sh`); esta es la comprobación de
invariantes por ciclo.

## Estado único y spool (T-0505)

Un solo directorio, inventario fijo de 9 ficheros (cota 10):

```text
/data/botd/
├── node.json          # identidad; su validez manda el modo de arranque
├── botd.pid           # pidfile con flock del rol padre
└── state/
    ├── secret.bin     # node_secret de 32 B (0600)
    ├── rotation.json  # ventana current/previous del secreto
    ├── key.pem        # clave privada, nunca salió del dispositivo
    ├── cert.pem       # certificado de flota (TTL 90 d)
    ├── ca.pem         # CA de flota
    ├── device_token   # token de alta (cabecera, nunca URL)
    └── ticket.bin     # ticket EP-02 (opcional)
```

- Permisos: ficheros 0600, directorios 0700 —
  `find /data/botd -perm /077 | wc -l` = 0.
- Spool contable en `/tmp/botd-spool` (clase C: tmpfs, no gasta
  flash): tope 60 KiB con trim por cola — `du -k /tmp/botd-spool`
  ≤ 64 KB tras 24 h.
- Flush contable por sesión + periódico a 15 min con jitter
  determinista por `node_id` (`state.FlushTime`): la flota no despierta
  a la hora fija (A1-05); pico de escritura diario ≤ 30 KB.

## Cotas y su evidencia

| Criterio numérico | Cota | Medición |
|---|---|---|
| relanzar hijo | < 10 s | `go test ./internal/watchdog -run TestRespawnUnderTenSeconds` |
| re-elección de padre | < 15 s | `go test ./internal/watchdog -run TestElectionUnderFifteenSeconds` (medido ~2 s) |
| silencio de pipe | > 30 s | constante `ChildSilenceLimit`; doble en test con 1 s |
| revival con cron | ≤ 70 s | `go test ./internal/watchdog -run TestOwnerKillRevivalMedian` (cron 60 s simulado a 12 s: factor 5x anotado; la cota asertada es la real) |
| mediana revival dueño | < 60 s | mismo test + `owner_kill_soak.sh --kills=30` |
| ciclos sin zombis | 100/100 | `TestHundredCyclesNoZombies` (modo rápido 15 ciclos por defecto en este host; `BOTNET_WD_CYCLES=100` para el criterio completo — mismo código, mismo kill, solo más iteraciones) |
| 1.000 reinicios sin huérfanos | 0 fuera de pidfile | `orphan_check.sh` (FULL=1000 en QEMU/Linux; EP-12 lo encadena) |
| ficheros de estado | ≤ 10 | `TestInventoryWithinTenFiles` |
| permisos | 0600 | `TestNonPrivatePermsZero` + `find -perm /077` en imagen |
| spool /tmp | ≤ 64 KB | `TestSpoolCappedAfter24h` |
| escritura diaria spool | ≤ 30 KB | mismo test (24 h simuladas) |

## Dobles por hardware (anotados)

Este host no tiene Docker estable ni QEMU con systemd, y en Git Bash
no existen `pkill`/`pgrep`/señales POSIX ni bits de permisos POSIX.
Los criterios que exigen hardware llevan doble medido y su limitación:

- **`kill -STOP` del hijo colgado**: en el test el doble es un proceso
  vivo que deja de latir el pipe (silencio escalado a 1 s); la señal
  real se sufre en la imagen QEMU/Linux.
- **Reboot real ≤ 90 s por perfil** (systemd / rc / `@reboot`): la
  detección y los tres eslabones están asertados en Go
  (`TestPersistMode`, `TestBootChainFiles`); el reboot físico es la
  matriz QEMU de la fase (BLOCKED-HW hasta tener la imagen).
- **Permisos 0600**: en Windows no hay bits POSIX; la medición real es
  el `find -perm /077` sobre la imagen Linux.
- **Zombis (`defunct`)**: sin estado Z en Windows; el `grep
  '[d]efunct botd'` de `orphan_check.sh` solo cuenta en Linux/QEMU.
- **`iostat` del overlay < 1 MB/día en clase C**: cota de hardware; el
  tope lógico que lo garantiza (spool en tmpfs + ≤ 30 KB/día) está
  asertado en Go.
