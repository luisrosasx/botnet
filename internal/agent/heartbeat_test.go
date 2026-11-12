package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/state"
	"github.com/botnet/botnet/pkg/proto"
)

// El doble del stub F1: responde HeartbeatResponse binario y opcional
// segun el modo; vacio = stub compatible (zero-values).
func newHeartbeatStub(t *testing.T, resp func(seq uint32) *proto.HeartbeatResponse) (*httptest.Server, *atomic.Uint32) {
	t.Helper()
	var hits atomic.Uint32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/heartbeat" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var hb proto.Heartbeat
		if len(body) > 0 {
			if err := hb.Unmarshal(body); err != nil {
				t.Errorf("heartbeat del agente no decodifica: %v", err)
			}
		}
		n := hits.Add(1)
		if resp == nil {
			return
		}
		if hr := resp(n); hr != nil {
			b, err := hr.Marshal()
			if err != nil {
				t.Errorf("Marshal respuesta: %v", err)
				return
			}
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestHeartbeatBeatRoundTrip(t *testing.T) {
	nodeID := [16]byte{1, 2, 3}
	srv, _ := newHeartbeatStub(t, func(seq uint32) *proto.HeartbeatResponse {
		return &proto.HeartbeatResponse{NextIntervalS: 40, ConfigRev: 5, Bye: seq == 2}
	})
	c := &HeartbeatClient{Endpoint: srv.URL, NodeID: nodeID, AgentVer: "v0.1.0"}
	status, hr, err := c.Beat(context.Background(), 1, 100, 60, 0)
	if err != nil || status != http.StatusOK {
		t.Fatalf("beat: status %d err %v", status, err)
	}
	if hr.ConfigRev != 5 || hr.NextIntervalS != 40 || hr.Bye {
		t.Fatalf("respuesta: %+v", hr)
	}
}

func TestHeartbeatStubEmptyBodyCompatible(t *testing.T) {
	srv, _ := newHeartbeatStub(t, nil)
	c := &HeartbeatClient{Endpoint: srv.URL, NodeID: [16]byte{}, AgentVer: "v0.1.0"}
	status, hr, err := c.Beat(context.Background(), 1, 0, 0, 0)
	if err != nil || status != http.StatusOK {
		t.Fatalf("stub vacio: status %d err %v", status, err)
	}
	if hr.ConfigRev != 0 {
		t.Fatalf("stub vacio debe dar zero-values, got %+v", hr)
	}
}

// T-0301 AC3: el bucle del agente en reposo no filtra gorutinas — el
// diff de runtime.NumGoroutine tras un ciclo de latidos queda < 5.
func TestAgentLoopGoroutineStability(t *testing.T) {
	srv, hits := newHeartbeatStub(t, func(seq uint32) *proto.HeartbeatResponse {
		return &proto.HeartbeatResponse{NextIntervalS: 120}
	})
	mgr := NewEntitlementManager('C', Entitlements{})
	ctx, cancel := context.WithCancel(context.Background())
	base := runtime.NumGoroutine()
	done := make(chan error, 1)
	go func() {
		done <- RunAgentLoop(ctx, AgentDeps{
			Log:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Machine:   state.New(),
			Client:    &HeartbeatClient{Endpoint: srv.URL, NodeID: [16]byte{9}, AgentVer: "v0.1.0"},
			Manager:   mgr,
			Sup:       NewSupervisor(slog.New(slog.NewJSONHandler(io.Discard, nil))),
			IntervalS: 3600,
		})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for hits.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() < 1 {
		t.Fatal("el bucle no latio")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	diff := runtime.NumGoroutine() - base
	if diff < 0 {
		diff = -diff
	}
	if diff >= 5 {
		t.Fatalf("diff de gorutinas = %d >= 5 tras el ciclo", diff)
	}
}

// T-0306 AC1 (pata de latido): la respuesta del heartbeat con
// config_rev mayor llega al manager a traves del bucle sin reinicio.
func TestAgentLoopAppliesConfigRev(t *testing.T) {
	srv, hits := newHeartbeatStub(t, func(seq uint32) *proto.HeartbeatResponse {
		return &proto.HeartbeatResponse{NextIntervalS: 120, ConfigRev: 2, BandwidthCapKbps: 2048}
	})
	mgr := NewEntitlementManager('B', Entitlements{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		for hits.Load() < 1 {
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	if err := RunAgentLoop(ctx, AgentDeps{
		Log:       slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Machine:   state.New(),
		Client:    &HeartbeatClient{Endpoint: srv.URL, NodeID: [16]byte{7}, AgentVer: "v0.1.0"},
		Manager:   mgr,
		Sup:       NewSupervisor(slog.New(slog.NewJSONHandler(io.Discard, nil))),
		IntervalS: 3600,
	}); err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if got := mgr.BandwidthBytesPerSec(); got != 2048*1024 {
		t.Fatalf("el bucle no aplico el tope del heartbeat: %d B/s", got)
	}
}
