package discovery

import (
	"fmt"
	"strings"
)

// OptoutWindowMonths es la ventana de re-clutaje que fija el
// BLUEPRINT §13 (leaves bloquea >= 12 meses).
const OptoutWindowMonths = 12

// SelectQueryWithOptout es la seleccion canonica de T-0106: candidatos
// con servicio inferido y recientes, excluyendo IPs cuyo node tiene
// optout_at dentro de 12 meses o un leave con reprobe_blocked_until
// futuro. Orden por last_seen_at DESC para que el indice parcial
// discovered_candidate_pick de (last_seen_at DESC) WHERE service IS NOT
// NULL se use como Index Scan con LIMIT.
func SelectQueryWithOptout() string {
	return `SELECT d.ip, d.port, d.service, d.asn, d.country
FROM discovered d
WHERE d.service IS NOT NULL
  AND d.last_seen_at > now() - interval '30 days'
  AND NOT EXISTS (
        SELECT 1 FROM nodes n
        WHERE n.public_ip = d.ip
          AND n.optout_at > now() - interval '12 months')
  AND NOT EXISTS (
        SELECT 1 FROM leaves l
        JOIN nodes n ON n.node_id = l.node_id
        WHERE n.public_ip = d.ip
          AND l.reprobe_blocked_until > now())
ORDER BY d.last_seen_at DESC
LIMIT $1;`
}

// SelectQueryBasic es la misma forma sin el anti-join, para bases de
// prueba sin nodes/leaves (EP-08 los crea despues; el script detecta
// con to_regclass). Mismo indice parcial.
func SelectQueryBasic() string {
	return `SELECT d.ip, d.port, d.service, d.asn, d.country
FROM discovered d
WHERE d.service IS NOT NULL
  AND d.last_seen_at > now() - interval '30 days'
ORDER BY d.last_seen_at DESC
LIMIT $1;`
}

// FormatSelectRow pinta la salida CSV de select_candidates.sh.
func FormatSelectRow(ip string, port int, service string, asn int, country string) string {
	a := ""
	if asn != 0 {
		a = fmt.Sprintf("%d", asn)
	}
	return strings.Join([]string{ip, fmt.Sprintf("%d", port), service, a, country}, ",")
}
