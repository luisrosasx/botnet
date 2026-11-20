---
artifact: backlog
project: BOT.NET
scope: panel
created: 2026-11-18
---

# Backlog — panel de dispositivos y motor de descubrimiento

Reglas de este backlog:

- Una task por hallazgo o por entrega. Ninguna task sin al menos tres
  criterios de aceptación medibles con un comando y un valor esperado.
- Un hallazgo sin task es un hallazgo perdido: la tabla de trazabilidad se
  rellena en la misma tanda en que se acepta el veredicto.
- Nada se marca DONE sin prueba ejecutada y sin su fila `PASS` en
  `docs/verification/EP-3X.md`.
- Las tasks del panel no escriben en `nodes`: el panel es de solo lectura.

## Épicas

| Épica | Título | Cierra |
| --- | --- | --- |
| EP-30 | Panel: inventario de dispositivos y estado (doc en `docs/blueprint/EPICS`) | listado exacto con IP y estado |
| EP-31 | Panel: base caída, paginación y métricas propias | 503 contra lista vacía |
| EP-32 | Motor: cierre del bucle cola, barrido y presupuesto | exploración sin manos |
| EP-33 | Exponencialidad medida y sin doble conteo | ×1,5 tres ventanas |
| EP-34 | Puerta del panel con sabotajes | la puerta muerde |
| EP-35 | Despliegue del panel como unidad y runbook | unidad activa y runbook |
| EP-36 | Cierre: evidencia y puertas anteriores en 0 | tres puertas en 0 |

## Trazabilidad hallazgo → task

| Hallazgo | Origen | Gravedad | Task |
| --- | --- | --- | --- |
| _(se rellena al cerrar la oleada 4)_ | | | |

## Orden de ejecución

1. EP-30: sin listado no hay nada que enseñar ni que probar.
2. EP-31: lo que hace que el panel sea usable de verdad (error, paginación).
3. EP-32 y EP-33: el motor y su medida, que es lo que hace crecer la flota.
4. EP-34: los sabotajes de todo lo anterior.
5. EP-35: el despliegue y el runbook.
6. EP-36: el cierre con evidencia.
