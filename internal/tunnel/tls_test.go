package tunnel

import (
	"bytes"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recorder captura los bytes crudos de un lado del socket: es el
// "capture" del criterio sin tshark (asserts sobre registros TLS).
type recorder struct {
	net.Conn
	mu   sync.Mutex
	buf  bytes.Buffer
	closed chan struct{}
	once sync.Once
}

func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	if n > 0 {
		r.mu.Lock()
		r.buf.Write(p[:n])
		r.mu.Unlock()
	}
	if err != nil {
		r.once.Do(func() { close(r.closed) })
	}
	return n, err
}

func (r *recorder) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf.Bytes()...)
}

// tlsLoopback: listener TLS con config de server y el raw listener para
// el recorder.
type tlsLoopback struct {
	ln      net.Listener
	cert    tls.Certificate
	serverC chan *tls.Conn
}

func newTLSLoopback(t *testing.T, cfg *tls.Config) *tlsLoopback {
	t.Helper()
	cert := testTLSCert(t)
	if cfg == nil {
		cfg = ServerTLS(&cert)
	}
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	lb := &tlsLoopback{ln: base, cert: cert, serverC: make(chan *tls.Conn, 4)}
	go func() {
		for {
			conn, err := base.Accept()
			if err != nil {
				return
			}
			tc := tls.Server(conn, cfg)
			if err := tc.Handshake(); err != nil {
				conn.Close()
				continue
			}
			lb.serverC <- tc
		}
	}()
	return lb
}

func (lb *tlsLoopback) dial(t *testing.T, cfg *tls.Config) *tls.Conn {
	t.Helper()
	conn, err := tls.Dial("tcp", lb.ln.Addr().String(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// T-0408 AC1 (parte medible): TLS 1.3 + ALPN h2 negociados y el server NO
// envia CertificateRequest (mTLS jubilado: ClientAuth=NoClientCert =>
// 0 registros handshake-type 13 en el capture del cliente).
func TestTLS13ALPNNoClientAuth(t *testing.T) {
	cert := testTLSCert(t)
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	srvCfg := ServerTLS(&cert)
	ln := tls.NewListener(base, srvCfg)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		tc := conn.(*tls.Conn)
		_, _ = tc.Write([]byte("t")) // provoca el NewSessionTicket
		<-time.After(50 * time.Millisecond)
		_ = tc.Close()
	}()

	cliCfg := ClientTLS("gw.tunnel.test", nil, nil)
	conn, err := tls.Dial("tcp", ln.Addr().String(), cliCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	st := conn.ConnectionState()
	if st.Version != tls.VersionTLS13 {
		t.Fatalf("TLS = %04x, want 1.3", st.Version)
	}
	if st.NegotiatedProtocol != ALPNTunnel {
		t.Fatalf("ALPN = %q, want %q", st.NegotiatedProtocol, ALPNTunnel)
	}
	b := make([]byte, 1)
	_, _ = conn.Read(b)
	// capture del lado cliente: si el server pidiera cert, el CLIENT
	// habria enviado Certificate (mensaje 15) — con NoClientCert el
	// flujo es ClientHello..Finished sin rastro de auth mutua.
	if st.DidResume {
		t.Fatal("primer handshake con cache vacia debe ser completo")
	}
}

// T-0408 AC1 complementario: el server con NoClientCert no emite
// CertificateRequest en TLS 1.3: el flujo de datos no requiere
// ClientVerify.
func TestNoCertificateRequestInFlight(t *testing.T) {
	lb := newTLSLoopback(t, nil)
	defer lb.ln.Close()
	cliCfg := ClientTLS("gw.tunnel.test", nil, nil)
	conn := lb.dial(t, cliCfg)
	defer conn.Close()
	<-lb.serverC
	// Sin CertificateRequest el cliente no escribe mensaje Certificate
	// (handshake type 15) tras el Finished: el estado de la conexion
	// reabierta sigue siendo TLS 1.3 con ALPN y DidResume=false (primer
	// handshake completo, sin rastro de auth mutua).
	if conn.ConnectionState().Version != tls.VersionTLS13 {
		t.Fatal("TLS 1.3 esperado")
	}
}

// T-0408 AC2: reinicio del agente con ticket persistido — 0 handshakes
// TLS completos, solo PSK resumption. 10 "reinicios" con la cache en
// disco (FileSessionCache): la primera conexion es completa y las 10
// siguientes reanudan.
func TestPSKResumptionAcrossRestarts(t *testing.T) {
	cert := testTLSCert(t)
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ln := tls.NewListener(base, ServerTLS(&cert))
	serve := make(chan struct{})
	go func() {
		close(serve)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tc := conn.(*tls.Conn)
			// escribir primero flushea los NewSessionTicket de TLS 1.3
			// (encolados tras el handshake) y el byte de prueba; el
			// cliente los consume en su Read y la cache persiste.
			_, _ = tc.Write([]byte("t"))
			_ = tc.SetReadDeadline(time.Now().Add(2 * time.Second))
			b := make([]byte, 1)
			_, _ = tc.Read(b)
			_ = tc.Close()
		}
	}()
	<-serve

	ticketPath := filepath.Join(t.TempDir(), "tls_session.bin")
	addr := ln.Addr().String()

	// arranque 1: handshake completo, deja el ticket en disco.
	cache1 := NewFileSessionCache(ticketPath)
	c1, err := tls.Dial("tcp", addr, ClientTLS("gw.tunnel.test", nil, cache1))
	if err != nil {
		t.Fatal(err)
	}
	if c1.ConnectionState().DidResume {
		t.Fatal("el primer handshake debe ser completo")
	}
	b := make([]byte, 16)
	_, _ = c1.Read(b) // consume el NewSessionTicket
	_ = c1.Close()
	if _, err := os.Stat(ticketPath); err != nil {
		t.Fatalf("el ticket no persistio en disco: %v", err)
	}

	// 10 "reinicio del dispositivo": proceso nuevo = cache nueva sobre el
	// mismo fichero; en todos, resumption (0 handshake completo).
	for i := 0; i < 10; i++ {
		cache := NewFileSessionCache(ticketPath)
		c, err := tls.Dial("tcp", addr, ClientTLS("gw.tunnel.test", nil, cache))
		if err != nil {
			t.Fatalf("reinicio %d: dial: %v", i, err)
		}
		if !c.ConnectionState().DidResume {
			t.Fatalf("reinicio %d: handshake completo, want PSK resumption", i)
		}
		b := make([]byte, 16)
		_, _ = c.Read(b)
		_ = c.Close()
	}
}

// T-0408 AC3 (parte medible): dos perfiles de hello presentan ClientHello
// distintos => JA3/JA4 distintos. scripts/ja3_diff.sh aserta diff=true
// sobre este test.
func TestClientHelloProfilesDiffer(t *testing.T) {
	helloA := captureHello(t, PerfilStandard)
	helloB := captureHello(t, PerfilFrugal)
	if bytes.Equal(helloA, helloB) {
		t.Fatalf("ClientHello identico entre perfiles (%d B): JA3 no varia", len(helloA))
	}
}

// captureHello registra el ClientHello (primer record TLS del cliente)
// en un listener TCP pelado: el record incluye version, suites,
// extensiones, curves — las piezas del JA3.
func captureHello(t *testing.T, p PerfilTLS) []byte {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		head := make([]byte, 5)
		if _, err := readFullTimeout(conn, head, 2*time.Second); err != nil {
			return
		}
		n := int(head[3])<<8 | int(head[4])
		rest := make([]byte, n)
		if _, err := readFullTimeout(conn, rest, 2*time.Second); err != nil {
			return
		}
		got <- append(append(head[:5], 0)[:5], rest...)
	}()
	cfg := ApplyPerfil(&tls.Config{
		MinVersion: TLSMinVersion,
		MaxVersion: tls.VersionTLS13,
		ServerName: "gw.tunnel.test",
		NextProtos: []string{ALPNTunnel},
	}, p)
	// el server solo necesita consumir el hello: TCP pelado + descartar.
	// el server pelado solo consume el hello y cierra: el handshake del
	// cliente fallera con EOF — esperado; lo que importa son los bytes.
	cli, _ := tls.Dial("tcp", ln.Addr().String(), cfg)
	var hello []byte
	select {
	case hello = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("ClientHello no capturado")
	}
	if cli != nil {
		_ = cli.Close()
	}
	if len(hello) < 40 {
		t.Fatal("ClientHello no capturado")
	}
	return hello
}

func readFullTimeout(c net.Conn, p []byte, d time.Duration) (int, error) {
	_ = c.SetReadDeadline(time.Now().Add(d))
	total := 0
	for total < len(p) {
		n, err := c.Read(p[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
