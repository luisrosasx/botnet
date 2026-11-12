// Test del cableado agente↔backend (EP-03 T-0302/T-0308): el 409 de
// enroll lleva el node_id del nodo existente y quema el serial del cert
// anterior; POST /v1/heartbeat a pelo (la ruta que realmente llama
// botd) acepta los dos codecs v1; /v1/webhooks/stripe es alias vivo del
// webhook de billing. Requiere Postgres+Redis (testenv skip si falta).
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/hbcodec"
	"github.com/botnet/botnet/pkg/proto"
)

// enrollOnce da de alta un token fresco y devuelve (node_id, 201-raw).
func enrollOnce(t *testing.T, h *enrollHarness, token string) (string, []byte) {
	t.Helper()
	code, raw := h.enrollDo(t, token, ticketFor(h.deps.Cfg.TicketSecret, token, "probe-hb"), newCSR(t, "botnet-node"))
	if code != 201 {
		t.Fatalf("alta %q: quiero 201, tengo %d body=%s", token, code, raw)
	}
	var api proto.EnrollAPIResponse
	if err := json.Unmarshal(raw, &api); err != nil {
		t.Fatalf("201 ilegible: %v (%s)", err, raw)
	}
	return api.NodeID, raw
}

// T-0302 AC2 + T-0308 AC3: repetir el token con un ticket nuevo (el
// caso reset de fabrica) responde 409 con {"error":..,"node_id":..} y
// revoked:{serial} del certificado anterior queda en Redis para el CRL
// del gateway.
func TestEnroll409LlevaNodeIDYSerialRevocado(t *testing.T) {
	h := newEnrollHarness(t)
	token := "tok-409-min-" + t.Name()
	nodeID, _ := enrollOnce(t, h, token)

	serial, err := h.deps.RDB.Get(context.Background(), "node:"+nodeID+":cert_serial").Result()
	if err != nil || serial == "" {
		t.Fatalf("node:{id}:cert_serial ausente en Redis: %v", err)
	}

	app := NewApp(h.deps)
	// Repetir el token con ticket FRESCO = re-enrolado tras reset de
	// fabrica (ticket nuevo sobre nodo existente): 409 con node_id y el
	// serial viejo a la CRL (T-0308 AC3). Un reintento idempotente del
	// mismo alta NO quema el serial: T-0302 AC2, regresión cubierta por
	// TestEnroll409SerialLifecycle.
	code, raw := h.enrollDo(t, token, ticketFor(h.deps.Cfg.TicketSecret, token, "probe-reset"), newCSR(t, "botnet-node"))
	if code != 409 {
		t.Fatalf("repetir token: quiero 409, tengo %d body=%s", code, raw)
	}
	var minimal map[string]string
	if err := json.Unmarshal(raw, &minimal); err != nil {
		t.Fatalf("409 ilegible: %v (%s)", err, raw)
	}
	if minimal["node_id"] != nodeID {
		t.Errorf("409 node_id = %q, want %q", minimal["node_id"], nodeID)
	}
	if minimal["error"] == "" {
		t.Errorf("409 sin clave error: %s", raw)
	}
	v, err := h.deps.RDB.Get(context.Background(), "revoked:"+serial).Result()
	if err != nil || v != "1" {
		t.Errorf("revoked:{serial} del cert anterior: v=%q err=%v", v, err)
	}
	_ = app
}

// T-0308 AC2: la ruta a pelo que llama el agente (POST /v1/heartbeat,
// node_id en X-Node-Id) responde 200 con HeartbeatResponse para los dos
// codecs v1 que emite el agente: pkg/proto (nucleo 200 B) y el frame
// compacto de internal/hbcodec.
func TestHeartbeatRutaPelaAceptaCodecsV1(t *testing.T) {
	h := newEnrollHarness(t)
	nodeID, _ := enrollOnce(t, h, "tok-hb-bare-"+t.Name())
	idBytes, err := agent.ParseUUID(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(h.deps)

	post := func(path string, body []byte, header string) int {
		t.Helper()
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		if header != "" {
			req.Header.Set("X-Node-Id", header)
		}
		resp, terr := app.Test(req, 5000)
		if terr != nil {
			t.Fatal(terr)
		}
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			var hr proto.HeartbeatResponse
			if b, rerr := io.ReadAll(resp.Body); rerr == nil && len(b) > 0 {
				if uerr := hr.Unmarshal(b); uerr != nil {
					t.Errorf("200 sin HeartbeatResponse valida: %v", uerr)
				}
			}
		}
		return resp.StatusCode
	}

	pb := proto.Heartbeat{NodeID: idBytes, Seq: 1, RssKiB: 3000, UptimeS: 60, Streams: 2, AgentVer: "v0.1.0"}
	buf, err := pb.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if code := post("/v1/heartbeat", buf, nodeID); code != 200 {
		t.Errorf("heartbeat proto por ruta a pelo: quiero 200, tengo %d", code)
	}
	if code := post("/v1/heartbeat", buf, ""); code != 200 {
		t.Errorf("heartbeat proto con NodeID del frame: quiero 200, tengo %d", code)
	}

	hc := hbcodec.Heartbeat{NodeID: idBytes, Seq: 2, RssKiB: 3000, UptimeS: 120, Streams: 1, AgentVer: "v0.1.0"}
	hbFrame, err := hc.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if code := post("/v1/heartbeat", hbFrame, nodeID); code != 200 {
		t.Errorf("heartbeat hbcodec v1: quiero 200, tengo %d", code)
	}
	if code := post("/v1/heartbeat/"+nodeID, hbFrame, ""); code != 200 {
		t.Errorf("heartbeat hbcodec por ruta con node_id: quiero 200, tengo %d", code)
	}

	// El nodo sigue vivo: last_seen_at estampo (presencia Redis).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, err := h.deps.RDB.Exists(context.Background(), "node:"+nodeID+":live").Result(); err == nil && n == 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("node:{id}:live no se re-anudo tras los heartbeat")
}

// T-1104 AC1 (mitad ruta): /v1/webhooks/stripe existe y es el mismo
// handler que /v1/billing/webhook — ambos 400 sin firma con el secreto
// puesto (no 404 la primera, no 503 «plano apagado» la segunda).
func TestWebhookStripeAlias(t *testing.T) {
	h := newEnrollHarness(t)
	h.deps.StripeSecret = "whsec_alias_test"
	app := NewApp(h.deps)
	body := []byte(`{"id":"evt_alias","type":"invoice.paid"}`)
	codes := map[string]int{}
	for _, path := range []string{"/v1/webhooks/stripe", "/v1/billing/webhook"} {
		req := httptest.NewRequest("POST", path, bytes.NewReader(body))
		resp, err := app.Test(req, 5000)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		codes[path] = resp.StatusCode
	}
	if codes["/v1/webhooks/stripe"] == 404 {
		t.Fatal("/v1/webhooks/stripe sigue sin montar (404)")
	}
	if codes["/v1/webhooks/stripe"] != codes["/v1/billing/webhook"] {
		t.Errorf("alias divergen: stripe=%d billing=%d (sin firma ambos deben 400)",
			codes["/v1/webhooks/stripe"], codes["/v1/billing/webhook"])
	}
}

