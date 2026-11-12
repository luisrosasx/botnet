// geo.go — POST /v1/nodes/{id}/geo (EP-07 T-0704): el script
// scripts/mesh/geo_probe.sh sube el resultado de la sonda de exit
// (pais/ASN observados saliendo por el gateway) y el backend fija
// geo_verified + country_probed. Bearer admin como revoke: es plano
// de operaciones, nunca el agente quien reporta su geo (el
// autorreporte no se creia ni antes).
package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
)

// geoProbe: {country, asn, city?, bytes_tx?} -> {geo_verified,
// country_probed}. 401 sin bearer admin; 404 nodo desconocido; 400
// lectura de sonda invalida (pais de 2 chars obligatorio).
func geoProbe(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		auth := c.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != d.Cfg.AdminToken {
			return jsonError(c, http.StatusUnauthorized, "bearer admin requerido")
		}
		nodeID := c.Params("node_id")
		var reading services.GeoReading
		if err := c.BodyParser(&reading); err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		verified, err := services.ApplyGeoProbe(c.Context(), d.Repo, nodeID,
			services.StaticObserver{Reading: reading})
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "nodo desconocido")
		}
		if errors.Is(err, services.ErrGeoReading) {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"node_id":        nodeID,
			"geo_verified":   verified,
			"country_probed": strings.ToUpper(reading.Country),
		})
	}
}
