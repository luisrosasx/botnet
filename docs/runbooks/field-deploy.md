# Despliegue de campo — VPS, TLS, DNS y bastión

Runbook de la primera puesta en marcha real: un VPS con el plano de control y el bastión de
malla. Es el paso F0.2 de la fase de campo. Sin esto no hay rendezvous ni gateway, y sin ellos
el piloto no puede enrolar nada.

## 1. Requisitos

- VPS x86-64 con IP pública, 4 GB de RAM y 40 GB de disco (sobra: el compose pide ~1,5 GB).
- Un dominio con capacidad de crear registros DNS.
- Docker y Docker Compose en el VPS.
- Puertos públicos: 443 (TLS). Todo lo demás, por tailnet.

## 2. Registros DNS

Antes de levantar nada, deja el DNS resolviendo. Los nombres salen de `deploy/dns/rdv_cname.md`.

| Nombre | Tipo | Apunta a |
|---|---|---|
| `rdv.<dominio>` | A | IP del VPS |
| `api.<dominio>` | A | IP del VPS |
| `gw.<dominio>` | A | IP del VPS |
| `panel.<dominio>` | A | IP del VPS |

Comprobación: `for h in rdv api gw panel; do dig +short $h.<dominio>; done` devuelve la IP en los
cuatro.

## 3. Levantar el plano de control

```bash
git clone <repo> botnet && cd botnet
cp deploy/.env.example deploy/.env      # si no existe, crear con las claves de §4
docker compose -f deploy/docker-compose.yml up -d
docker compose -f deploy/docker-compose.yml ps
```

Servicios: `postgres`, `redis`, `nats` (con JetStream), `api`, `gw`, `rdv`, `web`.

Cierre de este paso:

```bash
curl -sf https://api.<dominio>/healthz   # 200
bash scripts/metrics_smoke.sh            # /metrics con las tres métricas
```

Migraciones (una vez, contra la base del VPS):

```bash
DATABASE_URL=postgres://... bash scripts/discovery/migrate.sh
```

## 4. Secretos y claves

En `deploy/.env`, nunca en el repo: `DATABASE_URL`, `REDIS_URL`, `NATS_URL`, `JWT_SECRET`,
`STRIPE_SECRET` (si se factura) y `BOTNET_API_TOKEN` para las tareas de operación.

La raíz de firma de release (`deploy/signing/keys/root_ed25519.key`) está commiteada como **DEV**:
para producción se genera una nueva con `go run ./cmd/botnet-build genroot -keydir <dir>` y se rota
según `deploy/signing/README.md`. La raíz pública va fijada en el binario del agente.

## 5. Bastión de malla (headscale)

El compose **no** incluye headscale: va aparte para poder actualizarlo sin tocar el plano de
control. Versión fijada en `deploy/headscale/VERSION` (0.29.7) y configuración en
`deploy/headscale/config.yaml`.

```bash
# binario y servicio (systemd) con la config del repo
headscale serve -c deploy/headscale/config.yaml
headscale nodes list
```

Cierre: `headscale nodes list` responde y un nodo de prueba (el propio VPS como cliente) aparece
`online`. La malla es **operación best-effort**: el plano de datos no depende de ella (así se
decidió en el blueprint), pero las mediciones de EP-07 la necesitan viva.

## 6. Verificación completa del despliegue

```bash
bash scripts/metrics_smoke.sh                    # métricas en los tres servicios
bash scripts/api/contract_test.sh                # contrato real contra serve
bash scripts/field/load_250_1000.sh --nodes 250  # P99 y saturación del gw
```

El humo de enrolado contra el host real se hace con un nodo de verdad:

```bash
bash scripts/field/bootstrap.sh --arch amd64 --target ssh --ssh root@<nodo> --persist auto
```

## 7. Rollback

- Plano de control: `docker compose -f deploy/docker-compose.yml down` y repetir en otro host con
  los mismos nombres. Los volúmenes `pgdata`, `redisdata` y `natsjs` guardan el estado.
- DNS: cambiar los A a la IP anterior; TTL corto (300 s) durante la migración.
- Si el problema es el motor y no el despliegue, `bash scripts/dev/reap.sh` y
  `docs/runbooks/dev-host.md` en el host de desarrollo, no aquí.

## 8. Lo que este runbook no cubre

- Emisión automática de certificados: la resuelve el proxy del compose; si se usa otro, hay que
  añadir ACME a mano.
- Alta disponibilidad: un solo VPS es un punto único de fallo, aceptado para el piloto.
- Backup de Postgres: pendiente, y no es opcional para producción.
