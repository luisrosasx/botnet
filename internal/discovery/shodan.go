package discovery

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
)

// shodanSearch es la envoltura de /shodan/search (respuesta "matches";
// los exports por lote usan "results": se aceptan ambos).
type shodanSearch struct {
	Results []shodanMatch `json:"results"`
	Matches []shodanMatch `json:"matches"`
}

type shodanMatch struct {
	IPNum     uint32 `json:"ip"`
	IPStr     string `json:"ip_str"`
	Port      int    `json:"port"`
	Transport string `json:"transport"`
	Product   string `json:"product"`
	Server    string `json:"server"`
	Device    struct {
		Vendor string `json:"vendor"`
		Model  string `json:"model"`
	} `json:"device"`
	Location struct {
		ASN         int    `json:"asn"`
		CountryCode string `json:"country_code"`
	} `json:"location"`
	Timestamp string `json:"timestamp"`
}

// ParseShodanSearch convierte la respuesta JSON de la API de Shodan en
// candidatos source="shodan". El ip viene como entero big-endian y/o
// ip_str; se prefiere ip_str y se deriva del entero si falta. El
// timestamp de Shodan queda en SeenAt a titulo informativo: el upsert
// fija last_seen_at = now() (criterio T-0102.3).
func ParseShodanSearch(r io.Reader) ([]Candidate, error) {
	var s shodanSearch
	// Shodan manda campos de sobra (tls, os, cpe...); se decodifica
	// tolerante y solo se leen los del embudo.
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return nil, fmt.Errorf("discovery: shodan json: %w", err)
	}
	matches := s.Matches
	if len(matches) == 0 {
		matches = s.Results
	}
	out := make([]Candidate, 0, len(matches))
	for i, m := range matches {
		ip := strings.TrimSpace(m.IPStr)
		if ip == "" {
			if m.IPNum == 0 {
				return nil, fmt.Errorf("discovery: shodan match %d: no ip", i)
			}
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], m.IPNum)
			ip = net.IP(b[:]).String()
		}
		c := Candidate{
			IP:      ip,
			Port:    m.Port,
			ASN:     m.Location.ASN,
			Country: iso2(m.Location.CountryCode),
			Vendor:  strings.TrimSpace(m.Device.Vendor),
			Model:   strings.TrimSpace(m.Device.Model),
			Source:  "shodan",
		}
		if svc, ok := InferService(m.Port); ok {
			c.Service = svc
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func iso2(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) != 2 {
		return ""
	}
	return s
}
