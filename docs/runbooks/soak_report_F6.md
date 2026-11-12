# Informe soak 7 días en ISP real — placeholder (F6)

Condición R7 (BLUEPRINT §12/§16): la extensión del soak de 1.000
reinicios a **7 días en ISP real con pérdida** es puerta F6. Este
fichero se llena al cerrar F6 con la corrida del nodo residencial; la
aceleración de host la cubre `scripts/soak/1000_restarts.sh`
(runbook: `docs/runbooks/soak.md`).

## Casillas por rellenar

| Casilla | Valor |
|---|---|
| Ventana (7 días, fechas) | ______ |
| ISP / pérdida media | ______ |
| Reinicios `pkill -9` totales | ______ |
| Huérfanos al final (esperado: 0) | ______ |
| Pidfiles vivos al final (esperado: 1) | ______ |
| Revival medio del watchdog | ______ |
| Revocación observada < 60 s | ______ |

Comando de la corrida: ver `docs/runbooks/soak.md` §«Extrapolación».
