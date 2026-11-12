package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Errores centinela del codec: los tests y los llamadores comparan con errors.Is.
var (
	ErrVersion    = errors.New("proto: unsupported codec version")
	ErrTruncated  = errors.New("proto: truncated message")
	ErrInvalidLen = errors.New("proto: invalid length")
)

type encoder struct{ b []byte }

func (e *encoder) u8(v uint8) { e.b = append(e.b, v) }

func (e *encoder) u16(v uint16) { e.b = binary.BigEndian.AppendUint16(e.b, v) }

func (e *encoder) u32(v uint32) { e.b = binary.BigEndian.AppendUint32(e.b, v) }

func (e *encoder) u64(v uint64) { e.b = binary.BigEndian.AppendUint64(e.b, v) }

func (e *encoder) raw(v []byte) { e.b = append(e.b, v...) }

// bytes serializa una longitud u16 seguida del contenido.
func (e *encoder) bytes(v []byte) {
	e.u16(uint16(len(v)))
	e.raw(v)
}

func (e *encoder) str(s string) { e.bytes([]byte(s)) }

// strs serializa un conteo u8 seguido de cada cadena.
func (e *encoder) strs(v []string) {
	e.u8(uint8(len(v)))
	for _, s := range v {
		e.str(s)
	}
}

type decoder struct {
	b   []byte
	pos int
}

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || d.pos+n > len(d.b) {
		return nil, ErrTruncated
	}
	v := d.b[d.pos : d.pos+n]
	d.pos += n
	return v, nil
}

func (d *decoder) version() error {
	v, err := d.take(1)
	if err != nil {
		return err
	}
	if v[0] != CodecVersion {
		return ErrVersion
	}
	return nil
}

func (d *decoder) u8() (uint8, error) {
	v, err := d.take(1)
	if err != nil {
		return 0, err
	}
	return v[0], nil
}

func (d *decoder) u16() (uint16, error) {
	v, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(v), nil
}

func (d *decoder) u32() (uint32, error) {
	v, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(v), nil
}

func (d *decoder) u64() (uint64, error) {
	v, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(v), nil
}

func (d *decoder) bytes() ([]byte, error) {
	n, err := d.u16()
	if err != nil {
		return nil, err
	}
	return d.take(int(n))
}

func (d *decoder) str() (string, error) {
	b, err := d.bytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (d *decoder) strs() ([]string, error) {
	n, err := d.u8()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for range n {
		s, err := d.str()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func tooLong(what string, got, max int) error {
	return fmt.Errorf("proto: %s length %d > %d: %w", what, got, max, ErrInvalidLen)
}
