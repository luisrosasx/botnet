package tunnel

import (
	"crypto/tls"
	"net"
	"os"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/state"
)

// AC1 (parte medible en host): ticket.bin + node_id.bin caben en <= 1 KiB
// con estado real (ticket TLS 1.3 de verdad) y el node_id de 16 B.
func TestTicketStoreBudgetWithRealTicket(t *testing.T) {
	st, err := NewTicketStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var id [NodeIDSize]byte
	for i := range id {
		id[i] = byte(i)
	}
	if err := st.SaveNodeID(id); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.LoadNodeID()
	if err != nil || !ok || got != id {
		t.Fatalf("node_id round-trip: ok=%v err=%v", ok, err)
	}

	cert := testTLSCert(t)
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ln := tls.NewListener(base, ServerTLS(&cert))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tc := conn.(*tls.Conn)
			_, _ = tc.Write([]byte("tick0000")) // flushea NewSessionTicket
			_ = tc.SetReadDeadline(time.Now().Add(2 * time.Second))
			b := make([]byte, 1)
			_, _ = tc.Read(b)
			_ = tc.Close()
		}
	}()

	// Handshake completo real: el ticket resultante se persiste en el
	// directorio de estado (es el mismo fichero que mide `wc -c`).
	c1, err := tls.Dial("tcp", ln.Addr().String(), ClientTLS("gw.tunnel.test", nil, st.SessionCache()))
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	_, _ = c1.Read(b)
	_ = c1.Close()

	total, err := st.StateBytes()
	if err != nil {
		t.Fatal(err)
	}
	if total > StateBudget {
		t.Fatalf("estado persistido %d B > presupuesto %d B", total, StateBudget)
	}
	if !st.HasTicket() {
		t.Fatal("el handshake completo debio dejar ticket en disco")
	}
	if st.FullHandshakeForRevival() {
		t.Fatal("con ticket, el revive no hace handshake completo")
	}

	// Degradacion controlada: el presupuesto se aplica a los writes
	// directos (SaveTicket); un ticket absurdo no revienta el flash.
	if err := st.SaveTicket(make([]byte, StateBudget)); err != state.ErrBudget {
		t.Fatalf("ticket de %d B: err=%v, want ErrBudget (16 B de node_id)", StateBudget, err)
	}
}

// AC1+AC3: con ticket valido, 10 "revives" = 0 handshakes completos
// (resumption PSK en todos); tras borrar el ticket a mano, el siguiente
// revive hace exactamente 1 handshake completo y FileSessionCache
// re-permite el ticket (degradacion controlada).
func TestRevivalZeroFullHandshakesWithTicket(t *testing.T) {
	st, err := NewTicketStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cert := testTLSCert(t)
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ln := tls.NewListener(base, ServerTLS(&cert))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			tc := conn.(*tls.Conn)
			_, _ = tc.Write([]byte("tick0000"))
			_ = tc.SetReadDeadline(time.Now().Add(2 * time.Second))
			b := make([]byte, 1)
			_, _ = tc.Read(b)
			_ = tc.Close()
		}
	}()
	addr := ln.Addr().String()
	path := st.Disk.Path(TicketFile)

	// arranque 0: handshake completo y ticket persistido.
	c, err := tls.Dial("tcp", addr, ClientTLS("gw.tunnel.test", nil, NewFileSessionCache(path)))
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 16)
	_, _ = c.Read(b)
	_ = c.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ticket no persistido: %v", err)
	}

	// 10 reinicios con cache nueva sobre el mismo fichero: 0 handshakes
	// completos.
	full := 0
	for i := 0; i < 10; i++ {
		c, err := tls.Dial("tcp", addr, ClientTLS("gw.tunnel.test", nil, NewFileSessionCache(path)))
		if err != nil {
			t.Fatalf("revive %d: %v", i, err)
		}
		if !c.ConnectionState().DidResume {
			full++
		}
		b := make([]byte, 16)
		_, _ = c.Read(b)
		_ = c.Close()
	}
	if full != 0 {
		t.Fatalf("handshakes completos con ticket valido: %d, want 0", full)
	}

	// Criterio 3: borrado manual => el siguiente revive hace 1 handshake
	// completo y reintenta persistir.
	if err := st.DeleteTicket(); err != nil {
		t.Fatal(err)
	}
	if !st.FullHandshakeForRevival() {
		t.Fatal("sin ticket: el revive debe planificar 1 handshake completo")
	}
	c, err = tls.Dial("tcp", addr, ClientTLS("gw.tunnel.test", nil, NewFileSessionCache(path)))
	if err != nil {
		t.Fatal(err)
	}
	if c.ConnectionState().DidResume {
		t.Fatal("sin ticket persistido el handshake debe ser completo")
	}
	b = make([]byte, 16)
	_, _ = c.Read(b)
	_ = c.Close()
	if !st.HasTicket() {
		t.Fatal("el revive con degradacion no re-permitio el ticket")
	}
	if st.FullHandshakeForRevival() {
		t.Fatal("tras re-permitir el ticket, el revive vuelve a ser PSK")
	}
}
