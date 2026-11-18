package main

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// parSesion conecta cliente y servidor en memoria y devuelve ambos extremos.
// Con net.Pipe cada Write debe consumirse entero o se queda bloqueado: los
// casos mandan solo los bytes que el servidor va a leer.
func parSesion(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	cli, srv := net.Pipe()
	t.Cleanup(func() { cli.Close(); srv.Close() })
	return cli, srv
}

func verificarTodo(string, string) bool { return true }

// TestSocks5HandshakeVersion5: negociacion correcta con user/pass y CONNECT.
func TestSocks5HandshakeVersion5(t *testing.T) {
	cli, srv := parSesion(t)
	go AtenderHandshake(srv, verificarTodo)

	cli.Write([]byte{0x05, 0x01, 0x02})
	resp := make([]byte, 2)
	if _, err := io.ReadFull(cli, resp); err != nil {
		t.Fatalf("saludo: %v", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x02 {
		t.Fatalf("respuesta=%v (quiero 05 02)", resp)
	}
	cli.Write([]byte{0x01, 13})
	cli.Write([]byte("bot-session:x"))
	cli.Write([]byte{1, 's'})
	auth := make([]byte, 2)
	if _, err := io.ReadFull(cli, auth); err != nil {
		t.Fatalf("auth: %v", err)
	}
	if auth[1] != 0x00 {
		t.Fatalf("auth status=%v (quiero 00)", auth)
	}
	cli.Write([]byte{0x05, 0x01, 0x00, 0x01, 8, 8, 8, 8, 0, 53})
}

// TestSocks5HandshakeV4Rechazada: la version 4 se rechaza con ff.
func TestSocks5HandshakeV4Rechazada(t *testing.T) {
	cli, srv := parSesion(t)
	errCh := make(chan error, 1)
	go func() { _, _, err := AtenderHandshake(srv, verificarTodo); errCh <- err }()

	cli.Write([]byte{0x04, 0x01})
	buf := make([]byte, 2)
	if _, err := io.ReadFull(cli, buf); err != nil {
		t.Fatalf("respuesta: %v", err)
	}
	if buf[1] != 0xff {
		t.Fatalf("v4 debe rechazar con ff, dio %v", buf)
	}
	if err := <-errCh; err == nil {
		t.Fatal("v4 debe devolver error")
	}
}

// TestSocks5HandshakeMetodoNoSoportado: sin user/pass se responde 05 ff.
func TestSocks5HandshakeMetodoNoSoportado(t *testing.T) {
	cli, srv := parSesion(t)
	errCh := make(chan error, 1)
	go func() { _, _, err := AtenderHandshake(srv, verificarTodo); errCh <- err }()

	cli.Write([]byte{0x05, 0x01, 0x01}) // solo GSSAPI
	buf := make([]byte, 2)
	if _, err := io.ReadFull(cli, buf); err != nil {
		t.Fatalf("respuesta: %v", err)
	}
	if buf[0] != 0x05 || buf[1] != 0xff {
		t.Fatalf("respuesta=%v (quiero 05 ff)", buf)
	}
	if err := <-errCh; err == nil {
		t.Fatal("metodo no soportado debe devolver error")
	}
}

// TestRechazoNoAceptaCredencialMala: el rechazo ocurre antes de tocar nodo.
func TestRechazoNoAceptaCredencialMala(t *testing.T) {
	cli, srv := parSesion(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go atenderSesion(log, srv, func(u, s string) bool { return s == "buena" })

	cli.Write([]byte{0x05, 0x01, 0x02})
	io.ReadFull(cli, make([]byte, 2))
	cli.Write([]byte{0x01, 13})
	cli.Write([]byte("bot-session:x"))
	cli.Write([]byte{5})
	cli.Write([]byte("mala!"))
	buf := make([]byte, 2)
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(cli, buf); err != nil {
		t.Fatalf("auth: %v", err)
	}
	if buf[1] != 0x01 {
		t.Fatalf("credencial mala debe rechazarse con 01, dio %v", buf)
	}
}
