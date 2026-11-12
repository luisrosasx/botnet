package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/botnet/botnet/pkg/proto"
)

// Nombres fijos del estado plano (§3): node.json manda el modo de
// arranque; todo lo demas vive bajo <nodeDir>/state con 0600.
const (
	NodeJSONName   = "node.json"
	StateDirName   = "state"
	SecretFileName = "secret.bin"
)

// NodeIdentity es lo que node.json conserva tras un alta: identidad,
// transparencia y auditoria del certificado, nunca secretos.
type NodeIdentity struct {
	NodeID          string    `json:"node_id"`
	Slug            string    `json:"slug"`
	Arch            string    `json:"arch"`
	Rendezvous      []string  `json:"rendezvous"`
	TTLS            uint32    `json:"ttl_s"`
	AgentVer        string    `json:"agent_ver"`
	CertFingerprint string    `json:"cert_fingerprint"`
	DeviceTokenHash string    `json:"device_token_hash"`
	EnrolledAt      time.Time `json:"enrolled_at"`
}

func (id NodeIdentity) valid() error {
	if _, err := ParseUUID(id.NodeID); err != nil {
		return err
	}
	if !proto.ValidSlug(id.Slug) {
		return fmt.Errorf("agent: slug %q invalido (12 alfanumericos)", id.Slug)
	}
	return nil
}

// EnsureStateDir crea <nodeDir>/state (0700: el dueno same-uid tampoco
// debe listar nombres ajenos) y devuelve su ruta — /data/botd/state en
// produccion, donde vive secret.bin (T-0303 AC1).
func EnsureStateDir(nodeDir string) (string, error) {
	dir := filepath.Join(nodeDir, StateDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("agent: state dir: %w", err)
	}
	return dir, nil
}

// WritePrivateFile deja el fichero con 0600 exacto (T-0307 AC3:
// find -perm /077 = 0 bajo el estado).
func WritePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("agent: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("agent: write %s: %w", path, err)
	}
	// Windows ignora los bits; en Unix el 0600 de umask ya vale, Chmod
	// lo fuerza ante umask laxos del stager de EP-02.
	return os.Chmod(path, 0o600)
}

// PrivatePermOK es el equivalente a `find -perm /077 | wc -l` = 0 para
// un fichero: ni group ni other deben tener ningun bit.
func PrivatePermOK(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.Mode().Perm()&0o077 == 0, nil
}

// SaveNodeIdentity persista node.json con 0600; un node.json valido es
// la condicion de arranque en modo agente (T-0301 AC1/AC2).
func SaveNodeIdentity(nodeDir string, id NodeIdentity) error {
	if err := id.valid(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivateFile(filepath.Join(nodeDir, NodeJSONName), append(b, '\n'))
}

// LoadNodeIdentity devuelve error si falta node.json o no es valido:
// el arranque cae a modo enrolado sin distinguir ambos casos.
func LoadNodeIdentity(nodeDir string) (NodeIdentity, error) {
	var id NodeIdentity
	b, err := os.ReadFile(filepath.Join(nodeDir, NodeJSONName))
	if err != nil {
		return id, err
	}
	if err := json.Unmarshal(b, &id); err != nil {
		return id, fmt.Errorf("agent: %s: %w", NodeJSONName, err)
	}
	return id, id.valid()
}
