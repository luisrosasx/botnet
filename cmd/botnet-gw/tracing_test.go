package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// AC2: una sesion de test deja >= 4 spans queryables por session_id
// (alloc → open → close → flush) contra el collector de prueba.
func TestTraceSessionFourSpans(t *testing.T) {
	tr := NewTracer(0)
	tr.TraceSession("sess-042")
	got := tr.Query("sess-042")
	if len(got) < SpansPerSession {
		t.Fatalf("spans queryables = %d, want >= %d", len(got), SpansPerSession)
	}
	wantNames := []string{SpanAlloc, SpanOpen, SpanClose, SpanFlush}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Fatalf("span %d = %q, want %q", i, got[i].Name, want)
		}
		if got[i].SessionID != "sess-042" {
			t.Fatalf("span %d con session_id = %q (mismo id de punta a punta)", i, got[i].SessionID)
		}
		if got[i].DurS <= 0 {
			t.Fatalf("span %q sin duracion: %v", want, got[i].DurS)
		}
	}
	if tr.Sessions() != 1 {
		t.Fatalf("Sessions = %d, want 1", tr.Sessions())
	}
}

// El techo por sesion acota el spool en memoria del gw (32 MB clase C).
func TestTracerCapPerSession(t *testing.T) {
	tr := NewTracer(SpansPerSession)
	for i := 0; i < 3*SpansPerSession; i++ {
		tr.Record("sess-cap", SpanFlush, time.Now(), time.Millisecond)
	}
	if got := len(tr.Query("sess-cap")); got != SpansPerSession {
		t.Fatalf("spans con techo = %d, want %d", got, SpansPerSession)
	}
}

// La traza se consulta por HTTP sin ssh al nodo (DoD T-1201).
func TestTracerHandlerQueryable(t *testing.T) {
	tr := NewTracer(0)
	tr.TraceSession("sess-http")
	rec := httptest.NewRecorder()
	tr.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/trace?session_id=sess-http", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /trace = %d", rec.Code)
	}
	var spans []Span
	if err := json.Unmarshal(rec.Body.Bytes(), &spans); err != nil {
		t.Fatalf("traza no JSON: %v", err)
	}
	if len(spans) < SpansPerSession {
		t.Fatalf("handler devolvio %d spans, want >= %d", len(spans), SpansPerSession)
	}
}
