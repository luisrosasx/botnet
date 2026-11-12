package main

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/loss"
)

// S-05b/c: el empuje extremo a extremo. El gateway escribe frames en una
// conexión real y el consumidor del agente los aplica. Si alguien borra la
// llamada a MaybePush, este test se pone rojo: el empuje deja de ser
// decorativo.
func TestLossPushExtremoAExtremo(t *testing.T) {
	gwEnd, agentEnd := net.Pipe()
	defer gwEnd.Close()
	defer agentEnd.Close()

	consumer := agent.NewLossConsumer(1)
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := agentEnd.Read(buf)
			if err != nil {
				done <- nil
				return
			}
			if err := consumer.Ingest(buf[:n]); err != nil {
				done <- err
				return
			}
		}
	}()

	var id [16]byte
	for i := range id {
		id[i] = byte(i + 1)
	}
	p := LossPusher{NodeID: id}

	write := func(seq uint32, bps uint16) {
		t.Helper()
		_, err := p.MaybePush(gwEnd, seq, bps)
		if err != nil {
			t.Fatalf("seq %d: %v", seq, err)
		}
	}

	// Sólo los múltiplos de 3 empujan: los demás no mueven el objetivo.
	for seq := uint32(1); seq <= 2; seq++ {
		write(seq, 150)
	}
	time.Sleep(20 * time.Millisecond)
	if got := consumer.TargetConns(); got != 1 {
		t.Fatalf("secuencias 1-2 empujaron: objetivo %d", got)
	}

	write(3, 150) // pérdida alta → abre la segunda
	time.Sleep(20 * time.Millisecond)
	if got := consumer.TargetConns(); got != 2 {
		t.Fatalf("tras pérdida 150 bps objetivo %d, want 2", got)
	}

	write(6, 700) // pérdida severa → techo
	time.Sleep(20 * time.Millisecond)
	if got := consumer.TargetConns(); got != loss.MaxConnsPerNode {
		t.Fatalf("tras 700 bps objetivo %d, want %d", got, loss.MaxConnsPerNode)
	}

	write(9, 30) // sin pérdida y con margen → vuelve a 1
	time.Sleep(20 * time.Millisecond)
	if got := consumer.TargetConns(); got != 1 {
		t.Fatalf("tras 30 bps objetivo %d, want 1", got)
	}

	gwEnd.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("consumidor: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("el consumidor no terminó")
	}

	if _, err := p.MaybePush(io.Discard, 12, 500); err != nil {
		t.Fatalf("push a escritor nulo con secuencia válida: %v", err)
	}
	if ok, err := p.MaybePush(nil, 12, 500); ok || err != nil {
		t.Fatalf("escritor nil debe devolver false sin error: ok=%v err=%v", ok, err)
	}
}
