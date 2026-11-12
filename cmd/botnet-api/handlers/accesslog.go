// accesslog.go — registro de acceso del backend (EP-11 verificacion):
// una linea por peticion con ts, ip, metodo, path, status y duracion.
// El Criterio operational es que el token del dueño NUNCA asome aqui
// (grep -c $TOKEN access.log = 0): las rutas publicas van por slug y
// las credenciales viajan en headers, jamas en la URL.
package handlers

import (
	"io"
	"os"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

// accessLog — middleware de registro. Deps.AccessLog nil = apagado
// (dev/tests); en produccion main.go lo abre en append desde
// BOTNET_ACCESS_LOG (p. ej. /var/log/botnet-api/access.log).
func accessLog(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		var f *os.File
		if d != nil {
			f = d.AccessLog
		}
		if f != nil {
			// Una sola escritura por linea: con O_APPEND el kernel
			// intercala peticiones sin partir registros.
			_, _ = io.WriteString(f, accessLine(start, c))
		}
		return err
	}
}

// accessLine — ts ip metodo path status duracion, una linea fija y
// sin comas: `awk '{print $4}'` lista los paths golpeados.
func accessLine(start time.Time, c *fiber.Ctx) string {
	ms := time.Since(start).Round(100 * time.Microsecond)
	return start.UTC().Format(time.RFC3339) + " " +
		c.IP() + " " + c.Method() + " " + c.Path() + " " +
		strconv.Itoa(c.Response().StatusCode()) + " " + ms.String() + "\n"
}
