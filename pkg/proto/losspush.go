package proto

import "fmt"

// LossPush es el empuje del gateway hacia el nodo del loss_estimate
// medido (EP-09 T-0906, A2-02): viaja en el carril de control del
// heartbeat (cada 3.er frame), nunca abre conexiones nuevas. El nodo lo
// usa para decidir la segunda conexion H2 solo si la perdida medida
// supera el 1 % (la politica de T-0405 manda: en reposo sano hay 1).
type LossPush struct {
	NodeID          [16]byte
	Seq             uint32 // seq del heartbeat que lo origino
	LossEstimateBPS uint16 // puntos basicos medidos por el gateway (0..10000)
}

func (m *LossPush) Marshal() ([]byte, error) {
	e := &encoder{}
	e.u8(CodecVersion)
	e.raw(m.NodeID[:])
	e.u32(m.Seq)
	e.u16(m.LossEstimateBPS)
	return e.b, nil
}

func (m *LossPush) Unmarshal(data []byte) error {
	d := &decoder{b: data}
	if err := d.version(); err != nil {
		return fmt.Errorf("proto: LossPush: %w", err)
	}
	id, err := d.take(16)
	if err == nil {
		copy(m.NodeID[:], id)
	}
	if err == nil {
		m.Seq, err = d.u32()
	}
	if err == nil {
		m.LossEstimateBPS, err = d.u16()
	}
	if err != nil {
		return fmt.Errorf("proto: LossPush: %w", err)
	}
	if len(d.b)-d.pos != 0 {
		return fmt.Errorf("proto: LossPush: %d B sobrantes: %w", len(d.b)-d.pos, ErrInvalidLen)
	}
	return nil
}
