package tunnel

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// Framing propio del tunel (BLUEPRINT §5): cabecera fija de 14 B con CRC32
// por frame, verificado en el receptor. Es la capa bajo la que viajan el
// control (ping/heartbeat) y los streams de sesion sobre UNA conexion TLS
// con ALPN h2 — el "H2 propio" pagado con puertas de seccion 12.
//
// Layout: [ver u8][kind u8][flags u8][stream u32][len u24][crc32 u32][payload]
// crc32 (IEEE) cubre desde kind hasta el payload: un bit alterado se
// detecta antes de entregar el chunk al stream.

const (
	// FrameVersion unica cabecera valida; cambiar el layout sube esta
	// constante y es un cambio de contrato (nunca reordenar en caliente).
	FrameVersion uint8 = 1

	frameHeaderLen = 14

	// MaxFramePayload: 16 KiB — coherente con los buffers fijos de 8 KiB
	// del plano de datos (§7); el control nunca pasa de 4 KiB.
	MaxFramePayload = 1 << 14
)

// Kind identifica la carga del frame. Anadir al final es compatible;
// reordenar rompe el cable.
type Kind uint8

const (
	KindPing      Kind = iota + 1 // keepalive de aplicacion (<45 s, T-0403)
	KindHeartbeat                 // payload = hbcodec/Heartbeat o proto.Heartbeat
	KindCmdAck                    // acks de pending_cmds (T-0409)
	KindOpen                      // apertura de stream (id en header.Stream)
	KindData                      // chunk de stream; payload vacio = cero-length
	KindClose                     // half-close del stream
	KindGoAway                    // contraccion del mux: cierra sin matar streams (T-0405)
)

var (
	ErrFrameVersion   = errors.New("tunnel: frame version incompatible")
	ErrFrameChecksum  = errors.New("tunnel: frame checksum mismatch")
	ErrFrameTooLarge  = errors.New("tunnel: frame payload too large")
	ErrFrameTruncated = errors.New("tunnel: frame truncated")
	ErrFrameCorrupted = errors.New("tunnel: corrupted frame stream")
)

// Header es la cabecera decodificada; viaja por valor para no asignar.
type Header struct {
	Kind   Kind
	Flags  uint8
	Stream uint32
	Len    uint32
}

// WriteFrame serializa cabecera+payload con el CRC32 del payload.
// Dos escrituras sobre el writer (buffered por encima); hot path sin
// asignaciones mas alla del payload que pasa el llamante.
func WriteFrame(w io.Writer, h Header, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, len(payload), MaxFramePayload)
	}
	var hdr [frameHeaderLen]byte
	hdr[0] = FrameVersion
	hdr[1] = byte(h.Kind)
	hdr[2] = h.Flags
	binary.BigEndian.PutUint32(hdr[3:7], h.Stream)
	l := uint32(len(payload))
	hdr[7] = byte(l >> 16)
	hdr[8] = byte(l >> 8)
	hdr[9] = byte(l)
	crc := crc32.Update(0, crc32.IEEETable, hdr[1:10])
	crc = crc32.Update(crc, crc32.IEEETable, payload)
	binary.BigEndian.PutUint32(hdr[10:14], crc)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if l > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// WriteFrameTo serializa el frame en una slice nueva (fixtures, helpers y
// tests; el hot path sobre la conexion usa WriteFrame).
func WriteFrameTo(payload []byte, h Header) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, h, payload); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// FrameReader decodifica frames reutilizando un unico buffer: el payload
// devuelto es un slice valido hasta la siguiente ReadFrame (cero
// asignaciones en el hot path, asercion T-0401 AC2).
type FrameReader struct {
	r    io.Reader
	hdr  [frameHeaderLen]byte
	buf  []byte
	crc  uint32
	fail uint64 // frames con CRC invalido (integridad medida en receptor)
}

// NewFrameReader crea el lector con buffer de maxPayload (0 = default).
func NewFrameReader(r io.Reader, maxPayload int) *FrameReader {
	if maxPayload <= 0 {
		maxPayload = MaxFramePayload
	}
	return &FrameReader{r: r, buf: make([]byte, maxPayload)}
}

// CorruptCount son los frames con CRC invalido detectados (el receptor
// los descarta y sigue; 0 esperado en trafico integro).
func (fr *FrameReader) CorruptCount() uint64 { return fr.fail }

// ReadFrame lee la cabecera, valida version y tamano, y deja el payload
// en fr.Buf. El payload de longitud 0 es un caso valido (corpus T-0401).
func (fr *FrameReader) ReadFrame() (Header, []byte, error) {
	if _, err := io.ReadFull(fr.r, fr.hdr[:]); err != nil {
		return Header{}, nil, err
	}
	if fr.hdr[0] != FrameVersion {
		return Header{}, nil, fmt.Errorf("%w: %d", ErrFrameVersion, fr.hdr[0])
	}
	var h Header
	h.Kind = Kind(fr.hdr[1])
	h.Flags = fr.hdr[2]
	h.Stream = binary.BigEndian.Uint32(fr.hdr[3:7])
	h.Len = uint32(fr.hdr[7])<<16 | uint32(fr.hdr[8])<<8 | uint32(fr.hdr[9])
	if h.Len > MaxFramePayload {
		return Header{}, nil, fmt.Errorf("%w: %d", ErrFrameTooLarge, h.Len)
	}
	if int(h.Len) > len(fr.buf) {
		return Header{}, nil, fmt.Errorf("%w: buffer %d < %d", ErrFrameTooLarge, len(fr.buf), h.Len)
	}
	var payload []byte
	if h.Len > 0 {
		if _, err := io.ReadFull(fr.r, fr.buf[:h.Len]); err != nil {
			return Header{}, nil, err
		}
		payload = fr.buf[:h.Len]
	}
	want := binary.BigEndian.Uint32(fr.hdr[10:14])
	crc := crc32.Update(0, crc32.IEEETable, fr.hdr[1:10])
	crc = crc32.Update(crc, crc32.IEEETable, payload)
	if crc != want {
		fr.fail++
		return Header{}, payload, fmt.Errorf("%w: got %08x want %08x", ErrFrameChecksum, crc, want)
	}
	return h, payload, nil
}

// Buf es el buffer interno del lector (alias del payload devuelto).
func (fr *FrameReader) Buf() []byte { return fr.buf }
