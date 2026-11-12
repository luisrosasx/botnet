# PLAN DE OLEADAS — proceso de diseño adversarial

Cinco oleadas secuenciales. Dentro de cada oleada, los agentes corren en paralelo y escriben ficheros
disjuntos. Ninguna oleada abre hasta que la anterior tiene artefactos válidos en disco.

| Oleada | Roles | Paralelo | Entrada | Salida |
|---|---|---|---|---|
| W1 | 3 proponentes | sí | BRIEF-BASE | 3 arquitecturas rivales de B |
| W2 | 3 defensores | sí | su propuesta + las otras dos | endurecimiento, refutación anticipada, presupuesto de recursos |
| W3 | 3 adversarios | sí | las 3 propuestas + defensas | hallazgos por lente: detección, operación/escala, correctitud/legal |
| W4 | 3 jueces | sí | todo lo anterior | matriz ponderada, ranking, veredicto por propuesta |
| W5 | 2 refinadores | sí | veredicto ganador + hallazgos | BLUEPRINT.md / BACKLOG.md + EPICS + TASKS |

## Lentes de los adversarios (W3)

- **A1 Detección y anti-análisis**: ¿cómo lo detecta un EDR, un IDS o el propio usuario? Huella de
  red, firma de binario, patrones de reinicio, comportamiento del proxy.
- **A2 Operación y escala**: coste por nodo, colapso del gateway, tormenta de reconexión, CGNAT,
  agotamiento de puertos, contabilidad a escala.
- **A3 Correctitud, seguridad y legal**: auth del gateway, fuga de credenciales de nodo, aislamiento
  entre tenants, path traversal en el proxy, cumplimiento y baja del dispositivo.

## Criterios de los jueces (W4), pesos sobre 100

Viabilidad en hardware pobre 20 · Robustez frente a ataques 20 · Mantenibilidad a 12 meses 15 ·
Coste operativo 15 · Cobertura de reclutamiento 15 · Time-to-first-paying-customer 15.

Cada juez puntúa las tres propuestas por separado, debe citar evidencia de disco y **cerrar cada
hallazgo adversario** como `resuelto`, `mitigado` o `descartado (motivo)`.

## Reglas de la fase de refinado (W5)

- El refinador solo puede tomar elementos del veredicto ganador; injertos de otras propuestas van
  marcados como `[injerto: Pn]` y justificados.
- Todo hallazgo no resuelto debe existir como TASK o como riesgo aceptado en BLUEPRINT.
- Los refinadores no editan ficheros del otro; escriben en rutas disjuntas.

## Validación

`scripts/check-blueprint.sh` valida estructura, unicidad de IDs, criterios de aceptación y
referencias cruzadas. Es la única puerta de salida del goal.
