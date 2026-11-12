//go:build socks5 && tcp

package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func proxySecret() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(i*7 + 3)
	}
	return s
}

func proxyIdentity(session string) SessionIdentity {
	return SessionIdentity{
		ClientID: "gw-client", TenantID: "tenant-" + session,
		SessionID: "sess-" + session, NodeID: "node-7",
		TS: time.Now().UTC(),
	}
}

func startTestProxy(t *testing.T, opener StreamOpener, tune func(*Config)) *Server {
	t.Helper()
	secret := proxySecret()
	cfg := Config{Addr: "127.0.0.1:0", Secret: secret, Opener: opener}
	if tune != nil {
		tune(&cfg)
	}
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
		s.Wait()
	})
	return s
}

// socksConnect habla el SOCKS5+RFC1929 cliente contra el proxy. Devuelve
// la respuesta CONNECT (10 B) o el momento del rechazo de auth (2 B +
// cierre). uname es el bot-session en el username.
func socksConnect(t *testing.T, addr, uname string, host string, port int) (net.Conn, []byte, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, nil, err
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte{socksVersion, 0x01, s5MethodUser}); err != nil {
		return conn, nil, err
	}
	gr := make([]byte, 2)
	if _, err := io.ReadFull(conn, gr); err != nil {
		return conn, nil, err
	}
	if gr[0] != socksVersion || gr[1] != s5MethodUser {
		return conn, nil, errors.New("proxy: seleccion de metodo inesperada")
	}
	auth := append([]byte{s5AuthVersion, byte(len(uname))}, uname...)
	auth = append(auth, 0x01, 'x') // password opaco de 1 B
	if _, err := conn.Write(auth); err != nil {
		return conn, nil, err
	}
	ar := make([]byte, 2)
	if _, err := io.ReadFull(conn, ar); err != nil {
		return conn, nil, err
	}
	if ar[1] == s5AuthReject {
		return conn, ar, errors.New("proxy: auth rechazada")
	}
	req := []byte{socksVersion, s5CmdConnect, 0x00, s5ATypDomain, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return conn, nil, err
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(conn, rep); err != nil {
		return conn, nil, err
	}
	return conn, rep, nil
}

// T-0601 AC1: el proxy vive en loopback del mismo proceso y el socket
// escuchado es loopback (doble del "ss -ltn sin puertos externos"); el
// echo de sesion funciona de punta a punta.
func TestSOCKS5LoopbackEcho(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	s := startTestProxy(t, opener, nil)
	if !s.Loopback() {
		t.Fatalf("listener en %s no es loopback", s.Addr())
	}
	tok := SignToken(proxySecret(), proxyIdentity("a"))
	conn, rep, err := socksConnect(t, s.Addr(), tok, "echo.example", 7)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if rep[1] != s5RepOK {
		t.Fatalf("respuesta CONNECT = %d, want 0", rep[1])
	}
	if _, err := conn.Write([]byte("hola-nodo")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 9)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hola-nodo" {
		t.Fatalf("eco = %q", buf)
	}
	if s.StreamsOpen() != 1 {
		t.Fatalf("streams abiertos = %d, want 1", s.StreamsOpen())
	}
	_ = conn.Close()

	// el nodo nunca escucha fuera: config no-loopback se rechaza.
	if _, err := Start(Config{Addr: "0.0.0.0:0", Secret: proxySecret(), Opener: opener}); !errors.Is(err, ErrNotLoopback) {
		t.Fatalf("Start en 0.0.0.0 = %v, want ErrNotLoopback", err)
	}
}

// T-0601 AC3: usuario malformado -> handshake rechazado sin abrir
// stream H2 (el contador del opener no sube).
func TestMalformedUserNoStream(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	s := startTestProxy(t, opener, nil)
	conn, ar, err := socksConnect(t, s.Addr(), "usuario-malformado", "echo.example", 7)
	if err == nil {
		t.Fatal("usuario malformado admitido")
	}
	if ar[1] != s5AuthReject {
		t.Fatalf("respuesta auth = %v, want [1 1]", ar)
	}
	_ = conn.Close()
	if opener.Opened.Load() != 0 {
		t.Fatalf("streams H2 abiertos = %d, want 0", opener.Opened.Load())
	}
}

// T-0602 AC2: ts de > 30 s -> 401 interno en el path de auth: rechazo
// sin stream.
func TestExpiredTokenNoStream(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	s := startTestProxy(t, opener, nil)
	stale := proxyIdentity("old")
	stale.TS = time.Now().UTC().Add(-40 * time.Second)
	tok := SignToken(proxySecret(), stale)
	conn, ar, err := socksConnect(t, s.Addr(), tok, "echo.example", 7)
	if err == nil {
		t.Fatal("token caducado admitido")
	}
	if ar[1] != s5AuthReject {
		t.Fatalf("respuesta auth = %v, want [1 1]", ar)
	}
	_ = conn.Close()
	if opener.Opened.Load() != 0 {
		t.Fatalf("streams H2 abiertos = %d, want 0", opener.Opened.Load())
	}
}

// T-0602 AC2: HMAC forjado -> stream abierto y cerrado al instante con
// 0 bytes transferidos.
func TestForgedTokenZeroBytes(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	s := startTestProxy(t, opener, nil)
	foreign := proxySecret()
	foreign[0] ^= 0xff
	tok := SignToken(foreign, proxyIdentity("f")) // firma ajena, forma valida
	conn, rep, err := socksConnect(t, s.Addr(), tok, "echo.example", 7)
	if err != nil {
		t.Fatalf("connect forjado: %v", err)
	}
	if rep[1] != s5RepOK {
		t.Fatalf("respuesta CONNECT = %d, want 0", rep[1])
	}
	// la conexion muere al instante: sin relay, 0 bytes
	if n, err := conn.Read(make([]byte, 1)); n != 0 && err == nil {
		t.Fatal("hubo bytes tras el cierre inmediato")
	}
	_ = conn.Close()
	if opener.Opened.Load() != 1 {
		t.Fatalf("streams abiertos = %d, want 1 (cerrado al instante)", opener.Opened.Load())
	}
	if opener.BytesSeen.Load() != 0 {
		t.Fatalf("bytes transferidos = %d, want 0", opener.BytesSeen.Load())
	}
}

// T-0601 AC1 (curl doble): GET real sobre el tunel -> 200. El opener
// dialis meta.Target() como haria el stream H2 del gateway.
func TestHTTPEndpointOverSocksReturns200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	p := uint16(mustAtoi(port))
	opener := OpenerFunc(func(ctx context.Context, meta StreamMeta) (io.ReadWriteCloser, error) {
		c, err := net.DialTimeout("tcp", meta.Target(), 2*time.Second)
		if err != nil {
			return nil, err
		}
		return c, nil
	})
	s := startTestProxy(t, opener, nil)
	tok := SignToken(proxySecret(), proxyIdentity("http"))
	conn, rep, err := socksConnect(t, s.Addr(), tok, host, int(p))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if rep[1] != s5RepOK {
		t.Fatalf("CONNECT = %d, want 0", rep[1])
	}
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus200(body) {
		t.Fatalf("respuesta:\n%s", body)
	}
}

func mustAtoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func hasStatus200(b []byte) bool {
	return len(b) >= 12 && string(b[:12]) == "HTTP/1.1 200"
}

// T-0607 AC2 (nivel SOCKS): dos sesiones de tenants distintos no se
// traspasan auth: cerrar A deja a B viva y ecos independientes por sesion.
func TestTwoSessionsNoCrossAuth(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	s := startTestProxy(t, opener, nil)
	tokA := SignToken(proxySecret(), proxyIdentity("A"))
	tokB := SignToken(proxySecret(), proxyIdentity("B"))
	connA, _, err := socksConnect(t, s.Addr(), tokA, "echo.example", 7)
	if err != nil {
		t.Fatal(err)
	}
	connB, _, err := socksConnect(t, s.Addr(), tokB, "echo.example", 7)
	if err != nil {
		t.Fatal(err)
	}
	echo := func(c net.Conn, msg string) {
		t.Helper()
		if _, err := c.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(msg))
		if _, err := io.ReadFull(c, buf); err != nil {
			t.Fatalf("eco %s: %v", msg, err)
		}
		if string(buf) != msg {
			t.Fatalf("eco = %q, want %q", buf, msg)
		}
	}
	echo(connA, "aaaa")
	echo(connB, "bbbb")
	_ = connA.Close()
	time.Sleep(100 * time.Millisecond)
	echo(connB, "cccc") // B sigue viva tras cerrar A
	_ = connB.Close()
}

// T-0603 AC2 (lado SOCKS): en modo solo-control el proxy rechaza
// sesiones nuevas sin tumbar el listener.
func TestControlOnlyRejectsNewSessions(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	sup := NewPanicBudget()
	s := startTestProxy(t, opener, func(c *Config) { c.Sup = sup })
	for i := 0; i < 3; i++ {
		sup.RegisterPanic()
	}
	tok := SignToken(proxySecret(), proxyIdentity("ctl"))
	conn, rep, err := socksConnect(t, s.Addr(), tok, "echo.example", 7)
	if err != nil {
		t.Logf("solo-control corto el handshake: %v", err)
	} else if rep[1] != s5RepFailed {
		t.Fatalf("respuesta en solo-control = %d, want 1 (fallo)", rep[1])
	}
	if conn != nil {
		_ = conn.Close()
	}
	if opener.Opened.Load() != 0 {
		t.Fatalf("abrio streams en solo-control: %d", opener.Opened.Load())
	}
}
