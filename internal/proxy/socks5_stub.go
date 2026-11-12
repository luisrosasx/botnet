//go:build !socks5

package proxy

// Server es el proxy apagado: el binario se compilo sin el tag socks5.
// cmd/botd comprueba Start, que devuelve ErrDisabled, y sigue con el
// plano de control.
type Server struct{}

// Start informa de que no hay proxy embebido en este build.
func Start(cfg Config) (*Server, error) { return nil, ErrDisabled }

// Addr es vacio sin proxy.
func (s *Server) Addr() string { return "" }

// Loopback es false sin proxy: no hay socket escuchando en este build
// (la variante con tag socks5 confirma loopback sobre el listener).
func (s *Server) Loopback() bool { return false }

// StreamsOpen devuelve 0: sin SOCKS5 no hay streams de sesion.
func (s *Server) StreamsOpen() int64 { return 0 }

// Close es no-op.
func (s *Server) Close() error { return nil }

// Wait es no-op.
func (s *Server) Wait() {}
