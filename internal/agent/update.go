// Package agent concentra el ciclo del agente enrolado (BLUEPRINT §4):
// enrollado con secreto por nodo, entitlements, heartbeat y baja por pull.
// EP-14 aporta aquí el suministro de releases: manifiesto ed25519 con raíz
// fijada en build [injerto: P2], self-update con commit a 5 min, rollback
// server-side por crash-loop y slots A/B solo en clase A.
package agent

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExitNeedRestart es el código con el que self-update sale del proceso: el
// watchdog (reexec, EP-05) lo interpreta como "relanza el binario recién
// renombrado" (BLUEPRINT §4: descarga a botd.new, rename, exit 75).
const ExitNeedRestart = 75

// CrashLoopWindow y CrashLoopThreshold: tres salidas 75 en 15 min definen
// el crash-loop; el backend fija nodes.stable_version a la versión anterior
// y el agente vuelve atrás (rollback server-side, A2-05).
const (
	CrashLoopWindow    = 15 * time.Minute
	CrashLoopThreshold = 3
)

// CommitAfter: los 5 min "sanos" tras los cuales el agente fija la versión
// nueva como estable (stable_version local + nodes.stable_version).
const CommitAfter = 5 * time.Minute

// RootPubKeyBase64 es la raíz ed25519 fijada en build con
// -ldflags "-X .../internal/agent.RootPubKeyBase64=$(base64 -w0 root_ed25519.pub)".
// botd --print-root imprime este valor; vacío significa que el binario no
// tiene raíz y cualquier manifiesto debe rechazarse antes de ejecutar.
var RootPubKeyBase64 string

// ErrRejectedSig: el manifiesto no verificó contra la raíz fijada. El log
// del agente usa exactamente el token update=rejected_sig: un MITM ya no
// sirve el binario que él mismo hasheó (injerto 11).
var ErrRejectedSig = errors.New("update=rejected_sig")

// ErrNoRootKey: no hay raíz compilada; se trata igual que un sig inválido
// (rechazar antes de ejecutar, nunca confiar en la ruta descargada).
var ErrNoRootKey = errors.New("update=no_root_pinned")

// Entry es la entrada por arquitectura del manifest.json de una release:
// {version, sha256, url, sig} (criterio T-1403.1) más name/class para el
// renaming de T-1305: `botctl status | grep name` debe coincidir con la
// entrada del manifest por clase (aserción cruzada; sha256 solo no basta).
type Entry struct {
	Arch    string `json:"arch"`
	Name    string `json:"name"`
	Class   string `json:"class"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	URL     string `json:"url"`
	Sig     string `json:"sig"` // base64 de la firma ed25519 sobre SigPayload
}

// Manifest es el manifest.json firmado de la release. Root permite al
// verificador detectar la raíz anunciada; la raíz autoritativa sigue siendo
// la compilada (RootPubKeyBase64), nunca el campo del manifiesto.
type Manifest struct {
	Version string  `json:"version"`
	Root    string  `json:"root"`
	Entries []Entry `json:"entries"`
}

// archMatrix es el conjunto de la matriz F1 (build_matrix.sh): NewManifest
// recorre estos tags y omite los que no existan en dist/.
var archMatrix = []string{"amd64", "arm64", "armv5", "armv7", "mipsbe", "mipsle"}

// ClassForArch mapea tag de la matriz a device_class (nodes.device_class).
func ClassForArch(arch string) string {
	switch arch {
	case "amd64", "arm64":
		return "A"
	case "armv5", "armv7":
		return "B"
	case "mipsbe", "mipsle":
		return "C"
	}
	return ""
}

// NameForClass fija el nombre canónico por clase (renaming T-1305): el
// manifest y `botctl status` deben mostrar el mismo name por clase.
func NameForClass(class string) string { return "botd" + strings.ToLower(class) }

// SigPayload es el canón sobre el que se firma: version global + campos de
// la entrada, separados por "|". Añadir o reordenar campos es cambio de
// contrato de release, no cosmético.
func (e *Entry) SigPayload() []byte {
	h := sha256.Sum256([]byte(strings.Join([]string{
		"v1", e.Arch, e.Name, e.Class, e.Version, e.SHA256, e.URL,
	}, "|")))
	return h[:]
}

// RootFromBase64 decodifica una pubkey raíz de 32 B.
func RootFromBase64(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("agent: root b64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("agent: root %d B != %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// PrintRoot es lo que debe emitir `botd --print-root`: la pubkey raíz
// compilada, sin saltos; byte a byte con keys/root_ed25519.pub.
func PrintRoot() string { return strings.TrimSpace(RootPubKeyBase64) }

// Sign firma cada entrada con la raíz (clave privada ed25519) y deja la
// pubkey en Manifest.Root. Solo la release builder lo usa; la clave privada
// nunca baja al nodo.
func (m *Manifest) Sign(priv ed25519.PrivateKey) error {
	if len(m.Entries) == 0 {
		return errors.New("agent: manifest vacío")
	}
	m.Root = base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey))
	for i := range m.Entries {
		sig := ed25519.Sign(priv, m.Entries[i].SigPayload())
		m.Entries[i].Sig = base64.StdEncoding.EncodeToString(sig)
	}
	return nil
}

// Verify comprueba la firma de cada entrada contra la raíz fijada. El
// primer fallo devuelve ErrRejectedSig: la verificación ocurre ANTES de
// ejecutar nada descargado (A2-05).
func (m *Manifest) Verify(root ed25519.PublicKey) error {
	if len(m.Entries) == 0 {
		return errors.New("agent: manifest vacío")
	}
	for i := range m.Entries {
		sig, err := base64.StdEncoding.DecodeString(m.Entries[i].Sig)
		if err != nil {
			return fmt.Errorf("%w: sig b64: %v", ErrRejectedSig, err)
		}
		if len(sig) != ed25519.SignatureSize ||
			!ed25519.Verify(root, m.Entries[i].SigPayload(), sig) {
			return ErrRejectedSig
		}
	}
	return nil
}

// VerifyPinned verifica contra la raíz compilada en el binario del agente.
func (m *Manifest) VerifyPinned() error {
	root, err := RootFromBase64(RootPubKeyBase64)
	if err != nil {
		return ErrNoRootKey
	}
	return m.Verify(root)
}

// SHA256File devuelve el hex (64 B) del fichero; es el sha256 que viaja en
// la entrada del manifest.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// NewManifest construye el manifest desde dist/: por cada tag de la matriz
// toma dist/botd-<tag>, calcula sha256 y arma la entrada con version, name
// por clase y url derivada de urlTmpl (placeholder "{file}"). Los binarios
// ausentes se omiten: una release parcial es válida (5 archs en F1).
func NewManifest(distDir, version, urlTmpl string) (*Manifest, error) {
	if version == "" {
		return nil, errors.New("agent: version vacía")
	}
	m := &Manifest{Version: version}
	for _, tag := range archMatrix {
		path := filepath.Join(distDir, "botd-"+tag)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		sum, err := SHA256File(path)
		if err != nil {
			return nil, err
		}
		class := ClassForArch(tag)
		m.Entries = append(m.Entries, Entry{
			Arch:    tag,
			Name:    NameForClass(class),
			Class:   class,
			Version: version,
			SHA256:  sum,
			URL:     strings.ReplaceAll(urlTmpl, "{file}", "botd-"+tag),
		})
	}
	if len(m.Entries) == 0 {
		return nil, fmt.Errorf("agent: dist %s sin binarios botd-*", distDir)
	}
	return m, nil
}

// LoadManifest / SaveManifest: manifest.json sobre disco.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("agent: manifest %s: %w", path, err)
	}
	return &m, nil
}

func SaveManifest(path string, m *Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// FetchFunc baja el binario de una URL. En tests es un http.Client real
// contra httptest; en producción, TLS con proxy-aware dialer (EP-04).
type FetchFunc func(url string) ([]byte, error)

// Updater ejecuta el flujo de self-update sobre el directorio del nodo
// (/data/botd). Class fija la política de slots: A/B solo en clase A;
// slot único en B/C para no tocar el presupuesto de flash (T-0204).
type Updater struct {
	Dir   string
	Class string // A|B|C
	Clock func() time.Time
}

func NewUpdater(dir, class string) *Updater {
	if class != "A" && class != "B" && class != "C" {
		return nil
	}
	return &Updater{Dir: dir, Class: class, Clock: time.Now}
}

// Stage baja el binario de la entrada (verificando sha256 y la firma del
// manifest contra la raíz), lo escribe en botd.new y hace rename atómico:
// en clase A al slot pasivo botd.A/botd.B (pointer file `slot`), en B/C
// sobre el propio botd. Devuelve la ruta activa tras el rename. Un sig
// inválido aborta ANTES de escribir en flash.
func (u *Updater) Stage(m *Manifest, e *Entry, fetch FetchFunc) (string, error) {
	if e == nil || m == nil {
		return "", errors.New("agent: stage: entry/manifest nil")
	}
	if err := m.VerifyPinned(); err != nil {
		return "", err // update=rejected_sig sale aquí, sin tocar botd.new
	}
	body, err := fetch(e.URL)
	if err != nil {
		return "", fmt.Errorf("agent: fetch %s: %w", e.URL, err)
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != e.SHA256 {
		return "", fmt.Errorf("agent: sha256 mismatch en %s", e.Arch)
	}
	tmp := filepath.Join(u.Dir, "botd.new")
	if err := os.WriteFile(tmp, body, 0o755); err != nil {
		return "", err
	}
	if u.Class == "A" {
		return u.stageAB(tmp)
	}
	active := filepath.Join(u.Dir, "botd")
	if err := os.Rename(tmp, active); err != nil { // rename(2) atómico mismo fs
		return "", err
	}
	return active, nil
}

// stageAB rota los slots A/B de clase A: escribe en el slot pasivo y mueve
// el pointer. El binario activo vieja queda como fallback pinado hasta el
// commit; tras él, el slot pasivo queda disponible para la siguiente release.
func (u *Updater) stageAB(tmp string) (string, error) {
	cur, err := os.ReadFile(filepath.Join(u.Dir, "slot"))
	if err != nil {
		cur = []byte("A")
	}
	next := "B"
	if strings.TrimSpace(string(cur)) == "B" {
		next = "A"
	}
	slot := filepath.Join(u.Dir, "botd"+next)
	if err := os.Rename(tmp, slot); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(u.Dir, "slot"), []byte(next+"\n"), 0o644); err != nil {
		return "", err
	}
	return slot, nil
}

// Commit fija la versión como estable local (clave para el rollback: si la
// nueva vuelve a crashear, el agente pide la stable_version). Llamado tras
// CommitAfter de latidos sanos.
func (u *Updater) Commit(version string) error {
	return os.WriteFile(filepath.Join(u.Dir, "stable_version"),
		[]byte(version+"\n"), 0o644)
}

// StableVersion devuelve la stable_version local (vacía si no hay).
func (u *Updater) StableVersion() string {
	raw, err := os.ReadFile(filepath.Join(u.Dir, "stable_version"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// SlotCount es el `ls /data/botd | grep -c 'botd.[AB]'` del criterio 3:
// 2 en clase A tras una actualización (slot A/B vivo + pasivo), 0 en B/C.
func (u *Updater) SlotCount() int {
	n := 0
	for _, name := range []string{"botdA", "botdB"} {
		if _, err := os.Stat(filepath.Join(u.Dir, name)); err == nil {
			n++
		}
	}
	return n
}

// CrashTracker cuenta salidas 75 para el rollback server-side. ShouldRollback
// es true cuando hay CrashLoopThreshold salidas dentro de CrashLoopWindow.
type CrashTracker struct {
	Window    time.Duration
	Threshold int
	exits     []time.Time
}

func NewCrashTracker() *CrashTracker {
	return &CrashTracker{Window: CrashLoopWindow, Threshold: CrashLoopThreshold}
}

// RecordExit anota una salida 75 (la siguiente arrancada del watchdog).
func (c *CrashTracker) RecordExit(t time.Time) {
	c.exits = append(c.exits, t)
}

// ShouldRollback evalúa la ventana en t: 3+ salidas 75 en Window => el
// backend debe fijar nodes.stable_version a la versión anterior (criterio 2).
func (c *CrashTracker) ShouldRollback(t time.Time) bool {
	if len(c.exits) == 0 {
		return false
	}
	n := 0
	for _, e := range c.exits {
		if t.Sub(e) <= c.Window {
			n++
		}
	}
	return n >= c.Threshold
}

// PlanRollback decide la versión final: si el backend ya fijó una
// stable_version anterior a la actual, esa es la versión a la que el agente
// vuelve (el test assertions la versión final tras el ciclo acelerado).
func PlanRollback(current, stable string) string {
	if stable == "" || stable == current {
		return current
	}
	return stable
}
