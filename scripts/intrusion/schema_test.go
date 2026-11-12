package intrusion_test

import (
	"strings"
	"testing"

	"github.com/botnet/botnet/internal/intrusion"
)

// TestCVEModelSchema es el criterio 2 de T-0203: ≥20 entradas y cada una con
// model, cve, arch y device_class dentro de dominio. LoadCVEModels ya aplica
// el esquema estricto (campos obligatorios, enums, unicidad); aquí se fija el
// umbral de cantidad y que la copia versionada es la que se valida.
func TestCVEModelSchema(t *testing.T) {
	entries, err := intrusion.LoadCVEModels("cve-models.yaml")
	if err != nil {
		t.Fatalf("cve-models.yaml no valida: %v", err)
	}
	if len(entries) < 20 {
		t.Fatalf("se exigían ≥20 entradas, hay %d", len(entries))
	}
	archSeen := map[string]int{}
	for i, e := range entries {
		if !oneOf(e.Arch, intrusion.Archs) {
			t.Errorf("entrada %d (%s): arch %q fuera de dominio", i, e.Model, e.Arch)
		}
		if !oneOf(e.DeviceClass, intrusion.DeviceClasses) {
			t.Errorf("entrada %d (%s): device_class %q fuera de dominio", i, e.Model, e.DeviceClass)
		}
		if _, ok := intrusion.LookupCVE(entries, e.Model); !ok {
			t.Errorf("entrada %d (%s): lookup falló", i, e.Model)
		}
		archSeen[e.Arch]++
	}
	if len(archSeen) < 4 {
		t.Errorf("la matriz solo cubre %d archs; la entrega necesita variedad", len(archSeen))
	}
}

// TestCVEModelSchemaRejectsX86 es el criterio 3 de T-0203: una copia de
// prueba con arch: x86 (fuera de dominio) debe fallar el assert de enum.
// Aquí se comprueba como expectativa: el validator devuelve error.
func TestCVEModelSchemaRejectsX86(t *testing.T) {
	_, err := intrusion.LoadCVEModels("../../testdata/intrusion/cve-models.badarch.yaml")
	if err == nil {
		t.Fatal("arch x86 fuera de dominio debería dar error y validó")
	}
	if !strings.Contains(err.Error(), "arch") {
		t.Fatalf("el error debería mencionar arch, es: %v", err)
	}
}

// TestTLWR841NLookup asegura el modelo exacto que usan los criterios de
// T-0202: existe y devuelve exploit_id no nulo con read_cmd "uid".
func TestTLWR841NLookup(t *testing.T) {
	entries, err := intrusion.LoadCVEModels("cve-models.yaml")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := intrusion.LookupCVE(entries, "TL-WR841N")
	if !ok {
		t.Fatal("TL-WR841N ausente del YAML")
	}
	if e.ExploitID == "" {
		t.Error("exploit_id vacío para TL-WR841N")
	}
	if e.ReadCmd != "uid" {
		t.Errorf("read_cmd = %q, quería uid", e.ReadCmd)
	}
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}
