// Package hbcodec es el codec binario compacto del beacon del agente
// sobre el tunel (T-0404, BLUEPRINT §4-5, injerto P3): frame fijo con
// relleno aleatorio <= 55 B en el hueco de version. Prohibido JSON en el
// canal de control (mandato): el JSON de 550 B del plano F1 muere aqui y
// la longitud fija deja de ser firma para el DPI (A1-12).
//
// El layout es independiente de pkg/proto (que conserva su cable F1):
// este codec vive en el hot path del tunel y queda vectorizado para el
// fuzz de CI (testdata/codec_vectors/).
package hbcodec

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// Version del codec; cambiar el layout la sube (cambio de contrato).
	Version uint8 = 1

	// MaxPad: techo del relleno del hueco de version (0..55 B).
	MaxPad = 55
	// MaxAgentVer: longitud maxima de la cadena de version.
	MaxAgentVer = 16
	// MaxFrameLen: techo duro del frame completo (criterio: <= 255 B).
	MaxFrameLen = 255

	// MaxPingFiller: relleno aleatorio del keepalive parcial (0..8 B).
	MaxPingFiller = 8

	KindPing      uint8 = 1
	KindHeartbeat uint8 = 2
	KindCmdAck    uint8 = 3

	// coreLen: ver+kind+node16+seq4+rss4+uptime4+streams2+loss2+verlen1.
	coreLen = 1 + 1 + 16 + 4 + 4 + 4 + 2 + 2 + 1
	// pingLen: ver+kind+seq4.
	pingLen = 1 + 1 + 4
)

var (
	ErrVersion    = errors.New("hbcodec: unsupported version")
	ErrTruncated  = errors.New("hbcodec: truncated frame")
	ErrTooLong    = errors.New("hbcodec: frame too long")
	ErrKind       = errors.New("hbcodec: unknown frame kind")
	ErrInvalidLen = errors.New("hbcodec: invalid length")
)

// Heartbeat es la carga del latido completo. Pad es el relleno del hueco
// de version: nil al construir (Encode genera uno aleatorio); Decode lo
// preserva y Encode vuelve a emitir los mismos bytes (round-trip exacto,
// asercion de vectores AC1).
type Heartbeat struct {
	NodeID       [16]byte
	Seq          uint32
	RssKiB       uint32
	UptimeS      uint32
	Streams      uint16
	LossEstimate uint16 // puntos basicos (0..10000)
	AgentVer     string // <= 16 B
	Pad          []byte // <= 55 B
}

// Encode serializa el heartbeat. Con Pad == nil genera relleno aleatorio
// 0..55 B: dos encodes del mismo payload difieren en longitud (AC2:
// >= 3 longitudes en 100 encodes, max-min <= 55 B). Con Pad explicito el
// resultado es determinista (re-Marshal del decode).
func (h *Heartbeat) Encode() ([]byte, error) {
	if len(h.AgentVer) > MaxAgentVer {
		return nil, fmt.Errorf("%w: agent ver %d", ErrTooLong, len(h.AgentVer))
	}
	pad := h.Pad
	if pad == nil {
		pad = RandomPad()
	} else if len(pad) > MaxPad {
		return nil, fmt.Errorf("%w: pad %d", ErrTooLong, len(pad))
	}
	out := make([]byte, 0, coreLen+MaxAgentVer+1+len(pad))
	out = append(out, Version, KindHeartbeat)
	out = append(out, h.NodeID[:]...)
	out = binary.BigEndian.AppendUint32(out, h.Seq)
	out = binary.BigEndian.AppendUint32(out, h.RssKiB)
	out = binary.BigEndian.AppendUint32(out, h.UptimeS)
	out = binary.BigEndian.AppendUint16(out, h.Streams)
	out = binary.BigEndian.AppendUint16(out, h.LossEstimate)
	out = append(out, uint8(len(h.AgentVer)))
	out = append(out, h.AgentVer...)
	out = append(out, uint8(len(pad)))
	out = append(out, pad...)
	if len(out) > MaxFrameLen {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooLong, len(out), MaxFrameLen)
	}
	return out, nil
}

// Decode reconstruye el heartbeat preservando Pad para el round-trip.
func (h *Heartbeat) Decode(buf []byte) error {
	if len(buf) < coreLen+1 {
		return fmt.Errorf("%w: %d", ErrTruncated, len(buf))
	}
	if buf[0] != Version {
		return fmt.Errorf("%w: %d", ErrVersion, buf[0])
	}
	if buf[1] != KindHeartbeat {
		return fmt.Errorf("%w: %d", ErrKind, buf[1])
	}
	if len(buf) > MaxFrameLen {
		return fmt.Errorf("%w: %d", ErrTooLong, len(buf))
	}
	copy(h.NodeID[:], buf[2:18])
	h.Seq = binary.BigEndian.Uint32(buf[18:22])
	h.RssKiB = binary.BigEndian.Uint32(buf[22:26])
	h.UptimeS = binary.BigEndian.Uint32(buf[26:30])
	h.Streams = binary.BigEndian.Uint16(buf[30:32])
	h.LossEstimate = binary.BigEndian.Uint16(buf[32:34])
	vl := int(buf[34])
	rest := buf[coreLen:]
	if vl > MaxAgentVer || len(rest) < vl+1 {
		return fmt.Errorf("%w: ver %d tail %d", ErrInvalidLen, vl, len(rest))
	}
	h.AgentVer = string(rest[:vl])
	rest = rest[vl:]
	pl := int(rest[0])
	rest = rest[1:]
	if pl > MaxPad || len(rest) != pl {
		return fmt.Errorf("%w: pad %d tail %d", ErrInvalidLen, pl, len(rest))
	}
	if pl == 0 {
		h.Pad = []byte{}
	} else {
		h.Pad = append([]byte(nil), rest...)
	}
	return nil
}

// Ping serializa el keepalive parcial: ver+kind+seq+filler aleatorio
// 0..8 B. Viaja en los ticks 1 y 2 de cada trio (ratio 2:1, T-0403 AC2)
// y su filler mata la longitud fija tambien en el ping.
func Ping(seq uint32) []byte {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		b[0] = 3
	}
	return PingRand(seq, int(b[0])%int(MaxPingFiller+1))
}

// PingRand deja el filler explicito (los tests quedan deterministas).
func PingRand(seq uint32, filler int) []byte {
	if filler < 0 {
		filler = 0
	}
	if filler > MaxPingFiller {
		filler = MaxPingFiller
	}
	out := make([]byte, 0, pingLen+filler)
	out = append(out, Version, KindPing)
	out = binary.BigEndian.AppendUint32(out, seq)
	for i := 0; i < filler; i++ {
		out = append(out, byte(seq>>(8*(i%4)))^byte(i+0x5a))
	}
	return out
}

// DecodePing valida un ping y devuelve su seq.
func DecodePing(buf []byte) (uint32, error) {
	if len(buf) < pingLen {
		return 0, fmt.Errorf("%w: ping %d B", ErrTruncated, len(buf))
	}
	if buf[0] != Version {
		return 0, fmt.Errorf("%w: %d", ErrVersion, buf[0])
	}
	if buf[1] != KindPing {
		return 0, fmt.Errorf("%w: %d", ErrKind, buf[1])
	}
	return binary.BigEndian.Uint32(buf[2:6]), nil
}

// CmdAck serializa los ids de pending_cmds acked (T-0409). Formato:
// ver+kind+n u8+(len u8 + ascii){n}.
func CmdAck(ids []string) ([]byte, error) {
	if len(ids) > 32 {
		return nil, fmt.Errorf("%w: %d ids", ErrTooLong, len(ids))
	}
	out := []byte{Version, KindCmdAck, uint8(len(ids))}
	for _, id := range ids {
		if len(id) > 32 {
			return nil, fmt.Errorf("%w: id %q", ErrTooLong, id)
		}
		out = append(out, uint8(len(id)))
		out = append(out, id...)
	}
	return out, nil
}

// DecodeCmdAck valida y devuelve los ids.
func DecodeCmdAck(buf []byte) ([]string, error) {
	if len(buf) < 3 {
		return nil, fmt.Errorf("%w: cmdack %d B", ErrTruncated, len(buf))
	}
	if buf[0] != Version || buf[1] != KindCmdAck {
		return nil, fmt.Errorf("%w: kind %d", ErrKind, buf[1])
	}
	n := int(buf[2])
	ids := make([]string, 0, n)
	pos := 3
	for i := 0; i < n; i++ {
		if pos >= len(buf) {
			return nil, fmt.Errorf("%w: cmdack", ErrTruncated)
		}
		l := int(buf[pos])
		pos++
		if pos+l > len(buf) {
			return nil, fmt.Errorf("%w: cmdack id", ErrTruncated)
		}
		ids = append(ids, string(buf[pos:pos+l]))
		pos += l
	}
	return ids, nil
}

// RandomPad genera relleno 0..55 B con distribucion plana.
func RandomPad() []byte {
	var l [1]byte
	if _, err := rand.Read(l[:]); err != nil {
		l[0] = 7 // mejor relleno determinista que un latido sin relleno
	}
	n := int(l[0]) % (MaxPad + 1)
	if n == 0 {
		return []byte{}
	}
	p := make([]byte, n)
	if _, err := rand.Read(p); err != nil {
		for i := range p {
			p[i] = byte(i)
		}
	}
	return p
}
