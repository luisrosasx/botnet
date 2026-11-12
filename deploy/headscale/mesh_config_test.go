// mesh_config_test.go — EP-07 T-0701 AC1/AC3 y T-0702 AC1/AC2 en el
// spec, no en la maquina: el YAML fija el CGNAT 100.64/10 y apaga
// listeners externos; el ACL hujson solo acepta SSH bastion→nodo:22 y
// no tiene ninguna regla de entrada para el rango de clientes (SSH de
// un cliente = timeout). headscale/iperf3/tcpdump no viven en este
// host: la malla real se ancla en el bastion (runbook mesh.md).
package headscale

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func readHere(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatalf("leer %s: %v", name, err)
	}
	return string(b)
}

func TestConfigCGNATYSinListenerExterno(t *testing.T) {
	raw := readHere(t, "config.yaml")
	var cfg map[string]any
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("config.yaml no parsea: %v", err)
	}

	// AC1: direccion 100.64.x.y del rango CGNAT para cada nodo tun.
	prefixes, _ := cfg["prefixes"].(map[string]any)
	if got, _ := prefixes["v4"].(string); got != "100.64.0.0/10" {
		t.Errorf("prefixes.v4 = %v, want 100.64.0.0/10 (CGNAT)", prefixes["v4"])
	}

	// T-0702 AC2: ni el API ni el metrics del bastion escuchan en
	// 0.0.0.0 hacia fuera: el acceso entra por el tunel WG.
	for _, k := range []string{"listen_addr", "metrics_listen_addr"} {
		v, _ := cfg[k].(string)
		if !strings.HasPrefix(v, "127.0.0.1") {
			t.Errorf("%s = %q: el bastion abriria un listener externo", k, v)
		}
	}

	// IP estable por nodo: el runbook de SSH la usa como destino.
	if port, _ := cfg["randomize_client_port"].(bool); port {
		t.Error("randomize_client_port: true rompe el SSH estable por IP de malla")
	}

	// La politica vive junto al config y existe.
	pp, _ := cfg["policy_path"].(string)
	if pp == "" {
		t.Fatal("policy_path ausente en config.yaml")
	}
	if _, err := os.Stat(strings.TrimPrefix(pp, "./")); err != nil {
		t.Errorf("policy_path %q inexistente: %v", pp, err)
	}

	// T-0705 AC3: ningun flag del config enruta dato por WG. El token
	// se construye por concatenacion: el guard de scripts/mesh no debe
	// contar ni al propio guard (grep sobre deploy/ es literal).
	if strings.Contains(raw, "data_"+"plane=wg") {
		t.Error("config.yaml trae el flag de plano WG: el dato volvio a la malla")
	}
}

type aclRule struct {
	Action string   `json:"action"`
	Proto  string   `json:"proto"`
	Src    []string `json:"src"`
	Dst    []string `json:"dst"`
}

func TestACLPoliticaSoloSSHDeOperaciones(t *testing.T) {
	raw := readHere(t, "aclpolicy.hujson")
	var pol struct {
		TagOwners map[string][]string `json:"tagOwners"`
		Groups    map[string][]string `json:"groups"`
		ACLs      []aclRule           `json:"acls"`
	}
	if err := json.Unmarshal([]byte(stripHujson(stripHujson(raw))), &pol); err != nil {
		t.Fatalf("aclpolicy.hujson no parsea como JSON post-comentarios: %v", err)
	}

	// AC1 T-0702 + AC3 T-0701: la unica regla aceptada es SSH
	// bastion→nodo:22 (proto tcp).
	if len(pol.ACLs) != 1 {
		t.Fatalf("hay %d reglas ACL; la malla de operaciones solo admite una", len(pol.ACLs))
	}
	r := pol.ACLs[0]
	if r.Action != "accept" || r.Proto != "tcp" {
		t.Errorf("regla %s/%s: want accept/tcp", r.Action, r.Proto)
	}
	if len(r.Src) != 1 || r.Src[0] != "tag:ops-bastion" {
		t.Errorf("src = %v, want [tag:ops-bastion] (el bastion es la unica puerta)", r.Src)
	}
	if len(r.Dst) != 1 || r.Dst[0] != "tag:node:22" {
		t.Errorf("dst = %v, want [tag:node:22]", r.Dst)
	}

	// AC3 T-0701: el rango de clientes no enruta a la malla — ninguna
	// regla tiene src de cliente; SSH desde un cliente es timeout.
	for _, rule := range pol.ACLs {
		for _, s := range rule.Src {
			if strings.Contains(s, "client") {
				t.Errorf("regla con src %q: el rango cliente enrruta hacia la malla", s)
			}
		}
	}

	// tag:node existe (los nodos con tun lo reciben; clase C no).
	if _, ok := pol.TagOwners["tag:node"]; !ok {
		t.Error("tagOwners sin tag:node: los nodos de la malla no tienen etiqueta")
	}
}

// stripHujson: quita comentarios // y /* */ de una pasada. Dos pasadas
// por los anidados del fichero; basta para el ACL de este deploy (el
// parser de referencia es el hujson de headscale en el bastion).
func stripHujson(s string) string {
	var b strings.Builder
	inStr, line, block := false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if line && c == '\n' {
			line = false
			b.WriteByte(c)
		} else if line {
			continue
		} else if block {
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				block = false
				i++
			}
		} else if c == '"' {
			inStr = !inStr
			b.WriteByte(c)
		} else if !inStr && c == '/' && i+1 < len(s) {
			switch s[i+1] {
			case '/':
				line = true
				i++
			case '*':
				block = true
				i++
			default:
				b.WriteByte(c)
			}
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}
