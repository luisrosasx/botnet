package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/state"
	"github.com/botnet/botnet/internal/tunnel"
	"github.com/botnet/botnet/pkg/proto"
)

// agentStub: backend de prueba con paths del plano de control binario
// (heartbeat/ping/cmdack), contadores y respuestas por numero de hit.
type agentStub struct {
	srv       *httptest.Server
	hits      atomic.Uint32
	pings     atomic.Uint32
	ackBodies [][]byte
	mu        sync.Mutex
	resp      func(seq uint32) (int, *proto.HeartbeatResponse)
}

func newAgentStub(t *testing.T, resp func(seq uint32) (int, *proto.HeartbeatResponse)) *agentStub {
	t.Helper()
	a := &agentStub{resp: resp}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/heartbeat":
			body, _ := io.ReadAll(r.Body)
			var hb proto.Heartbeat
			if len(body) > 0 {
				if err := hb.Unmarshal(body); err != nil {
					t.Errorf("heartbeat no decodifica: %v", err)
				}
			}
			seq := a.hits.Add(1)
			code, hr := a.resp(seq)
			if code != 0 && code != 200 {
				w.WriteHeader(code)
			}
			if hr != nil {
				b, _ := hr.Marshal()
				_, _ = w.Write(b)
			}
		case "/v1/ping":
			a.pings.Add(1)
		case "/v1/cmdack":
			body, _ := io.ReadAll(r.Body)
			a.mu.Lock()
			a.ackBodies = append(a.ackBodies, body)
			a.mu.Unlock()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(a.srv.Close)
	return a
}

func (a *agentStub) ackIDs(t *testing.T) []string {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.ackBodies) == 0 {
		return nil
	}
	var msg proto.CmdAckMsg
	if err := msg.Unmarshal(a.ackBodies[len(a.ackBodies)-1]); err != nil {
		t.Fatalf("cmdack no decodifica: %v", err)
	}
	return msg.IDs
}

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// T-0409 AC1: el servidor dicta next_interval_s=90 y el agente aplica la
// nueva cadencia en el frame siguiente (intervalo medido: 90 s exactos
// aqui; el ±10 % lo anade el jitter del soak real).
func TestServerDictatesNextInterval(t *testing.T) {
	stub := newAgentStub(t, func(seq uint32) (int, *proto.HeartbeatResponse) {
		// latido 1: sin dictado (usa IntervalS=1 s para que el test no
		// duerma 120 s); latido 2: el servidor dicta 90 s.
		if seq == 1 {
			return 200, &proto.HeartbeatResponse{NextIntervalS: 0}
		}
		return 200, &proto.HeartbeatResponse{NextIntervalS: 90}
	})
	var delays []time.Duration
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunAgentLoop(ctx, AgentDeps{
			Log:       testLogger(),
			Machine:   state.New(),
			Client:    &HeartbeatClient{Endpoint: stub.srv.URL, NodeID: [16]byte{1}},
			Sup:       NewSupervisor(testLogger()),
			IntervalS: 1,
			OnDelay: func(d time.Duration) {
				delays = append(delays, d)
				if len(delays) >= 2 {
					cancel()
				}
			},
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunAgentLoop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bucle no salio")
	}
	if len(delays) < 2 || delays[1] != 90*time.Second {
		t.Fatalf("delays = %v, want 2.º = 90 s dictado por el servidor", delays)
	}
}

// T-0409 AC1 (parte 410): retiring|burned -> modo drain sin streams
// nuevos: el bucle drena y sale con el estado en bye.
func TestGone410EntersDrain(t *testing.T) {
	stub := newAgentStub(t, func(seq uint32) (int, *proto.HeartbeatResponse) {
		return http.StatusGone, nil
	})
	var drained, flushed atomic.Bool
	mach := state.New()
	if err := mach.Transition(state.Agent); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := RunAgentLoop(ctx, AgentDeps{
		Log:     testLogger(),
		Machine: mach,
		Client:  &HeartbeatClient{Endpoint: stub.srv.URL, NodeID: [16]byte{2}},
		Sup:     NewSupervisor(testLogger()),
		Drain:   func() error { drained.Store(true); return nil },
		Flush:   func() error { flushed.Store(true); return nil },
	}); err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if !drained.Load() || !flushed.Load() {
		t.Fatal("410 deberia drenar y hacer el ultimo flush")
	}
	if mach.State() != state.Bye {
		t.Fatalf("estado = %v, want bye", mach.State())
	}
}

// T-0409 AC2: pending_cmds con update y reboot -> encolados por
// ctl.{node_id}.cmd con dedup por id; el ack viaja en el heartbeat
// siguiente (assert de id dedup en el cuerpo CmdAckMsg).
func TestPendingCmdsDedupAndAck(t *testing.T) {
	stub := newAgentStub(t, func(seq uint32) (int, *proto.HeartbeatResponse) {
		switch seq {
		case 1:
			return 200, &proto.HeartbeatResponse{PendingCmds: []string{"update:a1b2", "reboot:c3d4"}}
		case 2:
			// reenvio del id conocido + uno nuevo: dedup por id.
			return 200, &proto.HeartbeatResponse{PendingCmds: []string{"update:a1b2", "reboot:e5f6"}}
		default:
			return 200, &proto.HeartbeatResponse{Bye: true}
		}
	})
	q := NewCmdQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mach := state.New()
	_ = mach.Transition(state.Agent)
	done := make(chan error, 1)
	var seenIDs []string
	go func() {
		done <- RunAgentLoop(ctx, AgentDeps{
			Log:       testLogger(),
			Machine:   mach,
			Client:    &HeartbeatClient{Endpoint: stub.srv.URL, NodeID: [16]byte{3}},
			Sup:       NewSupervisor(testLogger()),
			IntervalS: 1,
			Cmds:      q,
			OnDelay: func(time.Duration) {
				// el ack del ciclo 1 sale en el heartbeat 2: capturalo
				// cuando el bucle lo haya enviado.
				if stub.hits.Load() >= 2 && len(seenIDs) == 0 {
					seenIDs = stub.ackIDs(t)
				}
				if stub.hits.Load() >= 3 {
					cancel()
				}
			},
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bucle no salio")
	}
	// el queue local tiene exactamente un cmd por id visto:
	// a1b2, c3d4, e5f6 (el reenvio de a1b2 no re-encola).
	if q.Len() != 3 {
		t.Fatalf("cmds encolados = %d, want 3 (dedup por id): %v", q.Len(), CTLCtlQueueName("3"))
	}
	// el ack del heartbeat 2 lleva los ids del heartbeat 1 una sola vez.
	for _, id := range []string{"a1b2", "c3d4"} {
		n := 0
		for _, got := range seenIDs {
			if got == id {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("id %q acked %d veces, want 1 (ids %v)", id, n, seenIDs)
		}
	}
}

// T-0409 AC3: flag bye -> el agente drena, hace el ultimo flush y borra
// cert + node_secret: los ficheros del state ya no existen (0 claves
// vivas) y el router puede apagarse despues — la baja no depende de un
// POST saliente.
func TestByeWipesKeysAndExits(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{certFileName, SecretFileName, rotationName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("material"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stub := newAgentStub(t, func(seq uint32) (int, *proto.HeartbeatResponse) {
		return 200, &proto.HeartbeatResponse{Bye: seq >= 1}
	})
	var drained, flushed atomic.Bool
	mach := state.New()
	_ = mach.Transition(state.Agent)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := RunAgentLoop(ctx, AgentDeps{
		Log:      testLogger(),
		Machine:  mach,
		Client:   &HeartbeatClient{Endpoint: stub.srv.URL, NodeID: [16]byte{4}},
		Sup:      NewSupervisor(testLogger()),
		StateDir: dir,
		Drain:    func() error { drained.Store(true); return nil },
		Flush:    func() error { flushed.Store(true); return nil },
	}); err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	for _, name := range []string{certFileName, SecretFileName, rotationName} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s sigue vivo tras el bye: %v", name, err)
		}
	}
	if !drained.Load() || !flushed.Load() {
		t.Fatal("bye deberia drenar y hacer el ultimo flush")
	}
	if mach.State() != state.Bye {
		t.Fatalf("estado = %v, want bye", mach.State())
	}
	// WipeKeys es idempotente: segunda pasada no es error.
	if err := WipeKeys(dir); err != nil {
		t.Fatalf("WipeKeys idempotente: %v", err)
	}
}

// T-0403 AC2 (bucle real): con la cadencia unica, cada 3.er tick porta
// el heartbeat completo y los otros dos son pings — ratio ~2:1 medido
// sobre los contadores del stub.
func TestCadenceLoopKeepsTwoToOneRatio(t *testing.T) {
	var mu sync.Mutex
	deadline := time.Now().Add(8 * time.Second)
	stub := newAgentStub(t, func(seq uint32) (int, *proto.HeartbeatResponse) {
		mu.Lock()
		defer mu.Unlock()
		if time.Now().After(deadline) {
			return 200, &proto.HeartbeatResponse{Bye: true}
		}
		return 200, nil
	})
	cad := tunnel.NewCadence("node-test")
	cad.Base = 20 * time.Millisecond
	cad.Jitter = 3 * time.Millisecond
	cad.Max = 30 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mach := state.New()
	_ = mach.Transition(state.Agent)
	if err := RunAgentLoop(ctx, AgentDeps{
		Log:     testLogger(),
		Machine: mach,
		Client:  &HeartbeatClient{Endpoint: stub.srv.URL, NodeID: [16]byte{5}},
		Sup:     NewSupervisor(testLogger()),
		Cadence: cad,
	}); err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	hb, ping := stub.hits.Load(), stub.pings.Load()
	if hb < 3 {
		t.Fatalf("heartbeats = %d, want >= 3 en la ventana", hb)
	}
	ratio := float64(ping) / float64(hb)
	if ratio < 1.5 || ratio > 2.6 {
		t.Fatalf("ratio pings:heartbeats = %.2f (%d/%d), want ~2:1", ratio, ping, hb)
	}
}
