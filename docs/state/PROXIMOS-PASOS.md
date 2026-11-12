---
artifact: state
kind: next-steps
project: BOT.NET
created: 2026-11-12
revision: 1
---

# PRÓXIMOS PASOS — BOT.NET

Plan de salida desde el estado actual hasta producción. Cada paso lleva prioridad, coste, rol
responsable, dependencias y **criterio de cierre ejecutable**: si no hay comando o medición, el paso
no sirve.

El detalle ejecutable del tramo P0, P1 y la puerta del piloto está en `docs/plan/PLAN-S01-S12.md`.


Prioridades: `P0` antes de tocar nada más, `P1` antes del piloto, `P2` durante el piloto, `P3` con el
producto en marcha. Roles: `dev`, `infra`, `field`, `ops`, `legal`.

## 1. P0 — Higiene de repositorio (medio día)

| # | Paso | Coste | Rol | Depende | Criterio de cierre |
|---|---|---|---|---|---|
| S-01 | Commit inicial del árbol completo y acotar `.gitignore` (`*.exe` → `/dist/*.exe`) | 2 h | dev | — | `git status --short` vacío; `git log --oneline` con ≥1 commit; `git ls-files \| wc -l` ≥ 500 |
| S-02 | CI con los dos gates, `-p 1`, motores como servicios y cache de módulos | 4 h | infra | S-01 | Pipeline verde en el primer push y rojo si se rompe un test a propósito |
| S-03 | Mover `tmp_ep05adv`, `tmp_ep06adv`, `tmp_gwseed`, `tmp_ticket` a `testdata/doubles/` | 1 h | dev | S-01 | `go list ./...` sin entradas `tmp_*`; suite completa verde |
| S-04 | Generar `dist/raw/` dentro de `build_matrix.sh` cuando exista `upx` | 1 h | dev | — | `bash scripts/cross/build_matrix.sh` deja `dist/raw` con 6 binarios y el smoke pasa sin preparación manual |

## 2. P1 — Antes del piloto (una semana)

| # | Paso | Coste | Rol | Depende | Criterio de cierre |
|---|---|---|---|---|---|
| S-05 | Cablear `PushLossOnHeartbeat`/`BuildLossPush` en el bucle del gateway y consumirlo en el agente | 1 d | dev | S-01 | Test e2e: pérdida inyectada → frame `LossPush` → nodo reduce conexiones; `grep -rn 'PushLossOnHeartbeat('` con llamante fuera de su paquete |
| S-06 | Temporizador de `CommitAfter` sobre latidos sanos | 4 h | dev | S-01 | Test con reloj falso: 5 min sanos → `update=commit`; nodo inestable no commitea |
| S-07 | `HeartbeatAlive()` derivado del último latido real | 2 h | dev | S-01 | `grep -n 'func (w *Watermark) HeartbeatAlive' -A3` sin `return true`; test de ventana |
| S-08 | Drenaje con WebSocket vivo medido | 4 h | dev | S-06 | Test con socket real: `closed_reason=goaway` y 0 huérfanos |
| S-09 | Quitar los flakes de loopback (`TestExpiredTokenNoStream`, `botnet-rdv`) | 4 h | dev | S-02 | 10 ejecuciones seguidas de ambos paquetes sin un solo rojo |
| S-10 | Stripe real en modo test: URL base y `STRIPE_SECRET` de la cuenta | 4 h | dev | — | Conciliación `usage`↔Stripe ≤ 0,5 % con eventos reales en la cuenta de test |
| S-11 | Fijar versión de headscale y adaptar el config al formato vigente | 3 h | infra | — | `headscale serve` arranca con el config del repo; runbook sin avisos de formato |

## 3. P2 — Piloto (dos a tres semanas)

| # | Paso | Coste | Rol | Depende | Criterio de cierre |
|---|---|---|---|---|---|
| S-12 | Piloto real con 20-50 dispositivos propios y ajenos | 2 sem | field | S-05…S-11 | Los 49 criterios con nota de campo pasan a `PASS` medido; `grep -c 'BLOCKED-HW de campo' docs/verification/*.md` = 0 |
| S-13 | Drill de DNS pasiva sobre la flota del piloto | 2 d | field | S-12 | ≤ 60 % de destinos correlacionables (hallazgo A1-18) |
| S-14 | Ejercicio de revocación masiva de la CA de flota | 2 d | infra | S-12 | Revocar 100 nodos cierra sus sesiones en < 60 s y ninguno vuelve sin re-enrolar |
| S-15 | Precargar `pg_stat_statements` y medir con la extensión | 3 h | infra | S-02 | El delta de consultas de la suite coincide con el que reporta el motor |
| S-16 | Medir iperf3 y latencia a 250/g y 1.000 nodos en hardware real | 3 d | field | S-12 | P99 < 1,5 s y `streams_saturated` < 10 % con nodos reales |

## 4. P3 — Producto en marcha

| # | Paso | Coste | Rol | Depende | Criterio de cierre |
|---|---|---|---|---|---|
| S-17 | Rate-limit por api key además del tope por nodo | 1 d | dev | S-16 | Abuso simulado de un tenant no degrada a los demás |
| S-18 | Caducidad configurable del `leave_code` | 4 h | dev | S-12 | Código vencido → 403; test de reloj |
| S-19 | Revisión legal del suelo de transparencia y de la política de baja | 2 d | legal | S-12 | Directorio y textos aprobados; página publicada |
| S-20 | Comentario de porqué en `internal/deps/deps.go` y arreglo del preflight CORS | 2 h | dev | S-01 | `deps.go` explica el anclaje; el test del panel corre sin flag de Chromium |

## 5. Orden recomendado y criterio de parada

1. S-01 y S-02 primero: sin commit ni CI, todo lo demás se construye sobre arena.
2. S-03 y S-04 son de una hora cada uno y quitan ruido: van con S-01.
3. S-05 a S-11 antes de pedir un solo dispositivo prestado; el piloto no debe descubrir cableado roto.
4. S-12 abre la fase de campo y es el único paso que cierra los 49 criterios pendientes.

**Parada:** si en S-12 más del 30 % de los nodos muere por causas del agente (no del dispositivo),
se detiene el piloto y se vuelve a P1 antes de ampliar. Si el margen bruto medido a 100k cae por
debajo del 50 %, se replantea el precio antes de seguir reclutando.

## 6. Lo que NO conviene hacer todavía

- Ampliar a miles de nodos antes de S-13: sin el drill de DNS pasiva se amplía a ciegas.
- Tocar la arquitectura del túnel: está verificada con fuzz y soak; el riesgo de regresión supera al
  beneficio.
- Conectar dispositivos de terceros sin la página de baja probada (S-18 y S-19).
- Declarar producción con D-01 o D-02 abiertas.
