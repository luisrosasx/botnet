package recruit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func postJSON(t *testing.T, h http.Handler, path string, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	var out map[string]any
	if rec.Body.Len() > 0 {
		json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// TestClaimYComplete: el reparto entrega cinco y el complete cierra los
// cinco con un outcome valido.
func TestClaimYComplete(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	seedCandidates(t, st, 8)
	h := (&API{Store: st}).Handler()

	code, out := postJSON(t, h, "/v1/work/claim", `{"stage":"deliver","worker":"probe-1","limit":5}`)
	if code != http.StatusOK {
		t.Fatalf("claim=%d (quiero 200)", code)
	}
	items, _ := out["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("claim devolvio %d (quiero 5)", len(items))
	}
	for _, it := range items {
		m := it.(map[string]any)
		cuerpo, _ := json.Marshal(map[string]any{
			"candidate_id": int64(m["candidate_id"].(float64)),
			"ip":           m["ip"],
			"port":         int(m["port"].(float64)),
			"stage":        "deliver",
			"attempt":      int(m["attempt"].(float64)),
			"outcome":      "ok",
			"worker":       "probe-1",
			"lease_token":  int64(m["lease_token"].(float64)),
		})
		code, _ := postJSON(t, h, "/v1/work/complete", string(cuerpo))
		if code != http.StatusOK {
			t.Fatalf("complete=%d (quiero 200)", code)
		}
	}
	var cerrados int
	if err := st.db.QueryRow(`
		SELECT count(*) FROM recruit_jobs WHERE worker='probe-1'
		   AND finished_at IS NOT NULL AND outcome='ok'`).Scan(&cerrados); err != nil {
		t.Fatalf("jobs: %v", err)
	}
	if cerrados != 5 {
		t.Fatalf("jobs cerrados=%d (quiero 5)", cerrados)
	}
}

// TestCompleteIdempotente: repetir la clave no duplica ni escribe eventos.
func TestCompleteIdempotente(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	seedCandidates(t, st, 1)
	h := (&API{Store: st}).Handler()
	_, out := postJSON(t, h, "/v1/work/claim", `{"stage":"deliver","worker":"probe-1","limit":1}`)
	items := out["items"].([]any)
	m := items[0].(map[string]any)
	cuerpo, _ := json.Marshal(map[string]any{
		"candidate_id": int64(m["candidate_id"].(float64)),
		"ip":           m["ip"],
		"port":         int(m["port"].(float64)),
		"stage":        "deliver",
		"attempt":      int(m["attempt"].(float64)),
		"outcome":      "fail",
		"reason":       "no_cred",
		"worker":       "probe-1",
		"lease_token":  int64(m["lease_token"].(float64)),
	})
	if code, _ := postJSON(t, h, "/v1/work/complete", string(cuerpo)); code != http.StatusOK {
		t.Fatalf("primer complete=%d", code)
	}
	var eventosAntes int
	st.db.QueryRow(`SELECT count(*) FROM recruit_candidate_events`).Scan(&eventosAntes)

	code, out2 := postJSON(t, h, "/v1/work/complete", string(cuerpo))
	if code != http.StatusOK {
		t.Fatalf("complete duplicado=%d (quiero 200)", code)
	}
	if escribio, _ := out2["escribio"].(bool); escribio {
		t.Fatal("el duplicado no debe escribir")
	}
	key := jobKey(m["ip"].(string), int(m["port"].(float64)), "deliver", int(m["attempt"].(float64)))
	var n, eventosDespues int
	st.db.QueryRow(`SELECT count(*) FROM recruit_jobs WHERE idempotency_key=$1`, key).Scan(&n)
	st.db.QueryRow(`SELECT count(*) FROM recruit_candidate_events`).Scan(&eventosDespues)
	if n != 1 {
		t.Fatalf("jobs con la clave=%d (quiero 1)", n)
	}
	if eventosDespues != eventosAntes {
		t.Fatalf("eventos %d -> %d (quiero igual)", eventosAntes, eventosDespues)
	}
}

// TestPausaNoReparteTrabajo: en pausa no hay 200 con carga ni jobs nuevos.
func TestPausaNoReparteTrabajo(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := st.SetPaused(ctx, true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	seedCandidates(t, st, 4)
	h := (&API{Store: st}).Handler()
	code, _ := postJSON(t, h, "/v1/work/claim", `{"stage":"deliver","worker":"w","limit":2}`)
	if code != http.StatusNoContent && code != http.StatusConflict {
		t.Fatalf("claim en pausa=%d (quiero 204 o 409)", code)
	}
	var jobs int
	st.db.QueryRow(`SELECT count(*) FROM recruit_jobs WHERE claimed_at > now() - interval '60 s'`).Scan(&jobs)
	if jobs != 0 {
		t.Fatalf("jobs reclamados en pausa=%d (quiero 0)", jobs)
	}
}
