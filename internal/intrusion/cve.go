package intrusion

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Archs válidos: los seis objetivos de la matriz de entrega (BLUEPRINT §4).
// Un arch fuera de dominio invalida la entrada entera: el binario mal
// compilado pierde la familia (BRIEF-BASE restricción 3).
var Archs = []string{"mipsbe", "mipsle", "armv5", "armv7", "arm64", "amd64"}

// DeviceClasses: A (≥128 MB), B (64-128 MB), C (32 MB / 8 MB flash) — BLUEPRINT §2.
var DeviceClasses = []string{"A", "B", "C"}

// CVEEntry es una fila del YAML de CVEs por modelo: el CVE que abre la puerta,
// la arquitectura a la que se entrega y la clase de hardware del objetivo.
type CVEEntry struct {
	Model       string `yaml:"model"`
	HWVersion   string `yaml:"hw_version"`
	CVE         string `yaml:"cve"`
	Arch        string `yaml:"arch"`
	DeviceClass string `yaml:"device_class"`
	ExploitID   string `yaml:"exploit_id"`
	Module      string `yaml:"module"`
	ReadCmd     string `yaml:"read_cmd"`
}

// cveDoc es la forma del documento: lista bajo la clave "entries".
type cveDoc struct {
	Entries []CVEEntry `yaml:"entries"`
}

// LoadCVEModels lee y valida el YAML de CVEs. Un campo desconocido es error:
// el esquema es contrato y el CI debe gritar antes que el operador.
func LoadCVEModels(path string) ([]CVEEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cve yaml: %w", err)
	}
	defer f.Close()
	return DecodeCVEModels(f)
}

// DecodeCVEModels valida el flujo YAML desde r. Strict: sin campos sueltos.
func DecodeCVEModels(r io.Reader) ([]CVEEntry, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, fmt.Errorf("cve yaml: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(buf.Bytes()))
	dec.KnownFields(true)
	var doc cveDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("cve yaml: %w", err)
	}
	if err := ValidateCVEModels(doc.Entries); err != nil {
		return nil, err
	}
	return doc.Entries, nil
}

// ValidateCVEModels comprueba el esquema de dominio: obligatorios, enums y
// unicidad (model, cve). Devuelve el primer error con índice de entrada.
func ValidateCVEModels(entries []CVEEntry) error {
	if len(entries) == 0 {
		return fmt.Errorf("cve yaml: sin entradas")
	}
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		for _, req := range []struct{ key, val string }{
			{"model", e.Model}, {"cve", e.CVE}, {"arch", e.Arch},
			{"device_class", e.DeviceClass}, {"exploit_id", e.ExploitID},
			{"module", e.Module},
		} {
			if strings.TrimSpace(req.val) == "" {
				return fmt.Errorf("cve yaml: entrada %d (%s): campo %q vacío", i, e.Model, req.key)
			}
		}
		if !oneOf(e.Arch, Archs) {
			return fmt.Errorf("cve yaml: entrada %d (%s): arch %q fuera de dominio %v", i, e.Model, e.Arch, Archs)
		}
		if !oneOf(e.DeviceClass, DeviceClasses) {
			return fmt.Errorf("cve yaml: entrada %d (%s): device_class %q fuera de dominio %v", i, e.Model, e.DeviceClass, DeviceClasses)
		}
		if !strings.HasPrefix(e.CVE, "CVE-") {
			return fmt.Errorf("cve yaml: entrada %d (%s): cve %q sin prefijo CVE-", i, e.Model, e.CVE)
		}
		key := e.Model + "\x00" + e.CVE
		if seen[key] {
			return fmt.Errorf("cve yaml: entrada %d (%s): duplicado (model, cve)", i, e.Model)
		}
		seen[key] = true
	}
	return nil
}

// LookupCVE busca la primera entrada para un modelo. read_cmd por defecto es
// "uid": la prueba barata de canal de explotación (T-0202 criterio 3).
func LookupCVE(entries []CVEEntry, model string) (CVEEntry, bool) {
	for _, e := range entries {
		if strings.EqualFold(e.Model, model) {
			if e.ReadCmd == "" {
				e.ReadCmd = "uid"
			}
			return e, true
		}
	}
	return CVEEntry{}, false
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}
