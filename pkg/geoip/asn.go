// Package geoip resuelve el plano de control por ASN: la condicion
// A1-18 del BLUEPRINT §5 exige >= 2 ASNs de control para que una ACL por
// prefijo del ISP no apague la cohorte entera. F2 usa una tabla fixture
// (prefijo -> ASN) leida desde config/env; el GeoIP2 real entra en el
// gateway cuando el MMDB del soak este versionado (geoip2-golang ya esta
// en go.mod).
package geoip

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// ASNTable es la coleccion de prefijos con su ASN, resuelta por
// coincidencia de prefijo mas largo (la semantica real de un RIB).
type ASNTable struct {
	entries []asnEntry
}

type asnEntry struct {
	prefix *net.IPNet
	asn    uint32
}

// ParseASNTable lee lineas "prefix asn" (CIDR o IP suelto = host) y
// ignora comentarios '#'. El orden de las lineas no importa: Lookup
// elige siempre el prefijo mas especifico.
func ParseASNTable(r io.Reader) (*ASNTable, error) {
	t := &ASNTable{}
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		fields := strings.Fields(s)
		if len(fields) != 2 {
			return nil, fmt.Errorf("geoip: linea %d: want \"prefix asn\", got %q", line, s)
		}
		_, pnet, err := net.ParseCIDR(fields[0])
		if err != nil {
			ip := net.ParseIP(fields[0])
			if ip == nil {
				return nil, fmt.Errorf("geoip: linea %d: prefijo invalido %q", line, fields[0])
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			pnet = &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
		}
		asn, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("geoip: linea %d: ASN invalido %q", line, fields[1])
		}
		t.entries = append(t.entries, asnEntry{prefix: pnet, asn: uint32(asn)})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return t, nil
}

// Lookup devuelve el ASN del prefijo mas especifico que contiene a ip.
func (t *ASNTable) Lookup(ip net.IP) (uint32, bool) {
	if t == nil || ip == nil {
		return 0, false
	}
	var best *asnEntry
	bestBits := -1
	for i := range t.entries {
		e := &t.entries[i]
		if !e.prefix.Contains(ip) {
			continue
		}
		ones, _ := e.prefix.Mask.Size()
		if ones > bestBits {
			best, bestBits = e, ones
		}
	}
	if best == nil {
		return 0, false
	}
	return best.asn, true
}

// ASNOfGateway resuelve el ASN de un destino "host:port": si el host es
// literal IP se consulta la tabla; si es nombre, look lo resuelve
// (fixture/DNS) y se consulta la tabla con la primera IP. ok=false
// cuando el destino cae fuera de toda tabla — la condicion A1-18 exige
// que el pool de gateways cubra >= 2 ASNs, y el rdv loguea los desconocidos.
func (t *ASNTable) ASNOfGateway(addr string, look func(host string) ([]string, error)) (uint32, bool) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ips := []string{host}
	if net.ParseIP(host) == nil {
		if look == nil {
			return 0, false
		}
		resolved, err := look(host)
		if err != nil || len(resolved) == 0 {
			return 0, false
		}
		ips = resolved
	}
	for _, s := range ips {
		if asn, ok := t.Lookup(net.ParseIP(s)); ok {
			return asn, true
		}
	}
	return 0, false
}

// DefaultFixture es la tabla de prueba de la condicion A1-18: dos ASN
// privados de ejemplo cubriendo los rangos de gateways de las fixtures.
// No es el RIB de Internet: es el doble determinista del soak.
func DefaultFixture() *ASNTable {
	t, err := ParseASNTable(strings.NewReader(`
# gateway A (ASN de ejemplo 64501) y B (64502)
192.0.2.0/26 64501
198.51.100.0/26 64502
`))
	if err != nil {
		panic("geoip: fixture invalida: " + err.Error())
	}
	return t
}
