// consent.go — suelo legal con memoria (EP-11 T-1102, A3-10/A3-14,
// injerto 5 de V3): cada alta y cada baja llevan fecha en la fila del
// nodo. consent_at nace con el alta (puerta 428 del enroll) y optout_at
// con la baja; leaves guarda la historia y bloquea el re-clutaje del
// barrido >= 12 meses.
package services

import (
	"context"
	"fmt"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
)

// LeaveMethods son las cuatro salidas tipadas del CHECK de leaves
// (0003): la puerta HTTP del dueño, el bye del heartbeat, el panel y
// la URL publica de transparencia (owner_url).
var LeaveMethods = []string{"http", "heartbeat_bye", "panel", "owner_url"}

// ValidLeaveMethod casa el metodo contra el CHECK de la tabla.
func ValidLeaveMethod(method string) bool {
	for _, m := range LeaveMethods {
		if m == method {
			return true
		}
	}
	return false
}

// StampOptout es la baja fechada en una sola llamada: estampa
// optout_at en la fila del nodo e inserta la fila historica en leaves
// (con reprobe_blocked_until +12 meses dentro de InsertLeave). El
// nodo desconocido propagan repo.ErrNotFound para que el handler
// responda 404 sin filtrar existencia.
func StampOptout(ctx context.Context, rp *repo.Data, nodeID, method, reason, code string) error {
	if !ValidLeaveMethod(method) {
		return fmt.Errorf("services: metodo de baja desconocido: %q", method)
	}
	if err := rp.MarkOptout(ctx, nodeID); err != nil {
		return err
	}
	return rp.InsertLeave(ctx, nodeID, method, reason, code)
}
