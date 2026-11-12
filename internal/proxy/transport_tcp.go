//go:build tcp

package proxy

import "net"

// listenTCP enlaza el listener del proxy SOCKS5. El tag tcp fija el
// transporte del plano de datos: el binario de flota se compila con
// tags socks5+tcp (T-0601 AC2).
func listenTCP(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}
