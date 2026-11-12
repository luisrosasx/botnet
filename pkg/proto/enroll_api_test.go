package proto

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func enrollBody(t *testing.T, extra string) string {
	t.Helper()
	body := `{"node_id":"11111111-2222-3333-4444-555555555555","slug":"k7qz2m4x9p1r",` +
		`"cert_pem":"PEM","ca_pem":"CA","node_secret":"` +
		base64.StdEncoding.EncodeToString(make([]byte, 32)) +
		`","rendezvous":["rdv1:443"],"ttl_s":86400`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

// T-0302 AC1: el 201 trae exactamente las 7 claves del contrato.
func TestEnrollAPIResponseExactlySevenKeys(t *testing.T) {
	body := enrollBody(t, "")
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(keys) != 7 {
		t.Fatalf("claves = %d != 7: %v", len(keys), keys)
	}
	resp, err := DecodeEnrollAPIResponse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	sec, err := base64.StdEncoding.DecodeString(resp.NodeSecretB)
	if err != nil || len(sec) != 32 {
		t.Fatalf("node_secret = %d B != 32 (err %v)", len(sec), err)
	}
	if resp.TTLS != 86400 || len(resp.Rendezvous) != 1 {
		t.Fatalf("respuesta incompleta: %+v", resp)
	}
}

func TestEnrollAPIResponseStrictKeys(t *testing.T) {
	if _, err := DecodeEnrollAPIResponse(strings.NewReader(strings.Replace(enrollBody(t, ""), `"ttl_s":86400`, `"ttl_s":86400,"node_id_extra":"x"`, 1))); err == nil {
		t.Fatal("clave desconocida aceptada")
	}
	missing := strings.Replace(enrollBody(t, ""), `"ca_pem":"CA",`, ``, 1)
	if _, err := DecodeEnrollAPIResponse(strings.NewReader(missing)); err == nil {
		t.Fatal("clave obligatoria ausente aceptada")
	}
	// T-0306 AC3: monthly_gb_cap=150 puede venir en el 201.
	withCap := enrollBody(t, `"monthly_gb_cap":150,"bandwidth_cap_kbps":3072`)
	resp, err := DecodeEnrollAPIResponse(strings.NewReader(withCap))
	if err != nil {
		t.Fatalf("extensiones de entitlements rechazadas: %v", err)
	}
	if resp.MonthlyGbCap != 150 || resp.BandwidthCapKbps != 3072 {
		t.Fatalf("entitlements no parseadas: %+v", resp)
	}
}

func TestEnrollAPIRequestRequiresCSR(t *testing.T) {
	if _, err := DecodeEnrollAPIRequest(strings.NewReader(`{"arch":"mipsle","csr":""}`)); err == nil {
		t.Fatal("csr vacio aceptado: el backend debe responder 400")
	}
	req, err := DecodeEnrollAPIRequest(strings.NewReader(`{"arch":"mipsle","model":"TL-WR841N","fw":"3.16.1","mac":"aa:bb","csr":"-----BEGIN"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Arch != "mipsle" || req.CSRPEM == "" {
		t.Fatalf("request incompleto: %+v", req)
	}
}

// T-0304 AC1/AC3: slug de 12 alfanumericos; la credencial no es slug.
func TestSlugValidationAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		s, err := NewSlug()
		if err != nil {
			t.Fatalf("NewSlug: %v", err)
		}
		if !ValidSlug(s) {
			t.Fatalf("slug invalido %q", s)
		}
		if seen[s] {
			t.Fatalf("slug repetido: %q", s)
		}
		seen[s] = true
	}
	if len(seen) != 50 {
		t.Fatalf("distinct slug = %d != 50", len(seen))
	}
	for _, bad := range []string{"short12chr", "has-dash-12x", "UPPER_lower!", "toolong12345xx", "aXk3m9qZ12!4"} {
		if ValidSlug(bad) {
			t.Fatalf("ValidSlug acepto %q (12 alfanumericos exactos)", bad)
		}
	}
	// Un device_token tipico (>=24 chars) nunca pasa como slug.
	if ValidSlug(HashDeviceToken("device-token")) {
		t.Fatal("hash de 64 chars no debe ser slug valido")
	}
	if got := TransparencyPath("k7qz2m4x9p1r"); got != "/t/k7qz2m4x9p1r" {
		t.Fatalf("TransparencyPath = %q", got)
	}
}

// T-0305: el ticket lo firma el probe; forjarlo (HMAC erroneo) falla.
func TestIntrusionTicketSignVerify(t *testing.T) {
	secret := []byte("probe-secret-de-flota")
	tk := Ticket{TokenHash: HashDeviceToken("tok"), ProbeID: "probe-eu-1", IAT: time.Now().Unix()}
	wire := SignTicket(secret, tk)
	got, err := VerifyTicket(secret, wire)
	if err != nil {
		t.Fatalf("VerifyTicket: %v", err)
	}
	if got.ProbeID != tk.ProbeID || got.TokenHash != tk.TokenHash || got.IAT != tk.IAT {
		t.Fatalf("roundtrip: %+v != %+v", got, tk)
	}
	if _, err := VerifyTicket([]byte("otro-secreto"), wire); err != ErrTicketForged {
		t.Fatalf("HMAC erroneo: err = %v, want ErrTicketForged", err)
	}
	if _, err := VerifyTicket(secret, "basura"); err == nil {
		t.Fatal("ticket malformado aceptado")
	}
	old := Ticket{TokenHash: tk.TokenHash, ProbeID: tk.ProbeID, IAT: tk.IAT - 7200}
	if !TicketExpired(SignTicketDecodeOnly(t, secret, old), time.Now(), time.Hour) {
		t.Fatal("ticket de hace 2 h no caducado con ttl 1 h")
	}
}

// SignTicketDecodeOnly re-parsea el ticket firmado para quedarnos solo
// con el struct (el TTL se evalua sobre el struct, no sobre el wire).
func SignTicketDecodeOnly(t *testing.T, secret []byte, tk Ticket) Ticket {
	t.Helper()
	got, err := VerifyTicket(secret, SignTicket(secret, tk))
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	return got
}

func TestTicketExpiry(t *testing.T) {
	now := time.Now()
	fresh := Ticket{TokenHash: "h", ProbeID: "p", IAT: now.Add(-time.Minute).Unix()}
	stale := Ticket{TokenHash: "h", ProbeID: "p", IAT: now.Add(-2 * time.Hour).Unix()}
	if TicketExpired(fresh, now, time.Hour) {
		t.Fatal("ticket fresco caducado")
	}
	if !TicketExpired(stale, now, time.Hour) {
		t.Fatal("ticket viejo no caducado")
	}
	if TicketExpired(stale, now, 0) {
		t.Fatal("ttl<=0 no debe caducar")
	}
}
