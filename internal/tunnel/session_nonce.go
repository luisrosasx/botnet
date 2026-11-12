package tunnel

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
)

// Nonce de sesion (T-0808, A3-08): el gateway lo emite en la asignacion
// y el nodo lo ecota en el OPEN_STREAM de cada stream. Un campo, una
// validacion, cero bytes cruzados: el aislamiento de streams deja de ser
// cortesía del nodo.

// ErrNonceMismatch: el echo del nodo no es el nonce emitido. El stream se
// rechaza y sessions.closed_reason queda 'error'.
var ErrNonceMismatch = errors.New("tunnel: nonce ecotado no calza")

// OpenStreamLen: session u64 + nonce u32, big-endian fijo.
const OpenStreamLen = 12

// OpenStream es el primer frame de un stream de sesion (payload de
// KindOpen bajo el framing del tunel).
type OpenStream struct {
	SessionID uint64
	Nonce     uint32
}

// IssueSessionNonce: 32 bits criptograficos. No hace falta mas: el
// reuso entre sesiones vivas del mismo nodo es cosmico y el espacio se
// renueva en cada asignacion.
func IssueSessionNonce() (uint32, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, errors.New("tunnel: nonce: " + err.Error())
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

// Marshal serializa el OPEN_STREAM canonico.
func (o OpenStream) Marshal() []byte {
	var b [OpenStreamLen]byte
	binary.BigEndian.PutUint64(b[:8], o.SessionID)
	binary.BigEndian.PutUint32(b[8:], o.Nonce)
	return b[:]
}

// UnmarshalOpenStream parsea el payload del primer frame del stream.
func UnmarshalOpenStream(p []byte) (OpenStream, error) {
	if len(p) != OpenStreamLen {
		return OpenStream{}, ErrFrameTooLarge
	}
	return OpenStream{
		SessionID: binary.BigEndian.Uint64(p[:8]),
		Nonce:     binary.BigEndian.Uint32(p[8:]),
	}, nil
}

// VerifyNonce compara emision y eco en tiempo constante por tamano fijo.
func VerifyNonce(issued, echoed uint32) error {
	if issued != echoed {
		return ErrNonceMismatch
	}
	return nil
}

// FirstFrameMustBeOpen es la regla de protocolo (T-0808 AC2): un edge
// falso que responda bytes sin prueba propia no recibe datos — el primer
// frame validado del stream es el OPEN (nonce), no un KindData.
func FirstFrameMustBeOpen(kinds []Kind) bool {
	if len(kinds) == 0 {
		return true // stream sin frames: nada que validar todavia
	}
	return kinds[0] == KindOpen
}
