package proxy

import (
	"context"
	"io"
	"testing"
)

// La seam StreamOpener es el contrato con internal/tunnel (EP-04):
// OpenStream(ctx, StreamMeta) (io.ReadWriteCloser, error). Este test fija
// que el doble respeta la firma y el contrato: aperturas contabilizadas,
// cierre idempotente y Err propagate.
func TestStreamOpenerSeamContract(t *testing.T) {
	opener := &FakeOpener{Echo: true}
	s, err := opener.OpenStream(context.Background(), StreamMeta{SessionID: "s1", TenantID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if opener.Opened.Load() != 1 {
		t.Fatalf("Opened = %d, want 1", opener.Opened.Load())
	}
	if _, err := s.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(s, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "x" {
		t.Fatalf("eco = %q", buf)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close doble: %v", err) // idempotente
	}

	// OpenerFunc adapta la closure y el error del tunel se propaga.
	f := OpenerFunc(func(context.Context, StreamMeta) (io.ReadWriteCloser, error) {
		return nil, ErrNoTunnel
	})
	if _, err := f.OpenStream(context.Background(), StreamMeta{}); err != ErrNoTunnel {
		t.Fatalf("err = %v, want ErrNoTunnel", err)
	}

	// El adapter del *tunnel.Session.Open() (firma sin ctx/meta) embone
	// la seam: doble con la forma exacta de *tunnel.Stream.
	st := &tunnelStreamShaped{}
	var _ io.ReadWriteCloser = st // *Stream cumple ReadWriteCloser
	opener2 := NewOpenerFromSession(func() (io.ReadWriteCloser, error) { return st, nil })
	if _, err := opener2.OpenStream(context.Background(), StreamMeta{}); err != nil {
		t.Fatalf("adapter Session.Open: %v", err)
	}
}

// tunnelStreamShaped imita la superficie publica de *tunnel.Stream
// (internal/tunnel/stream.go): Read/Write/Close sin ctx ni meta.
type tunnelStreamShaped struct{ n int }

func (s *tunnelStreamShaped) Read(p []byte) (int, error)  { return 0, io.EOF }
func (s *tunnelStreamShaped) Write(p []byte) (int, error) { s.n += len(p); return len(p), nil }
func (s *tunnelStreamShaped) Close() error                { return nil }

// Config.Validate: loopback estricto y secreto de 32 B (T-0601 AC1).
func TestConfigValidate(t *testing.T) {
	opener := &FakeOpener{}
	if err := (Config{Secret: testSecret(), Opener: opener}).Validate(); err != nil {
		t.Fatalf("default loopback: %v", err)
	}
	if err := (Config{Addr: "10.13.14.1:1080", Secret: testSecret(), Opener: opener}).Validate(); err == nil {
		t.Fatal("acepto addr no-loopback")
	}
	if err := (Config{Addr: "localhost:1080", Secret: testSecret(), Opener: opener}).Validate(); err != nil {
		t.Fatalf("localhost: %v", err)
	}
	if err := (Config{Secret: []byte("corto"), Opener: opener}).Validate(); err == nil {
		t.Fatal("acepto secreto != 32 B")
	}
	if err := (Config{Secret: testSecret()}).Validate(); err == nil {
		t.Fatal("acepto opener nil")
	}
}
