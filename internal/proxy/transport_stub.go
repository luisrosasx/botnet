//go:build !tcp

package proxy

import (
	"errors"
	"net"
)

// ErrNoTCPTransport: binario sin el tag tcp — el SOCKS5 del agente no
// tiene donde enlazar. El plano de control sigue vivo.
var ErrNoTCPTransport = errors.New("proxy: transporte tcp ausente (tag tcp no incluido)")

func listenTCP(addr string) (net.Listener, error) {
	return nil, ErrNoTCPTransport
}
