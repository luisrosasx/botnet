package proto

import (
	"encoding/binary"
	"fmt"
)

// FrameType identifica la carga de un CtrlFrame. Estable para el cable:
// reordenar o borrar valores es un cambio de CodecVersion.
type FrameType uint8

const (
	FramePing      FrameType = iota + 1 // keepalive de aplicación (<45 s, §5)
	FrameHeartbeat                      // payload = Heartbeat.Marshal
	FrameHello                          // payload = HelloCaps.Marshal
	FrameRdvAssign                      // payload = RdvAssign.Marshal
	FrameSocksGreeting
	FrameSocksRequest
	FrameBye    // flag bye de baja por pull (§4, injerto P3)
	FrameCmdAck // ack de pending_cmds por id (T-0409); anadido al final, cable F1 intacto

	FrameLossPush // loss_estimate empujado gw→nodo (T-0906); anadido al final, cable F1 intacto
)

const (
	frameHeaderLen = 1 + 1 + 1 + 16 + 4 + 2 // ver+type+flags+node+seq+payloadLen
	// MaxFramePayload: techo de payload de control; el heartbeat completo
	// cabe en 256 B, el resto del plano de control es aún más frugal.
	MaxFramePayload = 4096
)

// CtrlFrame es el frame genérico del canal de control: una conexión larga
// saliente TLS/H2 (§5). El payload es el Marshal de un mensaje concreto.
type CtrlFrame struct {
	Type    FrameType
	Flags   uint8
	NodeID  [16]byte
	Seq     uint32
	Payload []byte
}

// EncodeFrame serializa el frame con la longitud de payload declarada en
// la cabecera; DecodeFrame exige que el buffer coincida exactamente.
func EncodeFrame(f *CtrlFrame) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("proto: EncodeFrame: nil frame: %w", ErrInvalidLen)
	}
	if len(f.Payload) > MaxFramePayload {
		return nil, tooLong("frame payload", len(f.Payload), MaxFramePayload)
	}
	out := make([]byte, 0, frameHeaderLen+len(f.Payload))
	out = append(out, CodecVersion, uint8(f.Type), f.Flags)
	out = append(out, f.NodeID[:]...)
	out = binary.BigEndian.AppendUint32(out, f.Seq)
	out = binary.BigEndian.AppendUint16(out, uint16(len(f.Payload)))
	out = append(out, f.Payload...)
	return out, nil
}

func DecodeFrame(buf []byte) (*CtrlFrame, error) {
	if len(buf) < frameHeaderLen {
		return nil, fmt.Errorf("proto: frame %d B < header %d: %w", len(buf), frameHeaderLen, ErrInvalidLen)
	}
	if buf[0] != CodecVersion {
		return nil, fmt.Errorf("proto: DecodeFrame: %w", ErrVersion)
	}
	pLen := int(binary.BigEndian.Uint16(buf[frameHeaderLen-2:]))
	if pLen > MaxFramePayload {
		return nil, tooLong("frame payload", pLen, MaxFramePayload)
	}
	if len(buf) != frameHeaderLen+pLen {
		return nil, fmt.Errorf("proto: frame %d B != %d + %d: %w", len(buf), frameHeaderLen, pLen, ErrInvalidLen)
	}
	f := &CtrlFrame{}
	f.Type = FrameType(buf[1])
	f.Flags = buf[2]
	copy(f.NodeID[:], buf[3:19])
	f.Seq = binary.BigEndian.Uint32(buf[19:23])
	f.Payload = append([]byte(nil), buf[frameHeaderLen:]...)
	return f, nil
}
