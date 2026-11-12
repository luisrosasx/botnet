package main

import (
	"github.com/botnet/botnet/internal/tunnel"
)

// Nonce de sesion en el gateway (T-0808): la asignacion emite un nonce
// y el nodo lo ecota en OPEN_STREAM; un eco incorrecto rechaza el stream
// con closed_reason='error' y un edge que responde datos antes que su
// prueba propia no consume bytes de tenant.

// closedReasonNonceError es el valor de sessions.closed_reason que fija
// el esquema cuando el eco del nonce falla.
const closedReasonNonceError = "error"

// NonceRegistry lleva el nonce emitido por sesion hasta el OPEN_STREAM.
type NonceRegistry struct {
	issued map[uint64]uint32
}

func NewNonceRegistry() *NonceRegistry {
	return &NonceRegistry{issued: map[uint64]uint32{}}
}

// Issue registra el nonce de la asignacion (lo que devolvio
// gw.{gw_id}.alloc en el 201 del cliente).
func (r *NonceRegistry) Issue(sessionID uint64, nonce uint32) {
	r.issued[sessionID] = nonce
}

// HandleOpen valida el OPEN_STREAM: si el nodo ecota mal (fixture:
// nonce-1), el stream se rechaza y el registro se limpia.
func (r *NonceRegistry) HandleOpen(open tunnel.OpenStream) (closedReason string, ok bool) {
	issued, known := r.issued[open.SessionID]
	if !known {
		return "error", false
	}
	if err := tunnel.VerifyNonce(issued, open.Nonce); err != nil {
		delete(r.issued, open.SessionID)
		return closedReasonNonceError, false
	}
	return "", true
}

// AllowData es la puerta del primer dato (AC2): sin OPEN validado, los
// bytes del edge falso se descartan — el primer frame validado es el
// nonce, no los datos.
func AllowData(openAccepted bool) bool { return openAccepted }
