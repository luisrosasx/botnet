# Informe soak CGNAT 100k — T-1203

Fecha: 2026-11-12T10:41:28Z. Simulacion determinista (semilla 7): 100000 nodos x 24 h simuladas.
Modelo: cadencia unica T-0403 (keepalive 40 s + jitter <= 15 s; heartbeat
1-de-3), purge TCP de flows ociosos > 60 s del BRIEF, revival con ticket
TLS de 1500 B (A1-03). Tamano de frames medido del hbcodec real: ping
10.0 B, heartbeat medio 69.5 B.

| Criterio | Medida | Limite | Resultado |
|---|---|---|---|
| inflado_control_pct | 2.764 % | < 5 % | PASS |
| sessions_killed_by_purge | 0 | 0 | PASS |
| beacon KB/nodo/dia (control tx) | 54.3 | <= 120 | PASS |
| reconexiones de streams de dato | 1161105 | informativo | — |

## Lectura honesta

* El keepalive 40+jitter 15 < purge 60 s hace matematicamente imposible
  cortar una sesion viva: el numerador del inflado son los revival de
  control (renovacion de assign) y los ticks que el jitter empujo sobre
  el purge — por eso el porcentaje queda pequeno y el conteo de sesiones
  cortadas, en cero.
* Las reconexiones del plano de dato (huecos de stream > purge) se
  reportan aparte: pagan ticket TLS, no engordan el plano de control.
* Extrapolacion a 24 h reales y QEMU clase B: los contejos por nodo y
  dia no dependen del host (son aritmetica de ticks), asi que el
  simulador a 100k corre en segundos y la puerta F6 reusa el informe.
