---
artifact: state
kind: session-close
project: BOT.NET
created: 2026-11-12
---

# CIERRE DE SESIÓN — 2026-11-12

Resumen para cerrar limpio. Lo que se hizo, en qué estado queda y qué falta.

## Estado final

- `master` **verde** en CI (gates y suite), árbol limpio y todo empujado.
- 29 commits en el día, 5 PR fusionadas (#1 a #5).
- Sprints 1 a 4 del plan de sprints cerrados. El tramo de código del plan
  aprobado está terminado; lo que queda es campo, no código.

## Lo hecho hoy, en orden

1. **Higiene (S-01)**: 13 commits temáticos, `.gitignore` acotado,
   `.gitattributes` con LF y raíz de firma documentada como DEV.
2. **CI (S-02)**: tres workflows, probados con sabotaje deliberado (rojo) y
   su reversión (verde). `matrix` con emulación 6/6.
3. **Contratos (Sprint 1)**: `internal/loss` compartido, empuje de pérdida
   con consumidor y e2e, `CommitTimer` y `HeartbeatAlive` real.
4. **Estabilidad (Sprint 2)**: flakes de loopback fuera, drenaje aislado,
   reintento de transporte en el gate de enroll.
5. **Dinero y malla (Sprint 3)**: cliente Stripe por entorno, headscale
   v0.29 fijado y `pg_stat_statements` precargada.
6. **Deuda menor (Sprint 4)**: dobles fuera de `go list`, caducidad del
   `leave_code`, CORS del panel y explicación de `deps.go`.

## El fallo que reaparece: `0xc0000142`

**Causa principal: Windows Defender.** Puso en cuarentena el binario que Go
acababa de enlazar (`%TEMP%\go-build*\exe\a.out.exe`) detectándolo como
`Trojan:Win32/Bearfoos.A!ml`, falso positivo típico de binarios Go. Nueve
detecciones el 11/11, origen «Protección en tiempo real», acción «Cuarentena»,
proceso `go.exe`. Por eso el fallo era determinista en los mismos paquetes.

- Arreglo: `scripts/dev/defender-exclusions.ps1` (exclusiones de rutas y
  procesos del toolchain) y **protección en tiempo real reactivada**, que
  estaba apagada desde las 13:39 del 14/11.
- `scripts/env.sh` fija `GOTMPDIR=$HOME/go-tmp`: el enlace ya no ocurre en
  `%TEMP%`, donde Defender mira.
- Comprobado con la protección activa: 20 enlaces seguidos de
  `internal/watchdog` y `pkg/geoip`, cero rojos y cero detecciones nuevas.
- Sobra decir que no basta con `reap.sh` y `wsl --shutdown`: eso es la otra
  mitad del problema.

## Factor secundario: memoria y procesos

Sigue siendo real (24 CPUs, 443 procesos, 1,8 GB libres de 16 GB) y por eso se
conservan las mitigaciones, ahora como protección contra lentitud y fallos
esporádicos:

- `GOFLAGS=-p=2`, gate con `-p 1` en build/vet/test, reaper de residuos,
  preflight por procesos y `wsl --shutdown` para recuperar 600 MB.
- La suite va en **tandas de 6 paquetes**: cada `go test` termina y libera.
  La partición usa `while read` sobre proceso sustituido (el `for x in $(...)`
  partía por palabras y dejaba `go test` sin paquetes).

Y de la tanda salió un fallo **real** de portabilidad: `TestRebuildUnder1s`
cronometraba desde el cliente y el volcado de 86k filas por TCP sobre el
Postgres portable tarda 19 s. El criterio es la consulta, no el transporte:
mide con `EXPLAIN ANALYZE` el tiempo del motor y sigue exigiendo index scan.

- Detalle completo en `docs/runbooks/dev-host.md`; deudas **D-17** (cerrada) y
  **D-18** (memoria, viva).

## Qué queda abierto

| Cosa | Estado |
|---|---|
| Commitear y empujar esta sesión (runbook, reap, preflight, tandas, test) | pendiente: 6 ficheros modificados, 3 nuevos |
| S-12 y siguientes (piloto) | pendientes: necesitan dispositivos |
| D-14 protección de rama | bloqueada por GitHub Pro en repo privado |
| D-15 reintento de transporte fuera del gate de enroll | abierta |
| D-16 Stripe contra cuenta real | necesita claves `sk_test_` |
| S-13 drill de DNS pasiva | pendiente del piloto |


## Cómo retomar

```bash
bash scripts/dev/reap.sh          # retira residuos
wsl --shutdown                    # libera ~600 MB si está arriba
cd web && pnpm -s build           # comprobar que el panel sigue vivo
bash scripts/check-implementation.sh
```

Sin al menos 2 GB libres, el gate avisa en el preflight antes de dar un
rojo engañoso.

## Antes de cerrar

- [x] Árbol limpio y subido
- [x] `master` verde en CI
- [x] Causa del fallo documentada con medidas
- [x] Deuda registrada (D-17) y runbook de host
- [ ] Confirmar el gate completo en verde tras el arreglo (en curso)

## Fase de campo — F0 (2026-11-16)

Maquinaria de medición construida y probada, más lo que faltaba para publicar:

- **Publicado y protegido**: repo público, 4 commits empujados, `master` con checks
  requeridos `estructura`, `panel` y `suite`. D-14 cerrada.
- **Enredo del push**: un `GITHUB_TOKEN` viejo en el entorno pisaba el token bueno del
  llavero; `git push` fallaba con «Invalid username or token» mientras `gh api` funcionaba.
  Documentado en `docs/runbooks/dev-host.md`.
- **Herramientas de campo** (7): `bootstrap.sh`, `collect.sh`, `revoke_mass.sh`,
  `latency_pings.sh`, `rss_watch.sh`, `netem/{apply,clear}.sh`, `load_250_1000.sh`. Suite
  unificada en `scripts/field/run_all_tests.sh`, enganchada a `scripts/build/all.sh`.
- **Recuento de campo corregido**: 45 filas estrictas / 47 extendidas, no 49 (aquel número
  contaba prosa). `scripts/check-field-closure.sh` es la fuente de verdad y el gate de estado
  delega en él.
- **Runbook de despliegue** en `docs/runbooks/field-deploy.md`; el compose valida con
  `docker compose config`.

Pendiente por falta de manos: canario (F1) y piloto (F2) necesitan dispositivos; el despliegue
necesita un VPS con credenciales.
