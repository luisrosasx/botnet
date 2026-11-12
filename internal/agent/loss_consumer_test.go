package agent

import (
	"testing"

	"github.com/botnet/botnet/internal/loss"
	"github.com/botnet/botnet/pkg/proto"
)

func lossFrame(t *testing.T, nodeID [16]byte, seq uint32, bps uint16) []byte {
	t.Helper()
	msg, err := (&proto.LossPush{NodeID: nodeID, Seq: seq, LossEstimateBPS: bps}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := proto.EncodeFrame(&proto.CtrlFrame{Type: proto.FrameLossPush, NodeID: nodeID, Seq: seq, Payload: msg})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func TestLossConsumerAplicaHisteresis(t *testing.T) {
	var id [16]byte
	c := NewLossConsumer(1)
	if c.TargetConns() != 1 {
		t.Fatalf("arranque = %d, want 1", c.TargetConns())
	}
	// Sube sin esperar.
	if err := c.Ingest(lossFrame(t, id, 3, 150)); err != nil {
		t.Fatal(err)
	}
	if got := c.TargetConns(); got != 2 {
		t.Fatalf("tras 150 bps = %d, want 2", got)
	}
	// Rozar el umbral hacia abajo no baja.
	if err := c.Ingest(lossFrame(t, id, 4, 99)); err != nil {
		t.Fatal(err)
	}
	if got := c.TargetConns(); got != 2 {
		t.Fatalf("con 99 bps = %d, want 2 (sin margen)", got)
	}
	// Margen suficiente: baja.
	if err := c.Ingest(lossFrame(t, id, 5, 40)); err != nil {
		t.Fatal(err)
	}
	if got := c.TargetConns(); got != 1 {
		t.Fatalf("con 40 bps = %d, want 1", got)
	}
	if c.Seen() != 3 || c.LastLossBPS() != 40 {
		t.Fatalf("contadores seen=%d last=%d", c.Seen(), c.LastLossBPS())
	}
}

func TestLossConsumerRespetaElTechoDelPerfil(t *testing.T) {
	var id [16]byte
	c := NewLossConsumer(loss.MaxConnsPerNode)
	if err := c.Ingest(lossFrame(t, id, 3, 5000)); err != nil {
		t.Fatal(err)
	}
	if got := c.TargetConns(); got != loss.MaxConnsPerNode {
		t.Fatalf("techo = %d, want %d", got, loss.MaxConnsPerNode)
	}
}

func TestLossConsumerRechazaFrameAjenoYBasura(t *testing.T) {
	var id [16]byte
	c := NewLossConsumer(1)
	ajaxo, err := proto.EncodeFrame(&proto.CtrlFrame{Type: proto.FrameHello, NodeID: id, Seq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ingest(ajaxo); err == nil {
		t.Fatal("un frame HELLO debe rechazarse")
	}
	if err := c.Ingest([]byte{9, 9, 9}); err == nil {
		t.Fatal("basura debe rechazarse")
	}
	if got := c.TargetConns(); got != 1 {
		t.Fatalf("el objetivo cambió con frames inválidos: %d", got)
	}
}

func TestNewLossConsumerSaneaElInicial(t *testing.T) {
	if got := NewLossConsumer(0).TargetConns(); got != 1 {
		t.Fatalf("inicial 0 = %d, want 1", got)
	}
	if got := NewLossConsumer(99).TargetConns(); got != loss.MaxConnsPerNode {
		t.Fatalf("inicial 99 = %d, want techo", got)
	}
}
