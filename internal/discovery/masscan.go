package discovery

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ParseMasscanNDJSON lee el stdout de `masscan ... --output-format json`
// (una linea JSON por puerto abierto: {"ip","port","protocol","frame"})
// y produce candidatos source="masscan" con service inferido por puerto.
// Las lineas vacias se toleran: masscan escribe un banner de version por
// stderr, pero algunos envoltorios dejan lineas en blanco.
func ParseMasscanNDJSON(r io.Reader) ([]Candidate, error) {
	var out []Candidate
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 0; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || !strings.HasPrefix(raw, "{") {
			continue
		}
		// masscan emite dos formas: una linea por puerto (ip/port) o una
		// linea por host con `ports[]`. Se aceptan las dos.
		var m struct {
			IP       string `json:"ip"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Ports    []struct {
				Port   int    `json:"port"`
				Proto  string `json:"proto"`
				Status string `json:"status"`
			} `json:"ports"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("discovery: masscan line %d: %w", line+1, err)
		}
		if m.IP != "" && len(m.Ports) > 0 {
			for _, pt := range m.Ports {
				if strings.EqualFold(pt.Proto, "udp") || (pt.Status != "" && pt.Status != "open") {
					continue
				}
				c := Candidate{IP: m.IP, Port: pt.Port, Source: "masscan"}
				if svc, ok := InferService(pt.Port); ok {
					c.Service = svc
				}
				if err := c.Validate(); err != nil {
					continue
				}
				out = append(out, c)
			}
			continue
		}
		if strings.EqualFold(m.Protocol, "udp") {
			continue // la tabla discovered modela tcp (handshake para zgrab2)
		}
		c := Candidate{IP: m.IP, Port: m.Port, Source: "masscan"}
		if svc, ok := InferService(m.Port); ok {
			c.Service = svc
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("discovery: masscan line %d: %w", line+1, err)
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// SweepRateForRange traduce la cuota de caudal del runbook (<=5 Mbps
// sostenidos, BRIEF-BASE punto 6) a --rate de masscan: con ~44 B por
// SYN, 5 Mbps ~= 14 200 pps; se queda 30 % por debajo para amortiguar
// retransmisiones y la ventana de respuesta.
func SweepRate(vCPU int) int {
	// rate = pps; el limite fisico es del enlace, no del core, pero
	// multi-vCPU multiplica el burst efectivo: se divide entre cores
	// con suelo de 1500 pps.
	const basePPS = 10_000
	if vCPU < 1 {
		vCPU = 1
	}
	rate := basePPS / vCPU
	if rate < 1_500 {
		rate = 1_500
	}
	return rate
}
