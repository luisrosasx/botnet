package proto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// El alta es bootstrap: ocurre antes de existir el tunel TLS/H2, por eso
// viaja como JSON sobre HTTPS (csr.json) mientras el canal de control
// sigue prohibiendo JSON. Los structs de abajo son el contrato HTTP de
// POST /v1/enroll y GET /t/{slug} (T-0302, T-0304, T-0305).

// ErrTicketFormed es un X-Intrusion-Ticket con HMAC que no calza.
var ErrTicketForged = errors.New("proto: forged intrusion ticket")

// EnrollAPIRequest es el body de POST /v1/enroll segun csr.json. El
// device_token viaja en la cabecera X-Device-Token, nunca aqui ni en la
// URL (A3-01).
type EnrollAPIRequest struct {
	Arch     string `json:"arch"`
	Model    string `json:"model"`
	Firmware string `json:"fw"`
	MAC      string `json:"mac"`
	CSRPEM   string `json:"csr"` // PEM del CSR; la clave privada no sale
	// ConsentAt es la marca de consentimiento del dueño (EP-11
	// T-1102, A3-10): RFC3339 con el que el agente firmo el alta.
	// Sin marca no hay alta: el backend responde 428.
	ConsentAt string `json:"consent_at"`
}

// DecodeEnrollAPIRequest exige csr no vacia: el backend responde 400 si
// falta el campo (T-0302 AC3).
func DecodeEnrollAPIRequest(r io.Reader) (EnrollAPIRequest, error) {
	var req EnrollAPIRequest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("proto: enroll request: %w", err)
	}
	if strings.TrimSpace(req.CSRPEM) == "" {
		return req, fmt.Errorf("proto: enroll request: empty csr: %w", ErrInvalidLen)
	}
	return req, nil
}

// EnrollAPIResponse es la respuesta del alta: las 7 claves de identidad
// siempre presentes (T-0302 AC1) mas las claves opcionales de
// entitlements (T-0306 AC3: monthly_gb_cap=150 ya llega en el 201).
// NodeSecretB64 es el unico lugar donde viaja el node_secret (A3-06).
type EnrollAPIResponse struct {
	NodeID      string   `json:"node_id"` // uuid canonico
	Slug        string   `json:"slug"`
	CertPEM     string   `json:"cert_pem"`
	CAPEM       string   `json:"ca_pem"`
	NodeSecretB string   `json:"node_secret"` // base64 estandar de 32 B
	Rendezvous  []string `json:"rendezvous"`
	TTLS        uint32   `json:"ttl_s"`

	// Extensiones opcionales de entitlements; 0/vacio = ausente.
	BandwidthCapKbps uint32 `json:"bandwidth_cap_kbps,omitempty"`
	MonthlyGbCap     uint32 `json:"monthly_gb_cap,omitempty"`
	MaxStreams       uint32 `json:"max_streams,omitempty"`
	StableVersion    string `json:"stable_version,omitempty"`
	ConfigRev        uint32 `json:"config_rev,omitempty"`
}

// enrollRequiredKeys son las 7 claves exactas del contrato de T-0302.
var enrollRequiredKeys = []string{
	"node_id", "slug", "cert_pem", "ca_pem", "node_secret", "rendezvous", "ttl_s",
}

// DecodeEnrollAPIResponse exige las 7 claves y rechaza claves
// desconocidas: si el backend manda una octava clave no pactada, el
// agente lo nota en el alta, no se calla.
func DecodeEnrollAPIResponse(r io.Reader) (EnrollAPIResponse, error) {
	raw := map[string]json.RawMessage{}
	dec := json.NewDecoder(r)
	if err := dec.Decode(&raw); err != nil {
		return EnrollAPIResponse{}, fmt.Errorf("proto: enroll response: %w", err)
	}
	for _, k := range enrollRequiredKeys {
		if _, ok := raw[k]; !ok {
			return EnrollAPIResponse{}, fmt.Errorf("proto: enroll response: missing key %q: %w", k, ErrInvalidLen)
		}
	}
	known := map[string]bool{}
	for _, k := range append(append([]string{}, enrollRequiredKeys...),
		"bandwidth_cap_kbps", "monthly_gb_cap", "max_streams", "stable_version", "config_rev") {
		known[k] = true
	}
	for k := range raw {
		if !known[k] {
			return EnrollAPIResponse{}, fmt.Errorf("proto: enroll response: unknown key %q: %w", k, ErrInvalidLen)
		}
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return EnrollAPIResponse{}, err
	}
	var m EnrollAPIResponse
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("proto: enroll response: %w", err)
	}
	if len(m.NodeSecretB) > 0 {
		sec, err := base64.StdEncoding.DecodeString(m.NodeSecretB)
		if err != nil || len(sec) != 32 {
			return m, fmt.Errorf("proto: enroll response: node_secret %d B != 32: %w", len(sec), ErrInvalidLen)
		}
	}
	return m, nil
}

// SlugLen es el tamano fijo del slug de transparencia (A3-01: GET
// /t/{slug}, 12 chars alfanumericos).
const SlugLen = 12

var slugRe = regexp.MustCompile(`^[A-Za-z0-9]{12}$`)

// ValidSlug rechaza device_tokens (que suelen ser largos) como URL de
// transparencia: GET /t/$DEVICE_TOKEN cae a 404 (T-0304 AC1).
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

const slugAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// NewSlug genera un slug de 12 chars con crypto/rand. Con 62^12 espacio
// las colisiones entre 50 enrols son despreciables y el backend impone
// UNIQUE (nodes.slug) de todos modos.
func NewSlug() (string, error) {
	var b [SlugLen]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("proto: NewSlug: %w", err)
	}
	out := make([]byte, SlugLen)
	for i, c := range b {
		out[i] = slugAlphabet[int(c)%len(slugAlphabet)]
	}
	return string(out), nil
}

// TransparencyPath es la ruta publica del dueno: la credencial nunca
// aparece aqui (T-0304 AC2: 0 ocurrencias del token en el access.log).
func TransparencyPath(slug string) string { return "/t/" + slug }

// Ticket es el ticket de intrusion que el probe firma al sondear el
// dispositivo real (A3-11): llega en la cabecera X-Intrusion-Ticket y
// el alta la gana quien lo porta.
type Ticket struct {
	TokenHash string // sha256 hex del device_token (64 chars)
	ProbeID   string
	IAT       int64 // unix seconds
}

const ticketPrefix = "v1"

func (t Ticket) canonical() string {
	return ticketPrefix + "." + t.TokenHash + "." + t.ProbeID + "." + strconv.FormatInt(t.IAT, 10)
}

func ticketHMAC(secret []byte, canonical string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

// SignTicket produce el wire format v1.<token_hash>.<probe_id>.<iat>.<hmac_hex>.
func SignTicket(secret []byte, t Ticket) string {
	return t.canonical() + "." + hex.EncodeToString(ticketHMAC(secret, t.canonical()))
}

// VerifyTicket parsea y verifica en tiempo constante; ErrTicketForged si
// el HMAC no calza. El TTL se comprueba aparte (el reloj del router de
// 2009 no es fiable: el backend usa su propio now solo para caducidad).
func VerifyTicket(secret []byte, wire string) (Ticket, error) {
	parts := strings.Split(wire, ".")
	if len(parts) != 5 || parts[0] != ticketPrefix {
		return Ticket{}, fmt.Errorf("proto: malformed intrusion ticket: %w", ErrInvalidLen)
	}
	iat, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return Ticket{}, fmt.Errorf("proto: malformed intrusion ticket iat: %w", ErrInvalidLen)
	}
	t := Ticket{TokenHash: parts[1], ProbeID: parts[2], IAT: iat}
	want, err := hex.DecodeString(parts[4])
	if err != nil {
		return Ticket{}, ErrTicketForged
	}
	got := ticketHMAC(secret, t.canonical())
	if subtle.ConstantTimeCompare(want, got) != 1 {
		return t, ErrTicketForged
	}
	return t, nil
}

// TicketExpired es la unica comprobacion de reloj que se permite al
// ticket: comparacion de monotonas del backend, no del dispositivo.
func TicketExpired(t Ticket, now time.Time, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	age := now.Sub(time.Unix(t.IAT, 0))
	return age > ttl || age < -ttl
}

// HashDeviceToken es el hash que el backend guarda (device_token_hash) y
// el que viaja dentro del ticket: el token en claro no se loguea nunca.
func HashDeviceToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
