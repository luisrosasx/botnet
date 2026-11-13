// leave.go — POST /v1/nodes/{id}/leave?code=XXXXXXXX (EP-07 T-0706,
// cierra A3-13). La puerta legal del dueño es su codigo de 8
// caracteres + rate-limit por IP (middleware LeaveRateLimit): el uuid
// del node es enumerable, la baja no. Codigo valido -> 200 y fila en
// leaves (method='http'); codigo equivocado -> 404 (no se filtra que
// el ID existe); sin codigo o malformed -> 403: el ID solo no basta
// para tumbar la flota.
package handlers

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
)

// leaveCodeRE: char(8) alfanumerico — mismo contrato que
// nodes.leave_code (migracion 0006) y leaves.leave_code char(8).
var leaveCodeRE = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

func leave(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		nodeID := c.Params("node_id")
		code := c.Query("code")
		if !leaveCodeRE.MatchString(code) {
			return jsonError(c, http.StatusForbidden, "codigo de baja de 8 caracteres requerido")
		}
		stored, issued, err := d.Repo.LeaveCodeIssued(c.Context(), nodeID)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "nodo desconocido")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		// S-18: caducidad configurable. Un codigo viejo ya no vale aunque
		// siga en la fila; se responde 403 y no 404 para no confundirlo
		// con un codigo que no coincide.
		if ttl := d.LeaveCodeTTL; ttl > 0 && !issued.IsZero() && d.now().Sub(issued) > ttl {
			return jsonError(c, http.StatusForbidden, "codigo de baja caducado")
		}
		// Comparacion en tiempo constante: la ventana de rate-limit
		// es la puerta dura; el char(8) no se filtra ni por timing.
		if subtle.ConstantTimeCompare([]byte(stored), []byte(code)) != 1 {
			return jsonError(c, http.StatusNotFound, "codigo no coincide")
		}
		// T-1102: la baja se firma con optout_at + fila leaves en una
		// sola llamada. method admite las puertas publicas (http,
		// panel, owner_url); heartbeat_bye la estampa el heartbeat.
		method := c.Query("method", "http")
		if !services.ValidLeaveMethod(method) || method == "heartbeat_bye" {
			method = "http"
		}
		if err := services.StampOptout(c.Context(), d.Repo, nodeID, method, "baja por puerta legal", code); err != nil {
			if errors.Is(err, repo.ErrNotFound) {
				return jsonError(c, http.StatusNotFound, "nodo desconocido")
			}
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		// La baja es por pull (T-0803): retiring -> el heartbeat
		// responde 410 con bye y el agente drena sin panel.
		if err := d.Repo.SetNodeStatus(c.Context(), nodeID, "retiring"); err != nil && !errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{"node_id": nodeID, "left": true})
	}
}

// now: reloj de la caducidad del código de baja (falso en tests).
func (d *Deps) now() time.Time {
	if d.nowFn != nil {
		return d.nowFn()
	}
	return time.Now()
}
