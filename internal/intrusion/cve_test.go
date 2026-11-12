package intrusion

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeCVEModelsStrict(t *testing.T) {
	yamlDoc := `entries:
  - model: TL-WR841N
    hw_version: v14
    cve: CVE-2022-30262
    arch: mipsle
    device_class: C
    exploit_id: x
    module: m
    read_cmd: uid
`
	entries, err := DecodeCVEModels(strings.NewReader(yamlDoc))
	if err != nil {
		t.Fatalf("validó mal un documento bueno: %v", err)
	}
	if len(entries) != 1 || entries[0].Model != "TL-WR841N" {
		t.Fatalf("entradas: %+v", entries)
	}

	if _, err := DecodeCVEModels(strings.NewReader(strings.Replace(yamlDoc, "arch: mipsle", "arch: x86", 1))); err == nil {
		t.Error("arch x86 debería fallar")
	}
	if _, err := DecodeCVEModels(strings.NewReader(strings.Replace(yamlDoc, "device_class: C", "device_class: D", 1))); err == nil {
		t.Error("device_class D debería fallar")
	}
	if _, err := DecodeCVEModels(strings.NewReader(strings.Replace(yamlDoc, "cve: CVE-2022-30262", "cve: 2022-30262", 1))); err == nil {
		t.Error("cve sin prefijo CVE- debería fallar")
	}
	if _, err := DecodeCVEModels(strings.NewReader(yamlDoc + "    clave_mISTERIOsa: 1\n")); err == nil {
		t.Error("campo desconocido debería fallar (esquema estricto)")
	}
	dup := yamlDoc + `  - model: TL-WR841N
    cve: CVE-2022-30262
    arch: mipsle
    device_class: C
    exploit_id: x
    module: m
`
	if _, err := DecodeCVEModels(strings.NewReader(dup)); err == nil {
		t.Error("(model, cve) duplicado debería fallar")
	}
	if _, err := DecodeCVEModels(strings.NewReader("entries: []")); err == nil {
		t.Error("lista vacía debería fallar")
	}
}

func TestValidateCVEModelsAllArchsAndClasses(t *testing.T) {
	for _, a := range Archs {
		for _, c := range DeviceClasses {
			doc := "entries:\n  - model: M\n    cve: CVE-2022-0001\n    arch: " + a +
				"\n    device_class: " + c + "\n    exploit_id: x\n    module: m\n"
			if _, err := DecodeCVEModels(strings.NewReader(doc)); err != nil {
				t.Errorf("combo %s/%s debería validar: %v", a, c, err)
			}
		}
	}
}

func TestLookupCVEDefaultsReadCmd(t *testing.T) {
	entries := []CVEEntry{{Model: "X", ReadCmd: ""}}
	e, ok := LookupCVE(entries, "x") // case-insensitive: el operador escribe lo que ve en el panel web
	if !ok || e.ReadCmd != "uid" {
		t.Fatalf("lookup default read_cmd: %+v ok=%v", e, ok)
	}
	if _, ok := LookupCVE(entries, "Y"); ok {
		t.Error("modelo inexistente debería dar !ok")
	}
}

func TestLoadCVEModelsPath(t *testing.T) {
	if _, err := LoadCVEModels(filepath.Join("testdata", "no-existe.yaml")); err == nil {
		t.Error("fichero inexistente debería dar error")
	}
}
