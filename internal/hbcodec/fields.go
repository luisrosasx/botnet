// fields.go — rotacion de campos por codec_ver (EP-11 T-1106, cierra
// A1-16): el catalogo auto-inventado del heartbeat deja de ser firma
// estable para el DPI. Cada `codec_ver` define su ORDEN de campos; el
// payload y la longitud total son los mismos, la posicion cambia.
// Compatibilidad de dos versiones: el descifrador acepta la version
// activa y la anterior (ventana de rodaje por config_rev sin
// reinicio del agente).
package hbcodec

import (
	"encoding/binary"
	"fmt"
)

// Version2: layout rotado. Subir la version aqui exige vector nuevo
// en testdata/codec_vectors/rotation.json.
const Version2 uint8 = 2

// MaxOldCodecVers: el descifrador sirve la version activa y una
// anterior (la ventana de rodaje del hot-swap).
const MaxOldCodecVers = 1

type fieldSpec struct {
	name string
	size int
}

// layoutFor es el orden del bloque numerico tras node16 para cada
// codec_ver. v1 es el orden historico de codec.go; v2 rota el bloque
// (uptime, streams, loss, rss, seq): dos frames del mismo payload con
// ver consecutivos difieren en hex desde el byte 18.
func layoutFor(ver uint8) ([]fieldSpec, error) {
	switch ver {
	case Version:
		return []fieldSpec{{"seq", 4}, {"rss", 4}, {"uptime", 4}, {"streams", 2}, {"loss", 2}}, nil
	case Version2:
		return []fieldSpec{{"uptime", 4}, {"streams", 2}, {"loss", 2}, {"rss", 4}, {"seq", 4}}, nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrVersion, ver)
	}
}

// CodecForConfigRev: el backend rueda el layout con config_rev
// creciente (hot-swap en caliente, mismo heartbeat). Rev 1 = v1;
// cada salto de rev mayor o igual que el umbral sube un ver; el
// agente NO se reinicia (codec.go sigue valiendo para ver-1).
func CodecForConfigRev(rev uint64) uint8 {
	if rev >= 2 {
		return Version2
	}
	return Version
}

// EncodeVersioned serializa con el layout del ver dado (mismo
// payload, distinta posicion — y con Pad nil, distinta longitud por
// relleno aleatorio como siempre).
func EncodeVersioned(h *Heartbeat, ver uint8) ([]byte, error) {
	if _, err := layoutFor(ver); err != nil {
		return nil, err
	}
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
	out = append(out, ver, KindHeartbeat)
	out = append(out, h.NodeID[:]...)
	// El orden del layout manda; los valores salen del struct.
	for _, spec := range mustLayout(ver) {
		switch spec.name {
		case "seq":
			out = binary.BigEndian.AppendUint32(out, h.Seq)
		case "rss":
			out = binary.BigEndian.AppendUint32(out, h.RssKiB)
		case "uptime":
			out = binary.BigEndian.AppendUint32(out, h.UptimeS)
		case "streams":
			out = binary.BigEndian.AppendUint16(out, h.Streams)
		case "loss":
			out = binary.BigEndian.AppendUint16(out, h.LossEstimate)
		}
	}
	out = append(out, uint8(len(h.AgentVer)))
	out = append(out, h.AgentVer...)
	out = append(out, uint8(len(pad)))
	out = append(out, pad...)
	if len(out) > MaxFrameLen {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooLong, len(out), MaxFrameLen)
	}
	return out, nil
}

// DecodeVersioned acepta la version activa y MaxOldCodecVers atras
// (ventana de dos versiones) y dispatchea por el primer byte.
func DecodeVersioned(h *Heartbeat, buf []byte) error {
	if len(buf) < coreLen+1 {
		return fmt.Errorf("%w: %d", ErrTruncated, len(buf))
	}
	ver := buf[0]
	layout, err := layoutFor(ver)
	if err != nil {
		return err
	}
	if ver < Version-MaxOldCodecVers || ver > Version2 {
		return fmt.Errorf("%w: fuera de ventana: %d", ErrVersion, ver)
	}
	if buf[1] != KindHeartbeat {
		return fmt.Errorf("%w: %d", ErrKind, buf[1])
	}
	if len(buf) > MaxFrameLen {
		return fmt.Errorf("%w: %d", ErrTooLong, len(buf))
	}
	copy(h.NodeID[:], buf[2:18])
	pos := 18
	for _, spec := range layout {
		switch spec.name {
		case "seq":
			h.Seq = binary.BigEndian.Uint32(buf[pos : pos+4])
		case "rss":
			h.RssKiB = binary.BigEndian.Uint32(buf[pos : pos+4])
		case "uptime":
			h.UptimeS = binary.BigEndian.Uint32(buf[pos : pos+4])
		case "streams":
			h.Streams = binary.BigEndian.Uint16(buf[pos : pos+2])
		case "loss":
			h.LossEstimate = binary.BigEndian.Uint16(buf[pos : pos+2])
		}
		pos += spec.size
	}
	vl := int(buf[pos])
	rest := buf[pos+1:]
	if vl > MaxAgentVer || len(rest) < vl+1 {
		return fmt.Errorf("%w: agent ver len", ErrTruncated)
	}
	h.AgentVer = string(rest[:vl])
	padLen := int(rest[vl])
	if len(rest) < vl+1+padLen || padLen > MaxPad {
		return fmt.Errorf("%w: pad len", ErrTruncated)
	}
	h.Pad = append([]byte(nil), rest[vl+1:vl+1+padLen]...)
	return nil
}

// helpers de layout (sin allocaciones en el hot path del tunel).
func mustLayout(ver uint8) []fieldSpec {
	l, err := layoutFor(ver)
	if err != nil {
		panic(err) // solo llama con veres conocidos
	}
	return l
}

func fieldSlice(_ *Heartbeat, ver uint8) []fieldSpec { return mustLayout(ver) }
