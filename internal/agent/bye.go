package agent

import (
	"errors"
	"os"
	"path/filepath"
)

// certFileName es el certificado de cliente emitido en el alta (mismo
// nombre que escribe enroll.go).
const certFileName = "cert.pem"

// WipeKeys borra cert + node_secret del state dir (T-0409 AC3, injerto
// P3: baja por pull). Tras el flag bye el agente queda inofensivo: 0
// claves vivas y el router puede apagarse despues — la baja no depende
// de un POST saliente. Un fichero ausente no es error (idempotente).
func WipeKeys(stateDir string) error {
	if stateDir == "" {
		return nil
	}
	var first error
	for _, name := range []string{certFileName, SecretFileName, rotationName} {
		if err := os.Remove(filepath.Join(stateDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) && first == nil {
			first = err
		}
	}
	return first
}
