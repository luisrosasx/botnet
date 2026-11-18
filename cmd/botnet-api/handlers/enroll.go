package handlers

import (
	"bytes"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/services"
	"github.com/botnet/botnet/pkg/proto"
)

// enroll — POST /v1/enroll (T-0802). El device_token viaja en cabecera
// (A3-01); el body es el JSON de pkg/proto (csr.json) y la clave
// privada nunca entro aqui: solo el CSR (A3-02). El node_secret sale
// una sola vez, en este 201 (A3-06).
func enroll(d *Deps) fiber.Handler {
	svc := &services.EnrollDeps{
		Repo:         d.Repo,
		RDB:          d.RDB,
		CA:           d.CA,
		TicketSecret: d.Cfg.TicketSecret,
		Rendezvous:   d.Cfg.Rendezvous,
	}
	return func(c *fiber.Ctx) error {
		token := c.Get("X-Device-Token")
		if token == "" {
			return jsonError(c, http.StatusBadRequest, "falta X-Device-Token")
		}
		req, err := proto.DecodeEnrollAPIRequest(bytes.NewReader(c.Body()))
		if err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		ticket := c.Get("X-Intrusion-Ticket")
		resp, err := svc.Enroll(c.Context(), req, token, ticket, c.IP())
		if err != nil {
			return mapEnrollError(c, err, resp.NodeID)
		}
		if d.Log != nil {
			// Log de acceso minimo: la clave privada y el secreto no
			// aparecen jams (grep BEGIN PRIVATE KEY en logs = 0).
			d.Log.WriteString("enroll ok node=" + resp.NodeID + " slug=" + resp.Slug + "\n")
		}
		return c.Status(http.StatusCreated).JSON(resp)
	}
}

// mapEnrollError fija el codigo exacto del contrato: gates 409/428/403/
// 409, CSR forjado 400, politica CN/arch 422. Los dos 409 llevan ademas
// el node_id que resuelve el conflicto (T-0302 AC2 y T-0305 AC2): el
// del nodo existente en token conocido, el del dueno del ticket en
// ticket consumido; el agente con reset de fabrica continua sin el
// 7-claves del 201.
func mapEnrollError(c *fiber.Ctx, err error, nodeID string) error {
	switch {
	case errors.Is(err, services.ErrTokenKnown), errors.Is(err, services.ErrTicketUsed):
		if nodeID != "" {
			return c.Status(http.StatusConflict).JSON(fiber.Map{"error": err.Error(), "node_id": nodeID})
		}
		return jsonError(c, http.StatusConflict, err.Error())
	case errors.Is(err, services.ErrNoTicket), errors.Is(err, services.ErrNoConsent):
		return jsonError(c, http.StatusPreconditionRequired, err.Error())
	case errors.Is(err, proto.ErrTicketForged):
		return jsonError(c, http.StatusForbidden, err.Error())
	case errors.Is(err, services.ErrCSRForged):
		return jsonError(c, http.StatusBadRequest, err.Error())
	case errors.Is(err, services.ErrCNPolicy), errors.Is(err, services.ErrKeyWeak), errors.Is(err, services.ErrArch):
		return jsonError(c, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, proto.ErrInvalidLen):
		return jsonError(c, http.StatusBadRequest, err.Error())
	default:
		return jsonError(c, http.StatusInternalServerError, err.Error())
	}
}
