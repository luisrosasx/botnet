package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// nodeSecretLen es el tamano del node_secret (A3-06): 32 B emitidos una
// sola vez en el 201 del alta y guardados en su propia columna.
const nodeSecretLen = 32

// StoredSecret es un secreto con su ventana de rotacion: el viejo firma
// hasta ExpiresAt (doble ventana, T-0303 AC2).
type StoredSecret struct {
	Secret    []byte    `json:"secret"` // JSON lo base64-codea
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SaveSecret deja secret.bin de exactamente 32 B con 0600 (T-0303 AC1:
// stat -c %a = 600 y wc -c = 32).
func SaveSecret(stateDir string, secret []byte) error {
	if len(secret) != nodeSecretLen {
		return fmt.Errorf("agent: node_secret %d B != %d", len(secret), nodeSecretLen)
	}
	return WritePrivateFile(filepath.Join(stateDir, SecretFileName), secret)
}

// LoadSecret lee secret.bin y rechaza longitudes torcidas.
func LoadSecret(stateDir string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, SecretFileName))
	if err != nil {
		return nil, err
	}
	if len(b) != nodeSecretLen {
		return nil, fmt.Errorf("agent: %s: %d B != %d", SecretFileName, len(b), nodeSecretLen)
	}
	return b, nil
}

// rotationFile persiste la ventana de rotacion junto a secret.bin.
type rotationFile struct {
	Current  *StoredSecret `json:"current"`
	Previous *StoredSecret `json:"previous"`
}

const rotationName = "rotation.json"

// SaveRotation guarda la ventana current/previous y sincroniza
// secret.bin con el current.
func SaveRotation(stateDir string, current StoredSecret, previous *StoredSecret) error {
	if len(current.Secret) != nodeSecretLen {
		return fmt.Errorf("agent: secret current %d B != %d", len(current.Secret), nodeSecretLen)
	}
	rot := rotationFile{Current: &current, Previous: previous}
	b, err := json.Marshal(rot)
	if err != nil {
		return err
	}
	if err := WritePrivateFile(filepath.Join(stateDir, rotationName), b); err != nil {
		return err
	}
	return SaveSecret(stateDir, current.Secret)
}

// LoadRotation lee la ventana; previous puede ser nil (primer alta).
func LoadRotation(stateDir string) (StoredSecret, *StoredSecret, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, rotationName))
	if err != nil {
		return StoredSecret{}, nil, err
	}
	var rot rotationFile
	if err := json.Unmarshal(b, &rot); err != nil {
		return StoredSecret{}, nil, fmt.Errorf("agent: %s: %w", rotationName, err)
	}
	if rot.Current == nil {
		return StoredSecret{}, nil, fmt.Errorf("agent: %s sin current", rotationName)
	}
	return *rot.Current, rot.Previous, nil
}

// SessionFields son las cuatro piezas del bot-session (§7):
// HMAC(node_secret, {client_id, tenant_id, session_id, node_id}).
type SessionFields struct {
	ClientID  string
	TenantID  string
	SessionID string
	NodeID    string
}

func (f SessionFields) canonical() string {
	return strings.Join([]string{f.ClientID, f.TenantID, f.SessionID, f.NodeID}, "\x1f")
}

func sessionMAC(secret []byte, canonical string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

// SignSession produce el bot-session: <canonical-b64>.<hmac_hex>. El
// TTL de 30 s lo aplica el gateway (A2-14: auth sin RTT a Redis); aqui
// solo importa que el HMAC sea por nodo (A3-03).
func SignSession(secret []byte, f SessionFields) string {
	can := f.canonical()
	b64 := base64.RawStdEncoding.EncodeToString([]byte(can))
	return b64 + "." + hex.EncodeToString(sessionMAC(secret, can))
}

// VerifySession compara en tiempo constante; el token firmado con el
// secreto del nodo A no valida con el secreto del nodo B (T-0303 AC3).
func VerifySession(secret []byte, token string) bool {
	i := strings.LastIndex(token, ".")
	if i <= 0 {
		return false
	}
	raw, err := base64.RawStdEncoding.DecodeString(token[:i])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(token[i+1:])
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(want, sessionMAC(secret, string(raw))) == 1
}

// VerifySessionRotated es la doble ventana de la rotacion de 90 dias
// (T-0303 AC2): el nuevo firma desde Rotate; el viejo firma solo hasta
// su expires_at, despues del todo revocado.
func VerifySessionRotated(current StoredSecret, previous *StoredSecret, token string, now time.Time) bool {
	if VerifySession(current.Secret, token) {
		return true
	}
	if previous != nil && now.Before(previous.ExpiresAt) && VerifySession(previous.Secret, token) {
		return true
	}
	return false
}
