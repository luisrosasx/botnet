package agent

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/botnet/botnet/pkg/proto"
)

// EnrollConfig es el ceremonial del alta desde el dispositivo.
type EnrollConfig struct {
	Endpoint    string // URL base del plano de control (stub HTTPS en F1)
	DeviceToken string // cabecera X-Device-Token; jamas en la URL (A3-01)
	Ticket      string // X-Intrusion-Ticket firmado por el probe (A3-11)
	Arch        string // nodes.arch
	Model       string
	Firmware    string
	MAC         string
	NodeDir     string // <nodeDir>/node.json
	StateDir    string // <nodeDir>/state: secret.bin, claves, cert (0600)
	AgentVer    string
	// ConsentAt es la marca RFC3339 del consentimiento del dueno
	// recogida al instalar (EP-11 T-1102). Vacio → el agente la
	// estampa ahora: el consentimiento se pide en cada alta, nunca
	// se hereda de un refrito de firmware.
	ConsentAt string
	HTTP      *http.Client // nil → 15 s de timeout
}

// EnrollError lleva el codigo HTTP no triunfal para que el llamador
// distinga 400/403/428/409 sin parsear texto.
type EnrollError struct {
	Code int
	Body string
}

func (e *EnrollError) Error() string { return fmt.Sprintf("agent: /v1/enroll HTTP %d", e.Code) }

// EnrollStatus extrae el codigo de un EnrollError (0 si no lo es).
func EnrollStatus(err error) int {
	var e *EnrollError
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// EnrollOutcome es el resultado del ceremonial: identidad + material
// para persistir. Reenrolled=true cuando el 409 idempotente re-emite
// certificado sin fila nueva en nodes (T-0308).
type EnrollOutcome struct {
	Status      int
	Reenrolled  bool
	Resp        proto.EnrollAPIResponse
	NodeID      [16]byte
	NodeSecret  []byte
	KeyPEM      []byte
	CertPEM     []byte
	Fingerprint string // sha256 del DER del cert vigente ("openssl -fingerprint")
}

// GenerateEnrollKey crea el par del dispositivo. ECDSA P-256 en vez de
// RSA-2048: en un MIPS de clase C generar el par RSA cuesta segundos
// con GOMAXPROCS=1 y la CA de flota firma igual; la clave privada se
// queda en el dispositivo (A3-02).
func GenerateEnrollKey() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("agent: generar par: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	return key, pemBytes, nil
}

func createCSR(key *ecdsa.PrivateKey, model string) (string, error) {
	subj := pkix.Name{CommonName: "botnet-node", Organization: []string{"botnet"}}
	if model != "" {
		subj.OrganizationalUnit = []string{model}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: subj}, key)
	if err != nil {
		return "", fmt.Errorf("agent: CSR: %w", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

// certFingerprint imita `openssl x509 -noout -fingerprint -sha256`:
// sha256 del DER si el PEM parsea; si el stub manda un PEM sintentico,
// del string completo — lo que importa es que cambie en cada emision.
func certFingerprint(certPEM string) string {
	sum := sha256.Sum256([]byte(certPEM))
	if block, _ := pem.Decode([]byte(certPEM)); block != nil {
		sum = sha256.Sum256(block.Bytes)
	}
	return hex.EncodeToString(sum[:])
}

// Enroll ejecuta POST /v1/enroll: par generado en el dispositivo, CSR
// en el body, token y ticket en cabeceras. Un 409 con el mismo
// node_id se resuelve como re-emision (reenroll) y NO crea nodo nuevo.
// El node_secret solo viaja en el cuerpo de esta respuesta (A3-06).
func Enroll(ctx context.Context, c *EnrollConfig) (*EnrollOutcome, error) {
	if c == nil || c.Endpoint == "" || c.DeviceToken == "" {
		return nil, errors.New("agent: EnrollConfig requiere endpoint y device token")
	}
	key, keyPEM, err := GenerateEnrollKey()
	if err != nil {
		return nil, err
	}
	csrPEM, err := createCSR(key, c.Model)
	if err != nil {
		return nil, err
	}
	consentAt := c.ConsentAt
	if consentAt == "" {
		consentAt = time.Now().UTC().Format(time.RFC3339)
	}
	body, err := json.Marshal(proto.EnrollAPIRequest{
		Arch: c.Arch, Model: c.Model, Firmware: c.Firmware, MAC: c.MAC, CSRPEM: csrPEM,
		ConsentAt: consentAt,
	})
	if err != nil {
		return nil, err
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint+"/v1/enroll", bytesReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Token", c.DeviceToken)
	if c.Ticket != "" {
		req.Header.Set("X-Intrusion-Ticket", c.Ticket)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agent: POST /v1/enroll: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, fmt.Errorf("agent: /v1/enroll body: %w", err)
	}
	if resp.StatusCode == http.StatusConflict {
		// Contrato real del 409: {"error":..,"node_id":..} con el nodo
		// existente (T-0302 AC2). Un stub/doble que re-emite las 7 claves
		// sigue materializando estado; el body minimo NO lo hace: no hay
		// cert ni secreto nuevos que persistir.
		if api, fullErr := proto.DecodeEnrollAPIResponse(bytes.NewReader(respBody)); fullErr == nil {
			return finishEnroll(c, api, keyPEM, http.StatusConflict)
		}
		var minimal struct {
			Error  string `json:"error"`
			NodeID string `json:"node_id"`
		}
		if jerr := json.Unmarshal(respBody, &minimal); jerr != nil || minimal.NodeID == "" {
			return nil, &EnrollError{Code: resp.StatusCode, Body: string(respBody)}
		}
		nodeID, perr := ParseUUID(minimal.NodeID)
		if perr != nil {
			return nil, fmt.Errorf("agent: 409 con node_id ilegible: %w", perr)
		}
		return &EnrollOutcome{
			Status:     http.StatusConflict,
			Reenrolled: true,
			Resp:       proto.EnrollAPIResponse{NodeID: minimal.NodeID},
			NodeID:     nodeID,
			KeyPEM:     keyPEM,
		}, nil
	}
	if resp.StatusCode != http.StatusCreated {
		return nil, &EnrollError{Code: resp.StatusCode, Body: string(respBody)}
	}
	api, err := proto.DecodeEnrollAPIResponse(bytes.NewReader(respBody))
	if err != nil {
		return nil, err
	}
	return finishEnroll(c, api, keyPEM, http.StatusCreated)
}

// finishEnroll materializa el 201 (o un 409 con las 7 claves del doble):
// valida el material del alta y deja el estado 0600 en disco.
func finishEnroll(c *EnrollConfig, api proto.EnrollAPIResponse, keyPEM []byte, status int) (*EnrollOutcome, error) {
	nodeID, err := ParseUUID(api.NodeID)
	if err != nil {
		return nil, err
	}
	if !proto.ValidSlug(api.Slug) {
		return nil, fmt.Errorf("agent: slug del alta %q invalido", api.Slug)
	}
	secret, err := base64.StdEncoding.DecodeString(api.NodeSecretB)
	if err != nil || len(secret) != nodeSecretLen {
		return nil, fmt.Errorf("agent: node_secret del alta invalido (%d B)", len(secret))
	}
	out := &EnrollOutcome{
		Status:      status,
		Reenrolled:  status == http.StatusConflict,
		Resp:        api,
		NodeID:      nodeID,
		NodeSecret:  secret,
		KeyPEM:      keyPEM,
		CertPEM:     []byte(api.CertPEM),
		Fingerprint: certFingerprint(api.CertPEM),
	}
	if err := persistEnrollState(c, out); err != nil {
		return nil, err
	}
	return out, nil
}

// persistEnrollState deja el estado 0600: secret.bin de 32 B, clave
// propia, cert, CA, token y node.json. Un 409 re-emite certificado y
// secreto y pisa el estado, sin tocar node.json de la fila original
// mas que para actualizar fingerprint (T-0308 AC1).
func persistEnrollState(c *EnrollConfig, out *EnrollOutcome) error {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return err
	}
	if err := SaveSecret(c.StateDir, out.NodeSecret); err != nil {
		return err
	}
	if err := SaveRotation(c.StateDir, StoredSecret{
		Secret: out.NodeSecret, IssuedAt: time.Now().UTC(), ExpiresAt: certExpiry(c, out),
	}, nil); err != nil {
		return err
	}
	if err := WritePrivateFile(filepath.Join(c.StateDir, "key.pem"), out.KeyPEM); err != nil {
		return err
	}
	if err := WritePrivateFile(filepath.Join(c.StateDir, "cert.pem"), out.CertPEM); err != nil {
		return err
	}
	if err := WritePrivateFile(filepath.Join(c.StateDir, "ca.pem"), []byte(out.Resp.CAPEM)); err != nil {
		return err
	}
	if err := WritePrivateFile(filepath.Join(c.StateDir, "device_token"), []byte(c.DeviceToken)); err != nil {
		return err
	}
	return SaveNodeIdentity(c.NodeDir, NodeIdentity{
		NodeID:          out.Resp.NodeID,
		Slug:            out.Resp.Slug,
		Arch:            c.Arch,
		Rendezvous:      out.Resp.Rendezvous,
		TTLS:            out.Resp.TTLS,
		AgentVer:        c.AgentVer,
		CertFingerprint: out.Fingerprint,
		DeviceTokenHash: proto.HashDeviceToken(c.DeviceToken),
		EnrolledAt:      time.Now().UTC(),
	})
}

// certExpiry fija expires_at del secreto: rota con el certificado a 90
// dias (§6); ttl_s del 201 manda si es menor.
func certExpiry(c *EnrollConfig, out *EnrollOutcome) time.Time {
	exp := time.Now().UTC().Add(90 * 24 * time.Hour)
	if out.Resp.TTLS > 0 {
		short := time.Now().UTC().Add(time.Duration(out.Resp.TTLS) * time.Second)
		if short.Before(exp) {
			exp = short
		}
	}
	return exp
}

// ReadTokenOrEnv lee el device_token del estado (lo que dejo el probe
// de EP-02) o del entorno BOTNET_DEVICE_TOKEN.
func ReadTokenOrEnv(stateDir string) string {
	if b, err := os.ReadFile(filepath.Join(stateDir, "device_token")); err == nil && len(b) > 0 {
		return string(b)
	}
	return os.Getenv("BOTNET_DEVICE_TOKEN")
}

// ReadTicket lee el ticket que dejo el probe en state/ticket; vacio =
// sin ticket (el backend responde 428 si el nodo no esta dado de alta).
func ReadTicket(stateDir string) string {
	b, err := os.ReadFile(filepath.Join(stateDir, "ticket"))
	if err != nil {
		return ""
	}
	return string(b)
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
