package logger

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// AC1: las tres metricas del contrato presentes y en texto Prometheus.
func TestMetricsContractThreeNames(t *testing.T) {
	m := NewMetrics("gw")
	m.StreamsActive().Set(37)
	m.TrackAlloc(func() { time.Sleep(2 * time.Millisecond) })
	var buf bytes.Buffer
	if err := m.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body := buf.String()
	for _, want := range []string{MetricStreamsActive, MetricRSSKiB, MetricAllocMS} {
		if !strings.Contains(body, want) {
			t.Fatalf("faltan metricas en /metrics: %q", want)
		}
		if !strings.Contains(body, "# TYPE "+want) {
			t.Fatalf("sin # TYPE para %q:\n%s", want, body)
		}
	}
	if v := m.AllocMS().Value(); v <= 0 {
		t.Fatalf("botnet_alloc_ms = %v, want > 0 tras TrackAlloc", v)
	}
}

// El gauge RSS reporta el footprint real del proceso (estimacion con
// doble fuente: procfs exacta donde existe, runtime como fallback).
func TestRSSSamplePositive(t *testing.T) {
	if got := SampleRSSKiB(); got == 0 {
		t.Fatal("botnet_rss_kib = 0, want > 0")
	}
}

// El endpoint /metrics responde con el content-type Prometheus.
func TestMetricsHandler(t *testing.T) {
	m := NewMetrics("rdv")
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /metrics = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), MetricStreamsActive) {
		t.Fatalf("respuesta sin metrica del contrato: %q", rec.Body.String())
	}
}

// AC3: cada linea del spool es UN objeto JSON parseable, incluso con
// mensajes que traen saltos de linea crudos (0 lineas multilinea), y el
// spool rota por tamano en el techo del flash (clase C).
func TestSpoolJSONLines(t *testing.T) {
	var mirror bytes.Buffer
	dir := t.TempDir()
	s := NewSpool(dir, &mirror, 200)
	for i := 0; i < 30; i++ {
		l := NewLine("info", "asignacion de sesion\nsegunda linea cruda", "sess-42")
		if err := s.WriteLine(l); err != nil {
			t.Fatalf("WriteLine: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "botnet-spool.log"))
	if err != nil {
		t.Fatalf("spool: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 0 || len(lines) > 30 {
		t.Fatalf("lineas del spool = %d (rotacion: <= 30 y > 0)", len(lines))
	}
	for _, ln := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("linea no parseable por jq: %q: %v", ln, err)
		}
		if rec["level"] != "info" || rec["session_id"] != "sess-42" {
			t.Fatalf("registro sin level/session_id: %v", rec)
		}
	}
	if s.Lines() != 30 {
		t.Fatalf("Lines = %d, want 30", s.Lines())
	}
	if mirror.String() == "" {
		t.Fatal("el espejo stderr quedo vacio")
	}
}
