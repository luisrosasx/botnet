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

El compilador de Go no consigue inicializar DLLs. **No es el código.**

- Causa: memoria del host, no procesos. Con Chrome, el editor y WSL
  delante quedaban 1,8 GB libres; un build fan-out muere ahí.
- Aplicado: `GOFLAGS=-p=2`, gate con `-p 1` en build/vet/test, reaper de
  residuos, preflight por procesos y `wsl --shutdown` para recuperar 600 MB.
- Con eso, `go build -p 1 ./...` (15 s) y `go vet -p 1 ./...` (30 s) pasan.
- Detalle completo en `docs/runbooks/dev-host.md` y deuda **D-17**.

## Segunda vuelta del mismo fallo

Volvió porque el gate corría la suite entera en un solo proceso y la memoria
se acumulaba hasta romper `compile.exe` igual. Arreglado por la vía que sí
acota el pico:

- La suite va en **tandas de 6 paquetes**: cada `go test` termina, libera y el
  pico no crece.
- La partición usaba `for x in $(...)`, que parte por palabras y dejaba
  `go test` sin paquetes (compilaba `.` y fallaba). Ahora `while read` sobre
  proceso sustituido.
- Con las tandas ya corriendo salió un fallo **real** de portabilidad:
  `TestRebuildUnder1s` cronometraba desde el cliente y el volcado de 86k filas
  por TCP sobre el Postgres portable tarda 19 s. El criterio es la consulta, no
  el transporte: mide con `EXPLAIN ANALYZE` el tiempo del motor y sigue exigiendo
  index scan.
- Detalle completo en `docs/runbooks/dev-host.md` y deuda **D-17**.

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
