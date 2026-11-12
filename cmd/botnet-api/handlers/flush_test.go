// Test del flush contable (T-1002): el reenvio del mismo flush_id
// responde 202 {dedup:true} sin mover la suma de usage; la ruta real
// (FlushRepo sobre Postgres) vierte sessions.bytes_* y usage(day,
// node_id). El webhook Stripe (T-1004) comparte fichero: firma local
// con el mismo esquema HMAC-SHA256.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/accounting"
	"github.com/botnet/botnet/internal/backend/testenv"
	"github.com/botnet/botnet/internal/proxy"
)

// --- dedup in-process (sin BD) ------------------------------------------------

type trackSink struct {
	calls int
	sum   int64
}

func (s *trackSink) ApplyFlush(env accounting.FlushEnvelope) error {
	s.calls++
	s.sum += accounting.FlushTotalBytes(env)
	return nil
}

func flushBody(t *testing.T, env accounting.FlushEnvelope) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewBuffer(b)
}

func TestFlushDedupHTTP(t *testing.T) {
	sink := &trackSink{}
	d := &Deps{FlushSink: sink, Dedup: accounting.NewMemDedup()}
	app := NewApp(d)

	env := accounting.FlushEnvelope{
		FlushID: "node-9-00000001", NodeID: "node-9",
		At:       time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		Sessions: []proxy.SessionUsage{{SessionID: "7", BytesIn: 100, BytesOut: 150}},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes/node-9/flush", flushBody(t, env))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("primer flush: %d", resp.StatusCode)
	}
	var first map[string]any
	json.NewDecoder(resp.Body).Decode(&first)
	if first["applied"] != true || first["bytes"].(float64) != 250 {
		t.Fatalf("respuesta 200: %v", first)
	}
	resp.Body.Close()

	// Redelivery del durable: mismo flush_id → 202 {dedup:true} y la
	// suma de usage es invariante.
	req = httptest.NewRequest(http.MethodPost, "/v1/nodes/node-9/flush", flushBody(t, env))
	resp, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("reenvio: want 202, hay %d", resp.StatusCode)
	}
	var second map[string]any
	json.NewDecoder(resp.Body).Decode(&second)
	resp.Body.Close()
	if second["dedup"] != true {
		t.Fatalf("202 sin dedup:true: %v", second)
	}
	if sink.calls != 1 || sink.sum != 250 {
		t.Fatalf("el sink se invoco %d veces, suma %d", sink.calls, sink.sum)
	}

	// node_id del envelope no coincide → 400.
	req = httptest.NewRequest(http.MethodPost, "/v1/nodes/otro/flush", flushBody(t, env))
	if resp, _ = app.Test(req); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("node_id torcido: want 400, hay %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Sin flush_id → 400 (no hay clave de dedup).
	req = httptest.NewRequest(http.MethodPost, "/v1/nodes/node-9/flush",
		bytes.NewBufferString(`{"node_id":"node-9"}`))
	if resp, _ = app.Test(req); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sin flush_id: want 400, hay %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Plano contable apagado → 503.
	if resp, _ = NewApp(&Deps{}).Test(httptest.NewRequest(http.MethodPost,
		"/v1/nodes/node-9/flush", flushBody(t, env))); resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("plano apagado: want 503, hay %d", resp.StatusCode)
	}
}

// --- ruta real Postgres + webhook Stripe -------------------------------------

func TestFlushAndWebhookOnPostgres(t *testing.T) {
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 1)
	tenantID := testenv.SeedTenant(t, db, "bill-tenant")

	// Sesion viva del nodo para recibir los contadores del flush.
	var sid int64
	if err := db.QueryRowContext(context.Background(), `
		INSERT INTO sessions (node_id, tenant_id, gw_id, started_at)
		VALUES ($1::uuid, $2::uuid, 'gw-1', now()) RETURNING session_id`,
		nodeID, tenantID).Scan(&sid); err != nil {
		t.Fatal(err)
	}

	day := time.Now().UTC()
	env := accounting.FlushEnvelope{
		FlushID: accounting.FlushIDEmitido(nodeID, uint64(time.Now().UnixNano())), NodeID: nodeID, At: day,
		Sessions: []proxy.SessionUsage{{SessionID: strconv.FormatInt(sid, 10), BytesIn: 4000, BytesOut: 6000}},
	}
	d := &Deps{
		Repo: repo.New(db), RDB: rdb,
		FlushSink: FlushRepo{Repo: repo.New(db)}, Dedup: RedisDedup{RDB: rdb},
		StripeSecret: "whsec_ep10_test",
	}
	app := NewApp(d)

	post := func() *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/v1/nodes/"+nodeID+"/flush", flushBody(t, env))
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := post(); resp.StatusCode != http.StatusOK {
		t.Fatalf("flush real: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}

	var usageBytes int64
	var sessIn, sessOut int64
	if err := db.QueryRowContext(context.Background(),
		`SELECT bytes FROM usage WHERE day = $1::date AND node_id = $2::uuid`,
		day.Format("2006-01-02"), nodeID).Scan(&usageBytes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(),
		`SELECT bytes_in, bytes_out FROM sessions WHERE session_id = $1`, sid).
		Scan(&sessIn, &sessOut); err != nil {
		t.Fatal(err)
	}
	if usageBytes != 10000 || sessIn != 4000 || sessOut != 6000 {
		t.Fatalf("usage=%d sessions=(%d,%d), want 10000/4000/6000", usageBytes, sessIn, sessOut)
	}

	// Redelivery: 202 y la suma de usage sigue siendo 10000.
	if resp := post(); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("reenvio durable: want 202, hay %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	var again int64
	db.QueryRowContext(context.Background(),
		`SELECT bytes FROM usage WHERE day = $1::date AND node_id = $2::uuid`,
		day.Format("2006-01-02"), nodeID).Scan(&again)
	if again != usageBytes {
		t.Fatalf("la suma de uso se movio con el dedup: %d -> %d", usageBytes, again)
	}

	// Webhook invoice.paid firmado con el esquema local: 200 y marca
	// usage.reconciled; firma torcida: 400 sin tocar nada.
	body := []byte(`{"type":"invoice.paid","day":"` + day.Format("2006-01-02") +
		`","gb":0.00001,"node_ids":["` + nodeID + `"],"invoice_id":"in_1"}`)
	hook := func(b []byte, header string) *http.Response {
		req := httptest.NewRequest(http.MethodPost, "/v1/billing/webhook", bytes.NewReader(b))
		if header != "" {
			req.Header.Set(accounting.WebhookSignatureHeader, header)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	good := accounting.SignWebhook("whsec_ep10_test", time.Now().UTC(), body)
	if resp := hook(body, good); resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook firmado: %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
	var rec bool
	db.QueryRowContext(context.Background(),
		`SELECT reconciled FROM usage WHERE day = $1::date AND node_id = $2::uuid`,
		day.Format("2006-01-02"), nodeID).Scan(&rec)
	if !rec {
		t.Fatal("invoice.paid valido no marco usage.reconciled")
	}
	// Firma ajena (secreto distinto): el esquema T-1104 AC1 la clasifica
	// como firma forjada -> 403, no 400; 400 queda solo para payload
	// sin firma o malformado.
	bad := accounting.SignWebhook("otro-secreto", time.Now().UTC(), body)
	if resp := hook(body, bad); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("webhook con firma ajena: want 403, hay %d", resp.StatusCode)
	} else {
		resp.Body.Close()
	}
}
