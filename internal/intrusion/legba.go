package intrusion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Protocols son los cuatro protocolos que el runner legba cubre con el
// diccionario por defecto (T-0201 criterio 3).
var Protocols = []string{"http", "ssh", "telnet", "ftp"}

// LegbaHit es la credencial ganadora tal como la reporta legba: protocolo y
// posición de la entrada en la salida del escáner.
type LegbaHit struct {
	Protocol  string
	CredIndex int
}

// LegbaVerdict es el objeto JSON que el runner imprime en stdout. Los tres
// campos viajan siempre —null cuando no hay puerta— para que el consumidor
// no tenga que distinguir clave ausente de valor nulo.
type LegbaVerdict struct {
	Protocol   *string `json:"protocol"`
	CredIndex  *int    `json:"cred_index"`
	ModelGuess *string `json:"model_guess"`
	OK         bool    `json:"ok"`
}

// ErrNoLegbaHit indica que la salida de legba no reporta ninguna credencial
// válida: el target está sano (T-0201 criterio 2).
var ErrNoLegbaHit = fmt.Errorf("legba: sin credencial válida")

// legbaCred es una entrada de credencial en los formatos JSON que legba puede
// emitir según versión: lista plana "credentials"/"results", o mapa por
// protocolo. proto vacío significa que el mapa externo aporta la clave.
type legbaCred struct {
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ParseLegbaOutput convierte la salida cruda de legba (JSON o texto) en el
// veredicto de la primera credencial válida. El formato texto canónico que
// documenta el runbook: "[SUCCESS] <proto> <host>:<port> <user>:<pass>".
func ParseLegbaOutput(raw []byte) (LegbaHit, error) {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return parseLegbaJSON(trimmed)
	}
	return parseLegbaText(raw)
}

func parseLegbaJSON(raw []byte) (LegbaHit, error) {
	var credList []legbaCred
	switch {
	case json.Valid(raw):
		var doc struct {
			Credentials []legbaCred `json:"credentials"`
			Results     []legbaCred `json:"results"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return LegbaHit{}, err
		}
		if len(doc.Credentials) > 0 {
			credList = doc.Credentials
		} else {
			credList = doc.Results
		}
		if len(credList) == 0 {
			var arr []legbaCred
			if err := json.Unmarshal(raw, &arr); err == nil {
				credList = arr
			}
		}
	}
	if len(credList) == 0 {
		// Forma por protocolo: {"http":[{"username":...}]} — el protocolo es la clave.
		var byProto map[string][]legbaCred
		if err := json.Unmarshal(raw, &byProto); err == nil {
			for _, proto := range Protocols {
				if creds, ok := byProto[proto]; ok && len(creds) > 0 {
					return LegbaHit{Protocol: proto, CredIndex: 0}, nil
				}
			}
		}
		return LegbaHit{}, ErrNoLegbaHit
	}
	for i, c := range credList {
		proto := strings.ToLower(c.Protocol)
		if oneOf(proto, Protocols) && (c.Username != "" || c.Password != "") {
			return LegbaHit{Protocol: proto, CredIndex: i}, nil
		}
	}
	return LegbaHit{}, ErrNoLegbaHit
}

var legbaTextRE = regexp.MustCompile(`(?i)\[(SUCCESS|WIN|OK)\]\s+(?:\w+/)?(http|ssh|telnet|ftp)\s+\S+\s+\S+`)

func parseLegbaText(raw []byte) (LegbaHit, error) {
	for _, line := range strings.Split(string(raw), "\n") {
		m := legbaTextRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		// El modo texto solo garantiza el primer acierto: cred_index = 0
		// (la posición exacta en el diccionario solo la da el modo JSON).
		return LegbaHit{Protocol: strings.ToLower(m[2]), CredIndex: 0}, nil
	}
	return LegbaHit{}, ErrNoLegbaHit
}

// modelPatterns asocia tokens de banner/firmware con un modelo normalizado.
// El orden importa: el patrón más específico gana si la evidencia menciona
// ambos (la marca y el modelo suelen salir en la misma línea HTTP title).
var modelPatterns = []struct {
	re    *regexp.Regexp
	model string
}{
	{regexp.MustCompile(`(?i)TL-WR\s*(\d{3,4})`), ""}, // el modelo se extrae del match
	{regexp.MustCompile(`(?i)TL-WDR\s*(\d{3,4})`), ""},
	{regexp.MustCompile(`(?i)Archer\s*C?\s*(\d{2,3})`), ""},
	{regexp.MustCompile(`(?i)DIR-\s*(\d{3,4})`), ""},
	{regexp.MustCompile(`(?i)DSL-\s*(\d{3,4})`), ""},
	{regexp.MustCompile(`(?i)HG\s?532`), "HG532"},
	{regexp.MustCompile(`(?i)ZXHN\s*([A-Z]\d+[A-Z]*)`), ""},
	{regexp.MustCompile(`(?i)DS[/-]?(\d{4})`), ""},
	{regexp.MustCompile(`(?i)Mi\s?Router\s*([A-Za-z0-9-]+)`), ""},
}

// GuessModel infiere el modelo del dispositivo desde evidencia de banner
// (HTTP title, banner telnet/SSH, firmware string). Es un indicio para el
// JSON del runner, no una clasificación: sin coincidencia devuelve ok=false.
func GuessModel(banner string) (string, bool) {
	for _, p := range modelPatterns {
		m := p.re.FindStringSubmatch(banner)
		if m == nil {
			continue
		}
		if p.model != "" {
			return p.model, true
		}
		return strings.TrimSpace(m[0]), true
	}
	return "", false
}

// WriteVerdict Marshalea el veredicto a JSON compacto en w.
func WriteVerdict(w io.Writer, v LegbaVerdict) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}
