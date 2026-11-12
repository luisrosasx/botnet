package agent

import (
	"fmt"
	"sync"

	"github.com/botnet/botnet/internal/loss"
	"github.com/botnet/botnet/pkg/proto"
)

// LossConsumer aplica el empuje de pérdida que manda el gateway por el
// canal de control (T-0906 AC3, cierra A2-02). Antes de este consumidor
// el frame se construía en el gateway y no lo leía nadie: el nodo nunca
// adaptaba sus conexiones (hallazgo H1).
//
// La histéresis vive en internal/loss.Apply: subir es inmediato, bajar
// exige margen. El consumidor solo guarda el objetivo; quien abre
// conexiones consulta TargetConns().
type LossConsumer struct {
	mu     sync.Mutex
	target int
	last   uint16
	seen   uint64
}

// NewLossConsumer arranca con el objetivo por defecto (1 conexión: reposo
// sano, T-0405).
func NewLossConsumer(initial int) *LossConsumer {
	if initial < 1 {
		initial = 1
	}
	if initial > loss.MaxConnsPerNode {
		initial = loss.MaxConnsPerNode
	}
	return &LossConsumer{target: initial}
}

// Ingest decodifica un CtrlFrame de FrameLossPush y ajusta el objetivo.
// Un frame de otro tipo o un payload corrupto no cambian nada.
func (c *LossConsumer) Ingest(wire []byte) error {
	f, err := proto.DecodeFrame(wire)
	if err != nil {
		return err
	}
	if f.Type != proto.FrameLossPush {
		return fmt.Errorf("agent: perdida: frame tipo %v: %w", f.Type, proto.ErrInvalidLen)
	}
	var msg proto.LossPush
	if err := msg.Unmarshal(f.Payload); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.target = loss.Apply(c.target, msg.LossEstimateBPS)
	c.last = msg.LossEstimateBPS
	c.seen++
	return nil
}

// TargetConns es el objetivo vigente de conexiones H2 del nodo.
func (c *LossConsumer) TargetConns() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.target
}

// LastLossBPS y Seen son para el heartbeat y los asserts.
func (c *LossConsumer) LastLossBPS() uint16 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func (c *LossConsumer) Seen() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen
}
