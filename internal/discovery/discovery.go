// Package discovery implementa el pipeline EP-01 (BLUEPRINT §15 F1):
// inventario de dispositivos abiertos (routers, camaras, NAS) desde
// Shodan API y barrido masscan, clasificacion de banners estilo zgrab2,
// informe por ASN/pais y consulta de seleccion que respeta opt-outs.
//
// El DDL vive en migrations/ (0002_discovered.up.sql); la tabla objetivo
// es discovered con UNIQUE (ip, port): toda ingesta es un upsert
// idempotente que solo refina columnas con COALESCE y renueva
// last_seen_at. Los parseadores aceptan la sintaxis real de Shodan,
// masscan (NDJSON) y zgrab2 (JSON anidado) para que los scripts puedan
// alternar entre binario real y fixture sin cambiar glue.
package discovery

import (
	"fmt"
	"strings"
	"time"
)

// Candidate es una fila de discovered lista para upsert. Campos vacios
// se traducen a NULL; el upsert conserva el valor previo (COALESCE).
type Candidate struct {
	IP               string    `json:"ip"`
	Port             int       `json:"port"`
	Service          string    `json:"service,omitempty"`
	Vendor           string    `json:"vendor,omitempty"`
	Model            string    `json:"model,omitempty"`
	ASN              int       `json:"asn,omitempty"`
	Country          string    `json:"country,omitempty"`
	DeviceClassGuess string    `json:"device_class_guess,omitempty"`
	BannerHash       string    `json:"banner_hash,omitempty"`
	Source           string    `json:"source"`
	SeenAt           time.Time `json:"seen_at,omitempty"`
}

// Device classes espejadas por el CHECK de la migracion.
const (
	ClassRouter  = "router"
	ClassCamera  = "camera"
	ClassNAS     = "nas"
	ClassUnknown = "unknown"
)

// serviceByPort es la inferencia de servicio por puerto abierto que
// pide T-0103 (23,80,8080,554,32210 entre otros). Curada a puertos de
// router/camara/NAS: un mapa exhaustivo IANA inflaria el binario sin
// beneficio para el embudo de descubrimiento.
var serviceByPort = map[int]string{
	21:    "ftp",
	22:    "ssh",
	23:    "telnet",
	25:    "smtp",
	53:    "dns",
	80:    "http",
	110:   "pop3",
	143:   "imap",
	443:   "https",
	554:   "rtsp",
	993:   "imaps",
	995:   "pop3s",
	1080:  "socks",
	1433:  "ms-sql-tds",
	1723:  "pptp",
	1883:  "mqtt",
	1900:  "ssdp",
	2323:  "telnet-alt",
	3000:  "http-alt",
	3306:  "mysql",
	3389:  "ms-wbt",
	4444:  "ntrtp",
	5060:  "sip",
	5432:  "postgresql",
	5555:  "miniupnpd",
	5900:  "vnc",
	7547:  "tr069",
	8000:  "http-alt",
	8080:  "http-proxy",
	8443:  "https-alt",
	8856:  "wemo",
	9000:  "ocs",
	10000: "netbackupper",
	32210: "redstorm",
	49152: "upnp",
	50000: "upnp",
}

// InferService devuelve el servicio sugerido por numero de puerto
// abierto; ok=false cuando el puerto no esta curado (queda NULL en
// discovered y el barrido lo puede reintentar tras ampliar el mapa).
func InferService(port int) (string, bool) {
	s, ok := serviceByPort[port]
	return s, ok
}

// Validate descarta candidatos inservibles antes de tocar la BD: ip
// presente, puerto en rango. country solo admite codigos ISO de 2
// caracteres (columna char(2)).
func (c Candidate) Validate() error {
	if strings.TrimSpace(c.IP) == "" {
		return fmt.Errorf("discovery: empty ip")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("discovery: port out of range: %d", c.Port)
	}
	if c.Country != "" && len(c.Country) != 2 {
		return fmt.Errorf("discovery: country must be ISO2, got %q", c.Country)
	}
	return nil
}
