# Runbook — servicio de exploración y reclutamiento masivo

Operación del servicio que barre, enrola y verifica nodos. Todo lo de aquí
está pensado para ejecutarse desde el propio VPS (`root@74.208.94.91`), con el
checkout en `/root/botnet-repo` y el entorno en `/etc/botnet-recruit/env`.

## Arranque en frío

1. Sincroniza el código y compila los binarios que usan las unidades.

```bash
cd /root/botnet-repo && git pull --ff-only
cd /root/botnet-repo && go build -o /usr/local/bin/botnet-recruit ./cmd/botnet-recruit
cd /root/botnet-repo && go build -o /usr/local/bin/botnet-probe ./cmd/botnet-probe
```

2. Migra la base (idempotente) y carga las exclusiones.

```bash
set -a; . /etc/botnet-recruit/env; set +a
/usr/local/bin/botnet-recruit migrate
/usr/local/bin/botnet-recruit load-exclusions --csv scripts/recruit/exclusions.csv
```

3. Levanta las unidades y comprueba que el latido está fresco.

```bash
systemctl daemon-reload
systemctl enable --now botnet-recruit.service
systemctl enable --now 'botnet-recruit-worker@sweep' 'botnet-recruit-worker@banner' 'botnet-recruit-worker@cred'
curl -s 127.0.0.1:9110/healthz
```

`healthz` responde `status=ok` con `tick_age_s` por debajo de 90. Si responde
`rate_zero` es que el planificador está en clase 0: mira la sección de pausa.

## Pausa y reanudación

Pausar es una orden de control, no un `systemctl stop`: el proceso sigue vivo,
sigue latiendo y deja de repartir trabajo.

```bash
curl -s -X POST 127.0.0.1:9110/v1/control/pause
curl -s -X POST 127.0.0.1:9110/v1/control/resume
curl -s 127.0.0.1:9110/metrics | grep -E '^botnet_recruit_(paused|rate_class)'
```

Si el planificador se quedó en clase 0 por un dato malo, se recupera a mano:

```bash
psql -tAc "UPDATE recruit_control SET paused=false, rate_class=1, manual_rate_class=NULL, tick_at=now()" "$DATABASE_URL"
```

## Rollback en cuatro escalones

Se baja un escalón solo si el anterior no basta, y se mide después de cada uno.

1. Pausa el reparto (deja de promover candidatos nuevos).

```bash
curl -s -X POST 127.0.0.1:9110/v1/control/pause
psql -tAc "SELECT paused FROM recruit_control" "$DATABASE_URL"
```

2. Para los workers de etapa (deja de haber tráfico de sondeo).

```bash
systemctl stop 'botnet-recruit-worker@*'
systemctl is-active 'botnet-recruit-worker@*'
```

3. Para el orquestador (deja de haber latido y de haber salud).

```bash
systemctl stop botnet-recruit.service
systemctl is-active botnet-recruit.service
```

4. Revoca los nodos enrolados por el servicio, conservando la base.

```bash
bash scripts/field/revoke_mass.sh --dry
bash scripts/field/revoke_mass.sh
```

Para volver a subir, en orden inverso: `systemctl start botnet-recruit` y
después los tres workers, y `curl -X POST 127.0.0.1:9110/v1/control/resume`.

## Revocación de nodos

Cerrar un nodo es una baja con código de salida, no un `DELETE`: el nodo se
lleva su `leave_code` y el re-clutaje queda bloqueado doce meses.

```bash
set -a; . /etc/botnet-recruit/env; set +a
psql -tAc "SELECT count(*) FROM nodes WHERE status IN ('enrolled','live')" "$DATABASE_URL"
bash scripts/field/revoke_mass.sh --dry
bash scripts/field/revoke_mass.sh
psql -tAc "SELECT status, count(*) FROM nodes GROUP BY 1" "$DATABASE_URL"
```

## Desinstalación

Deja el VPS como estaba: unidades fuera, binarios fuera, datos intactos.

```bash
systemctl disable --now 'botnet-recruit-worker@*' botnet-recruit.service
rm -f /usr/local/bin/botnet-recruit /usr/local/bin/botnet-probe
psql -tAc "SELECT count(*) FROM recruit_waves" "$DATABASE_URL"
```

Las tablas `recruit_*` no se borran: la desinstalación es del servicio, no de
la historia del reclutamiento.

## Sabotajes del paraguas

La puerta `scripts/check-recruit.sh` se prueba rompiendo cosas a propósito:
seis sabotajes, seis rojos, y todo vuelve a su sitio. Úsalo antes de fiarte de
un verde.

```bash
cd /root/botnet-repo
bash scripts/recruit/sabotage_recruit.sh
bash scripts/check-recruit.sh
```

Cada sabotaje dice si muerde o si es un falso verde. Si alguno no muerde, la
comprobación correspondiente está mirando el sitio equivocado.

## Límites del servicio

Los números del despliegue, que son los que hay que respetar:

- `recruit.slice`: `CPUQuota=300%`, `MemoryMax=1536M`, `TasksMax=256`.
- `botnet-recruit.service`: `--db-pool=2`, `CPUQuota=150%`, `MemoryMax=512M`.
- Workers: `CPUQuota=50%`, `MemoryMax=320M` cada uno; el barrido necesita
  `CAP_NET_RAW` para el SYN crudo.
- Métricas y healthcheck en `9110`, solo en loopback.

```bash
systemctl show -p TasksMax --value recruit.slice
systemctl show -p CPUQuotaPerSecUSec --value recruit.slice
```

## Nodos de clase B y C

Solo la clase A escucha SOCKS5; un B o un C enrolado no se puede verificar con
tráfico y no cuenta como fracaso.

- `MAX_A=8900000` es el techo de aparatos de clase A del plan de barrido; por
  encima de esa cifra el embudo se llena de B/C que no sirven para el proxy.
- Un candidato B/C que se enrola se marca `clase_bc_sin_socks5`: queda
  inventariado, no verificado. No se reintenta ni se descarta.
- Con `RECRUIT_BUDGET_PER_CIDR` se acota cuántos SYN recibe cada /24 destino,
  de modo que un bloque de B/C no se lleve el presupuesto de los A.

## Qué mirar cuando algo va mal

```bash
curl -s 127.0.0.1:9110/metrics | grep -E '^botnet_recruit_'
systemctl status botnet-recruit --no-pager
journalctl -u 'botnet-recruit*' --since -30min --no-pager
bash scripts/metrics_smoke.sh
```

Si `orchestrator_tick_age_seconds` crece sin parar, el orquestador está vivo
pero mudo: mira el journal antes de reiniciar nada. Si `queue_depth` crece y
`pps_effective` no, el barrido no está comprando presupuesto.

## Chuleta de comandos

Los mismos comandos, en el orden en que se suelen necesitar durante una
guardia. El prompt `$` marca lo que se teclea; el resto es lo que devuelve.

```text
$ systemctl is-active botnet-recruit
$ curl -s 127.0.0.1:9110/healthz
$ curl -s 127.0.0.1:9110/metrics | grep '^botnet_recruit_pps'
$ bash scripts/check-recruit.sh
$ bash scripts/check-recruit.sh --only nodos
$ bash scripts/metrics_smoke.sh
$ bash scripts/recruit/rbl_probe.sh --ip 74.208.94.91
$ systemctl kill --signal=TERM botnet-recruit
$ systemctl show -p NRestarts --value botnet-recruit
$ journalctl -u 'botnet-recruit*' --since -15min --no-pager | tail -40
$ psql -tAc "SELECT action, reason FROM recruit_planner_ticks ORDER BY tick_at DESC LIMIT 3" "$DATABASE_URL"
$ systemctl stop 'botnet-recruit-worker@*'
$ systemctl start 'botnet-recruit-worker@sweep' 'botnet-recruit-worker@banner' 'botnet-recruit-worker@cred'
```
