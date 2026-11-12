package agent

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/botnet/botnet/pkg/proto"
)

// enrollStub es el doble del backend POST /v1/enroll para el
// ceremonial (el handler real vive en cmd/botnet-api, tierra de
// EP-08). Orden segun T-0302/T-0305/T-0308: token conocido → 409 de
// re-emision idempotente (el ticket no hace falta tras reset de
// fabrica); token nuevo: sin ticket 428, HMAC erróneo 403, ticket ya
// usado 409, fresh 201. Los indices unicos (device_token_hash,
// ticket) se simulan con mapas bajo un solo mutex.
type enrollStub struct {
	mu             sync.Mutex
	probeSecret    []byte
	rowsByToken    map[string]string
	usedTickets    map[string]string
	issued         int
	rowCount       int
	capturedCSR    []string
	capturedSecret []string
	lastRespKeys   int
}

func newEnrollStub(t *testing.T) (*enrollStub, *httptest.Server) {
	t.Helper()
	s := &enrollStub{
		probeSecret: []byte("probe-secret-de-flota"),
		rowsByToken: map[string]string{},
		usedTickets: map[string]string{},
	}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	return s, srv
}

// slugFor deriva un slug de 12 digitos del node_id: estable entre
// re-enrolls del mismo nodo, como haria el backend (nodes.slug unico).
func slugFor(nodeID string) string {
	return strings.ReplaceAll(nodeID, "-", "")[:12]
}

func (s *enrollStub) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/enroll" {
		http.NotFound(w, r)
		return
	}
	var req proto.EnrollAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "body", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capturedCSR = append(s.capturedCSR, req.CSRPEM)
	if strings.TrimSpace(req.CSRPEM) == "" || req.Arch == "" {
		http.Error(w, "csr/arch requeridos", http.StatusBadRequest)
		return
	}
	token := r.Header.Get("X-Device-Token")
	if token == "" {
		http.Error(w, "X-Device-Token requerido", http.StatusBadRequest)
		return
	}
	respond := func(status int, nodeID string) {
		s.issued++
		secret := make([]byte, 32)
		secret[0], secret[31] = byte(s.issued), byte(s.issued*7)
		b64 := base64.StdEncoding.EncodeToString(secret)
		s.capturedSecret = append(s.capturedSecret, b64)
		body := map[string]any{
			"node_id":     nodeID,
			"slug":        slugFor(nodeID),
			"cert_pem":    fmt.Sprintf("CERT-%d", s.issued),
			"ca_pem":      "CA-PEM",
			"node_secret": b64,
			"rendezvous":  []string{"rdv1.test:443"},
			"ttl_s":       86400,
		}
		s.lastRespKeys = len(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	if node, ok := s.rowsByToken[token]; ok {
		respond(http.StatusConflict, node)
		return
	}
	ticket := r.Header.Get("X-Intrusion-Ticket")
	if ticket == "" {
		http.Error(w, "X-Intrusion-Ticket requerido", 428)
		return
	}
	if _, err := proto.VerifyTicket(s.probeSecret, ticket); err != nil {
		http.Error(w, "ticket forjado", http.StatusForbidden)
		return
	}
	if winner, ok := s.usedTickets[ticket]; ok {
		respond(http.StatusConflict, winner)
		return
	}
	node := fmt.Sprintf("%08x-0000-4000-8000-%012x", 0x10+s.issued, s.issued)
	s.rowsByToken[token] = node
	s.usedTickets[ticket] = node
	s.rowCount++
	respond(http.StatusCreated, node)
}

func pad32(b ...byte) []byte {
	out := make([]byte, 32)
	copy(out, b)
	return out
}

func stubToken(secret []byte) string {
	return "tok-" + hex.EncodeToString(secret[:2])
}

func stubCfg(srv *httptest.Server, nodeDir string, secret []byte, ticket string) *EnrollConfig {
	return &EnrollConfig{
		Endpoint: srv.URL, DeviceToken: stubToken(secret), Ticket: ticket,
		Arch: "mipsle", Model: "TL-WR841N", Firmware: "3.16.1", MAC: "a4:2b:b0:cf:3d:1e",
		NodeDir: nodeDir, StateDir: filepath.Join(nodeDir, "state"), AgentVer: "v0.1.0",
	}
}

func validTicket(probeSecret []byte) string {
	return proto.SignTicket(probeSecret, proto.Ticket{
		TokenHash: "aabb", ProbeID: "probe-eu-1", IAT: time.Now().Unix(),
	})
}

func mustEnroll(t *testing.T, srv *httptest.Server, nodeDir string, secret []byte, ticket string) *EnrollOutcome {
	t.Helper()
	out, err := Enroll(context.Background(), stubCfg(srv, nodeDir, secret, ticket))
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	return out
}

// T-0302 AC1+AC3: 201 con las 7 claves exactas; secret.bin de 32 B con
// 0600; ni la clave privada ni el node_secret viajan en ningun request
// saliente (el node_secret solo aparece en el cuerpo del 201).
func TestEnrollHappyPath(t *testing.T) {
	stub, srv := newEnrollStub(t)
	nodeDir := t.TempDir()
	out := mustEnroll(t, srv, nodeDir, pad32(1), validTicket(stub.probeSecret))
	if out.Status != http.StatusCreated || out.Reenrolled {
		t.Fatalf("status = %d reenroll = %v, want 201/false", out.Status, out.Reenrolled)
	}
	if len(out.NodeSecret) != 32 {
		t.Fatalf("node_secret = %d B != 32", len(out.NodeSecret))
	}
	if stub.lastRespKeys != 7 {
		t.Fatalf("el 201 trajo %d claves != 7", stub.lastRespKeys)
	}
	secPath := filepath.Join(nodeDir, "state", "secret.bin")
	b, err := os.ReadFile(secPath)
	if err != nil || len(b) != 32 {
		t.Fatalf("secret.bin = %d B (err %v), want 32 B", len(b), err)
	}
	if !isWindows() {
		if ok, err := PrivatePermOK(secPath); err != nil || !ok {
			t.Fatalf("permisos de secret.bin: ok=%v err=%v, want 0600", ok, err)
		}
	}
	dHex := privateKeyHex(t, filepath.Join(nodeDir, "state", "key.pem"))
	for _, csr := range stub.capturedCSR {
		if strings.Contains(csr, dHex) {
			t.Fatal("la clave privada aparecio en un request saliente")
		}
	}
}

// T-0302 AC3: body sin campo csr → 400.
func TestEnrollMissingCSR400(t *testing.T) {
	_, srv := newEnrollStub(t)
	body := `{"arch":"mipsle","model":"x","fw":"1","mac":"m","csr":""}`
	r, err := http.Post(srv.URL+"/v1/enroll", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("csr vacio → %d, want 400", r.StatusCode)
	}
}

// T-0302 AC2 + T-0308 AC1: repetir el mismo device_token → 409 con el
// mismo node_id; tras reset de fabrica (estado borrado) el 409 se
// resuelve re-emitiendo certificado, sin fila nueva y sin huesrfanos:
// el ultimo certificado persistido es el re-emitido.
func TestEnrollReenroll409Idempotent(t *testing.T) {
	stub, srv := newEnrollStub(t)
	nodeDir := t.TempDir()
	first := mustEnroll(t, srv, nodeDir, pad32(2), validTicket(stub.probeSecret))
	if stub.rowCount != 1 {
		t.Fatalf("filas nodes = %d != 1", stub.rowCount)
	}
	oldFingerprint := first.Fingerprint
	if err := os.RemoveAll(filepath.Join(nodeDir, "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(nodeDir, NodeJSONName)); err != nil {
		t.Fatal(err)
	}
	second := mustEnroll(t, srv, nodeDir, pad32(2), "")
	if !second.Reenrolled || second.Status != http.StatusConflict {
		t.Fatalf("segundo alta: status %d reenroll %v, want 409/true", second.Status, second.Reenrolled)
	}
	if second.Resp.NodeID != first.Resp.NodeID {
		t.Fatalf("node_id cambio: %q != %q", second.Resp.NodeID, first.Resp.NodeID)
	}
	if stub.rowCount != 1 {
		t.Fatalf("el re-enroll creo filas: %d != 1", stub.rowCount)
	}
	if second.Fingerprint == oldFingerprint {
		t.Fatal("el certificado no fue re-emitido (fingerprint identico)")
	}
	persisted, err := os.ReadFile(filepath.Join(nodeDir, "state", "cert.pem"))
	if err != nil || string(persisted) != string(second.CertPEM) {
		t.Fatalf("cert.pem no es el ultimo certificado valido (err %v)", err)
	}
	id, err := LoadNodeIdentity(nodeDir)
	if err != nil || id.CertFingerprint != second.Fingerprint {
		t.Fatalf("node.json fingerprint = %q != %q (err %v)", id.CertFingerprint, second.Fingerprint, err)
	}
}

// T-0305 AC1: sin ticket 428; HMAC erróneo 403; valido 201.
func TestEnrollTicketGate(t *testing.T) {
	stub, srv := newEnrollStub(t)
	if _, err := Enroll(context.Background(), stubCfg(srv, t.TempDir(), pad32(3), "")); EnrollStatus(err) != 428 {
		t.Fatalf("sin ticket: err %v, want 428", err)
	}
	forged := fmt.Sprintf("v1.aabb.probe-eu-1.%d.0000000000000000", time.Now().Unix())
	if _, err := Enroll(context.Background(), stubCfg(srv, t.TempDir(), pad32(4), forged)); EnrollStatus(err) != http.StatusForbidden {
		t.Fatalf("ticket forjado: err %v, want 403", err)
	}
	out := mustEnroll(t, srv, t.TempDir(), pad32(5), validTicket(stub.probeSecret))
	if out.Status != http.StatusCreated {
		t.Fatalf("ticket valido: status %d, want 201", out.Status)
	}
}

// T-0305 AC2: el mismo ticket dos veces con tokens distintos → 409 en
// el segundo y una sola fila por ticket (indice unico).
func TestEnrollTicketSingleWriter(t *testing.T) {
	stub, srv := newEnrollStub(t)
	tk := validTicket(stub.probeSecret)
	first := mustEnroll(t, srv, t.TempDir(), pad32(6), tk)
	if first.Status != http.StatusCreated {
		t.Fatalf("primer escritor: status %d, want 201", first.Status)
	}
	second := mustEnroll(t, srv, t.TempDir(), pad32(7), tk)
	if second.Status != http.StatusConflict {
		t.Fatalf("segundo uso del ticket: status %d, want 409", second.Status)
	}
	if stub.rowCount != 1 {
		t.Fatalf("filas por ticket = %d != 1", stub.rowCount)
	}
}

// T-0305 AC3: 100 enrols concurrentes con claves propias y un unico
// ticket → exactamente un 201 y noventa y nueve 409, una sola fila.
func TestEnrollConcurrent100SingleTicket(t *testing.T) {
	stub, srv := newEnrollStub(t)
	tk := validTicket(stub.probeSecret)
	var mu sync.Mutex
	created, conflict := 0, 0
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dir := t.TempDir()
			cfg := stubCfg(srv, dir, pad32(byte(i), byte(i*7+3)), tk)
			cfg.DeviceToken = fmt.Sprintf("tok-%03d", i)
			out, err := Enroll(context.Background(), cfg)
			if err != nil {
				t.Errorf("enrol %d: %v", i, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			switch out.Status {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				conflict++
			}
		}(i)
	}
	wg.Wait()
	if created != 1 || conflict != 99 {
		t.Fatalf("201 = %d, 409 = %d; want 1/99", created, conflict)
	}
	if stub.rowCount != 1 {
		t.Fatalf("filas nodes = %d != 1", stub.rowCount)
	}
}

// privateKeyHex devuelve el hex del escalar D de la ECPrivateKey del
// disco: es lo que no debe aparecer en ningun request saliente (A3-02).
func privateKeyHex(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("key.pem: %v", err)
	}
	block, _ := pem.Decode(b)
	if block == nil {
		t.Fatal("key.pem sin bloque PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParseECPrivateKey: %v", err)
	}
	return hex.EncodeToString(key.D.Bytes())
}

func isWindows() bool { return runtime.GOOS == "windows" }
