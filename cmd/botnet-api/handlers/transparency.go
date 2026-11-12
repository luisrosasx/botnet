// transparency.go — superficie publica del dueño (EP-11 T-1101/T-1107).
// Movido desde nodes.go para seguir el plano de ficheros de la task.
package handlers

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/pkg/proto"
)

// aceptaJSON — la JSON es un privilegio explicito: solo responde JSON
// quien pide application/json en el Accept (el panel Next lo manda en
// lib/api.ts). Un navegador o un curl con Accept: */* reciben HTML:
// /t/{slug} es una PAGINA, no un endpoint (T-1101: el dueño abre una
// URL y ve transparencia, no un volcado de keys).
func aceptaJSON(c *fiber.Ctx) bool {
	return strings.Contains(strings.ToLower(c.Get(fiber.HeaderAccept)), "application/json")
}

// transparencyHTML — la pagina minima de transparencia: mismas
// columnas publicables que la JSON (flota, modelo, clase, pais, topes,
// GB del mes, consentimiento fechado y leave_code). Todo valor del
// dueño va escapado: el HTML jamas interpreta datos del nodo.
func transparencyHTML(node map[string]any) string {
	gb, _ := node["gb_month"].(float64)
	cap, _ := node["monthly_gb_cap"].(int)
	bw, _ := node["bandwidth_cap_kbps"].(int)
	esc := func(k string) string { s, _ := node[k].(string); return html.EscapeString(s) }
	row := func(label, value string) string {
		return fmt.Sprintf("<tr><th>%s</th><td>%s</td></tr>\n", label, value)
	}
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"es\">\n<head>\n<meta charset=\"utf-8\">\n<title> BOT.NET — transparencia</title>\n</head>\n<body>\n")
	fmt.Fprintf(&b, "<h1>Nodo %s</h1>\n<table>\n", esc("slug"))
	b.WriteString(row("Flota", esc("status")))
	b.WriteString(row("Modelo", orDefault(esc("model"), "sin marca")))
	b.WriteString(row("Clase", orDefault(esc("device_class"), "desconocida")))
	b.WriteString(row("Pais", orDefault(esc("country"), "desconocido")))
	b.WriteString(row("Tope de ancho", fmt.Sprintf("%d kbps", bw)))
	b.WriteString(row("Cuota mensual", fmt.Sprintf("%d GB", cap)))
	b.WriteString(row("GB del mes", fmt.Sprintf("%.2f GB", gb)))
	b.WriteString(row("Consentimiento", esc("consent_date")))
	b.WriteString(row("Codigo de baja", orDefault(esc("leave_code"), "—")))
	b.WriteString("</table>\n</body>\n</html>\n")
	return b.String()
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// transparency — GET /t/{slug} (A3-01): la credencial jamas vive en la
// URL; un slug de 12 chars aleatorios no es numerable y el token del
// dueño no se filtra ni se loguea. Por defecto sirve la PAGINA HTML de
// transparencia (navegador y curl con Accept: */*); la JSON queda
// reservada a quien la pide explicitamente (Accept: application/json),
// que es como la consume el panel Next.
func transparency(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		slug := c.Params("slug")
		if !proto.ValidSlug(slug) {
			return jsonError(c, http.StatusNotFound, "slug invalido")
		}
		node, err := d.Repo.NodePublic(c.Context(), slug)
		if errors.Is(err, repo.ErrNotFound) {
			return jsonError(c, http.StatusNotFound, "slug desconocido")
		}
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if !aceptaJSON(c) {
			c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
			return c.Status(http.StatusOK).SendString(transparencyHTML(node))
		}
		return c.Status(http.StatusOK).JSON(node)
	}
}

// listNodes — GET /v1/nodes (contrato openapi, pendiente de montaje
// hasta EP-11): inventario publicable para el panel y el directorio
// /t (T-1107). Solo columnas publicables: el device_token jamas asoma.
func listNodes(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		nodes, err := d.Repo.NodesDirectory(c.Context(), c.Query("status"))
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(nodes)
	}
}
