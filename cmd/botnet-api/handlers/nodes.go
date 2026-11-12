package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
)

// revoke — POST /v1/nodes/{id}/revoke (T-0803). Bearer del token de
// administracion (panel/operaciones): quema al nodo y publica el CRL.
func revoke(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != d.Cfg.AdminToken {
			return jsonError(c, http.StatusUnauthorized, "bearer admin requerido")
		}
		nodeID := c.Params("node_id")
		res, err := services.RevokeNode(c.Context(), d.RDB, d.Repo, nodeID)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "nodo desconocido")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(res)
	}
}

// healthz — T-0804 AC2: el gateway (y el backend con cache de keys) no
// declara salud hasta terminar el warmup de apikey:{sha256}.
func healthz(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if d.Keys != nil && !d.Keys.Warmed() {
			return jsonError(c, http.StatusServiceUnavailable, "warmup de api keys en curso")
		}
		return c.SendString("ok")
	}
}
