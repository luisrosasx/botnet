package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/botnet/botnet/internal/agent"
)

// Claves de release bajo control del repo: la pub/ic se commitea; la clave
// privada vive en la build box y jamas sube al nodo ni viaja al backend.
const (
	pubKeyFile  = "root_ed25519.pub"
	privKeyFile = "root_ed25519.key"
)

// cmdGenroot crea el par raiz ed25519 de la release (idempotente: si la
// privada existe, se niega a pisarla: perder la raiz invalida releases pasadas).
func cmdGenroot(keydir string) error {
	if err := os.MkdirAll(keydir, 0o755); err != nil {
		return err
	}
	privPath := filepath.Join(keydir, privKeyFile)
	if _, err := os.Stat(privPath); err == nil {
		return fmt.Errorf("genroot: %s existe; no se pisa la raiz", privPath)
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(filepath.Join(keydir, pubKeyFile),
		[]byte(pubB64+"\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(privPath,
		[]byte(hex.EncodeToString(seed)+"\n"), 0o600)
}

func loadRootKey(keydir string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(filepath.Join(keydir, privKeyFile))
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("clave raiz %s: %w", keydir, err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("clave raiz: tamano de seed invalido")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func loadRootPub(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return agent.RootFromBase64(string(raw))
}

// cmdSign: dist/ + version -> manifest.json firmado entrada a entrada con la
// raiz (T-1403.1). El urlTmpl admite {version} y {file}.
func cmdSign(distDir, keydir, out, urlTmpl, version string) error {
	tmpl := strings.ReplaceAll(urlTmpl, "{version}", version)
	m, err := agent.NewManifest(distDir, version, tmpl)
	if err != nil {
		return err
	}
	priv, err := loadRootKey(keydir)
	if err != nil {
		return err
	}
	if err := m.Sign(priv); err != nil {
		return err
	}
	if err := agent.SaveManifest(out, m); err != nil {
		return err
	}
	fmt.Printf("sign: %s version=%s entradas=%d -> %s\n", distDir, version, len(m.Entries), out)
	return nil
}

// cmdVerify verifica firmas contra la pub raiz fijada y, si distDir se
// proporciona, que el sha256 de cada binario coincida con su entrada. Con
// 1 byte alterado del sig, el caller (verify_release.sh) recibe exit != 0.
func cmdVerify(manifestPath, rootPubPath, distDir string) error {
	m, err := agent.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	root, err := loadRootPub(rootPubPath)
	if err != nil {
		return err
	}
	if m.Root != "" && m.Root != base64.StdEncoding.EncodeToString(root) {
		return fmt.Errorf("verify: raiz anunciada %s != %s (raiz fijada)", m.Root,
			base64.StdEncoding.EncodeToString(root))
	}
	if err := m.Verify(root); err != nil {
		return err
	}
	if distDir != "" {
		for _, e := range m.Entries {
			sum, err := agent.SHA256File(filepath.Join(distDir, "botd-"+e.Arch))
			if err != nil {
				return fmt.Errorf("verify %s: %w", e.Arch, err)
			}
			if sum != e.SHA256 {
				return fmt.Errorf("verify %s: sha256 %s != %s", e.Arch, sum, e.SHA256)
			}
		}
	}
	fmt.Printf("verify: OK %d entradas version=%s\n", len(m.Entries), m.Version)
	return nil
}

// enrollResp replica las claves del contrato del 201 (BLUEPRINT §4) que el
// stub de QEMU debe responder; smokeDouble las exige presentes.
type enrollResp struct {
	NodeID     string   `json:"node_id"`
	Slug       string   `json:"slug"`
	CertPEM    string   `json:"cert_pem"`
	CAPEM      string   `json:"ca_pem"`
	NodeSecret string   `json:"node_secret"`
	Rendezvous []string `json:"rendezvous"`
	TTLS       int      `json:"ttl_s"`
}

// cmdSmokeDouble es el doble nativo del humo QEMU (T-1402 sin QEMU en host):
// enroll + heartbeat reales contra scripts/qemu/stub_api.go imprimiendo las
// lineas que smoke.sh aserta (ENROLL 201 / HEARTBEAT 200 ok).
func cmdSmokeDouble(stubURL string) error {
	stub := strings.TrimRight(stubURL, "/")
	body := []byte(`{"arch":"` + goHostArch() + `","model":"smoke","firmware":"n/a","mac":"02:00:00:00:00:00"}`)
	req, err := http.NewRequest(http.MethodPost, stub+"/v1/enroll", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Token", "smoke-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("smoke: enroll status %d, want 201", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var er enrollResp
	if err := json.Unmarshal(raw, &er); err != nil {
		return err
	}
	switch {
	case er.NodeID == "" || er.Slug == "" || er.NodeSecret == "":
		return errors.New("smoke: 201 sin node_id/slug/node_secret (contrato roto)")
	case len(er.Rendezvous) == 0 || er.TTLS <= 0:
		return errors.New("smoke: 201 sin rendezvous/ttl_s (contrato roto)")
	}
	fmt.Printf("ENROLL 201 node_id=%s slug=%s\n", er.NodeID, er.Slug)

	hb := []byte(fmt.Sprintf(`{"node_id":%q,"rss_kib":1024,"streams":0,"ver":%q,"bytes_delta":0}`, er.NodeID, version))
	hbReq, err := http.NewRequest(http.MethodPost, stub+"/v1/heartbeat", bytes.NewReader(hb))
	if err != nil {
		return err
	}
	hbReq.Header.Set("Content-Type", "application/json")
	hbResp, err := http.DefaultClient.Do(hbReq)
	if err != nil {
		return err
	}
	defer hbResp.Body.Close()
	hbBody, _ := io.ReadAll(hbResp.Body)
	if hbResp.StatusCode != http.StatusOK || string(hbBody) != "ok" {
		return fmt.Errorf("smoke: heartbeat %d %q, want 200 ok", hbResp.StatusCode, hbBody)
	}
	fmt.Printf("HEARTBEAT 200 ok\n")
	return nil
}

// goHostArch reporta el tag de la matriz para el body del enroll del doble
// (en el host nativo es amd64/arm64; los tags cross los fija el binario
// compilado, no este programa).
func goHostArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	default:
		return "amd64"
	}
}
