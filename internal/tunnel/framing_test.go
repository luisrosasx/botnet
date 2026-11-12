package tunnel

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- helpers de prueba -------------------------------------------------

func testTLSCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gw.tunnel.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"gw.tunnel.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// testServer levanta el eco TLS sobre loopback y cuenta conexiones
// aceptadas: el "ss -i 'dport :443' | grep -c ESTAB" medible en test.
func testServer(t *testing.T, cert tls.Certificate) (net.Listener, *atomic.Uint64) {
	t.Helper()
	var conns atomic.Uint64
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := tls.NewListener(base, ServerTLS(&cert))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns.Add(1)
			tlsConn := conn.(*tls.Conn)
			if err := tlsConn.Handshake(); err != nil {
				_ = conn.Close()
				continue
			}
			sess := NewSession(conn)
			go func() { _ = sess.ServeEcho() }()
		}
	}()
	return ln, &conns
}

func testClient(t *testing.T, addr string) (*tls.Conn, *Session) {
	t.Helper()
	cfg := ClientTLS("gw.tunnel.test", nil, nil)
	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if conn.ConnectionState().NegotiatedProtocol != ALPNTunnel {
		t.Fatalf("ALPN = %q, want %q", conn.ConnectionState().NegotiatedProtocol, ALPNTunnel)
	}
	return conn, NewSession(conn)
}

// --- T-0401 AC1: loopback de 50 streams (eco 10 MB) sobre UNA TLS/443 --

func TestLoopback50StreamsOneConnection(t *testing.T) {
	cert := testTLSCert(t)
	ln, conns := testServer(t, cert)

	_, sess := testClient(t, ln.Addr().String())

	const streams, chunk, chunksPer = 50, 16384, 13 // 50*13*16 KiB ~ 10 MB
	sts := make([]*Stream, streams)
	var openErr error
	for i := range sts {
		if sts[i], openErr = sess.Open(); openErr != nil {
			t.Fatal(openErr)
		}
	}

	var wg sync.WaitGroup
	wg.Add(streams)
	var fail atomic.Value
	fail.Store("")
	payload := make([]byte, chunk)
	for i := range payload {
		payload[i] = byte(i)
	}
	// lectores primero: cada stream espera su eco exacto.
	for i := range sts {
		go func(i int, st *Stream) {
			defer wg.Done()
			buf := make([]byte, chunk)
			for n := 0; n < chunksPer; n++ {
				if _, err := io.ReadFull(st, buf); err != nil {
					fail.Store(fmtLike("read %d: %v", st.ID(), err))
					return
				}
				for k := range buf {
					if buf[k] != payload[k] {
						fail.Store(fmtLike("stream %d payload corrupto en %d", st.ID(), k))
						return
					}
				}
			}
		}(i, sts[i])
	}
	for i := range sts {
		go func(st *Stream) {
			for n := 0; n < chunksPer; n++ {
				if _, err := st.Write(payload); err != nil {
					fail.Store(fmtLike("write %d: %v", st.ID(), err))
					return
				}
			}
			_ = st.CloseWrite()
		}(sts[i])
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("eco de 10 MB no termino (hang); estado: %v", fail.Load())
	}
	if msg, _ := fail.Load().(string); msg != "" {
		t.Fatal(msg)
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("conexiones TCP aceptadas: %d, want 1 (ss -i dport :443 = 1 ESTAB)", n)
	}
	if c := sess.Corrupt.Load(); c != 0 {
		t.Fatalf("frames con CRC invalido: %d, want 0", c)
	}
	_ = sess.Close()
	_ = ln.Close()
}

// fmtLike formatea mensajes de fallo sin propagar panicos del test.
func fmtLike(format string, a ...any) string {
	return fmt.Sprintf(format, a...)
}

// --- T-0401 AC2: corpus + allocas del hot path --------------------------

func readCorpus(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", "*.hex"))
	if err != nil || len(files) == 0 {
		t.Fatalf("corpus testdata/corpus/*.hex: %v (%d ficheros)", err, len(files))
	}
	out := map[string][]byte{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		hx := bytes.TrimSpace(raw)
		b, err := hex.DecodeString(string(hx))
		if err != nil {
			t.Fatalf("%s no es hex: %v", filepath.Base(f), err)
		}
		out[filepath.Base(f)] = b
	}
	if len(out) < 6 {
		t.Fatalf("corpus con %d ficheros, want >= 6", len(out))
	}
	return out
}

func TestFramingVectors(t *testing.T) {
	corpus := readCorpus(t)
	for name, buf := range corpus {
		// el corpus completo y todos sus prefijos truncados deben decodificar
		// sin panico (el fuzz de EP-12 reusa este corpus).
		for cut := 0; cut <= len(buf); cut++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s[%d]: panico %v", name, cut, r)
					}
				}()
				fr := NewFrameReader(bytes.NewReader(buf[:cut]), 1<<15)
				for {
					if _, _, err := fr.ReadFrame(); err != nil {
						break
					}
				}
			}()
		}
	}
	// los frames validos del corpus decodifican al menos una vez.
	for _, name := range []string{"ping.hex", "open.hex", "zero.hex", "heartbeat.hex", "data16.hex"} {
		buf := corpus[name]
		fr := NewFrameReader(bytes.NewReader(buf), 1<<15)
		if _, _, err := fr.ReadFrame(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// BenchmarkFrameHotPath mide el decode con reader y buffer reutilizados:
// el hot path real del readLoop.
func BenchmarkFrameHotPath(b *testing.B) {
	frame, err := WriteFrameTo([]byte{0, 0, 0, 7}, Header{Kind: KindPing})
	if err != nil {
		b.Fatal(err)
	}
	fr := NewFrameReader(bytes.NewReader(frame), 4096)
	br := bytes.NewReader(frame)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		br.Reset(frame)
		fr.r = br
		if _, _, err := fr.ReadFrame(); err != nil {
			b.Fatal(err)
		}
	}
}

// TestFrameHotPathAllocs: asercion del criterio 2 — allocs/op <= 2.
func TestFrameHotPathAllocs(t *testing.T) {
	res := testing.Benchmark(BenchmarkFrameHotPath)
	if a := res.AllocsPerOp(); a > 2 {
		t.Fatalf("allocs/op = %d, want <= 2 en el hot path del frame", a)
	}
}

// --- integridad: un bit alterado se detecta (checksum por frame) --------

func TestFrameChecksumDetection(t *testing.T) {
	frame, err := WriteFrameTo([]byte("hola tunel"), Header{Kind: KindData, Stream: 3})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), frame...)
	corrupt[len(corrupt)-1] ^= 0x01 // un bit en el payload
	fr := NewFrameReader(bytes.NewReader(corrupt), 4096)
	_, _, err = fr.ReadFrame()
	if !errors.Is(err, ErrFrameChecksum) {
		t.Fatalf("err = %v, want ErrFrameChecksum", err)
	}
	if fr.CorruptCount() != 1 {
		t.Fatalf("CorruptCount = %d, want 1", fr.CorruptCount())
	}
}

// --- T-0401 AC3: kill del peer -> re-abrir < 5 s, 0 hangs ---------------

func TestKillPeerReopenStreams(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	reg := NewRegistry()
	const stickyN, volatileN = 25, 25
	for i := 0; i < stickyN; i++ {
		reg.Register(uint32(2*i+1), true)
	}
	for i := 0; i < volatileN; i++ {
		reg.Register(uint32(2*stickyN+2*i+1), false)
	}

	start := time.Now()
	killed := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s := NewSession(conn)
		<-time.After(50 * time.Millisecond)
		_ = s.Conn.Close() // kill a mitad de stream
		close(killed)
	}()

	first, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	firstSess := NewSession(first)
	<-killed
	<-firstSess.Done() // el peer murio; sin hangs: Done cerro

	// re-apertura con backoff ± jitter (cota contractual < 5 s)
	bo := NewBackoff()
	bo.Base = 100 * time.Millisecond
	bo.Cap = 2 * time.Second
	var second net.Conn
	for {
		second, err = net.Dial("tcp", ln.Addr().String())
		if err == nil {
			break
		}
		time.Sleep(bo.Next())
		if time.Since(start) > 5*time.Second {
			t.Fatal("re-conexion > 5 s")
		}
	}
	recovered := time.Since(start)
	secondSess := NewSession(second)
	reatt, lost := reg.Restore(secondSess)
	if len(reatt) != stickyN {
		t.Fatalf("re-anudados %d, want %d", len(reatt), stickyN)
	}
	if len(lost) != volatileN {
		t.Fatalf("perdidos %d, want %d", len(lost), volatileN)
	}
	for _, err := range lost {
		if !errors.Is(err, ErrStreamGone) {
			t.Fatalf("error de stream perdido = %v, want ErrStreamGone", err)
		}
	}
	if recovered > 5*time.Second {
		t.Fatalf("re-conexion en %v, want < 5 s", recovered)
	}
	_ = secondSess.Close()
}
