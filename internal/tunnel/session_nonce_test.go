package tunnel

import (
	"errors"
	"io"
	"math"
	"net"
	"testing"
)

func TestNonceRoundTripAndIssue(t *testing.T) {
	n1, err := IssueSessionNonce()
	if err != nil {
		t.Fatalf("IssueSessionNonce: %v", err)
	}
	n2, err := IssueSessionNonce()
	if err != nil {
		t.Fatalf("IssueSessionNonce 2: %v", err)
	}
	if n1 == n2 {
		t.Errorf("dos nonces seguidos iguales: %d", n1)
	}
	os := OpenStream{SessionID: 42, Nonce: n1}
	open, err := UnmarshalOpenStream(os.Marshal())
	if err != nil {
		t.Fatalf("UnmarshalOpenStream: %v", err)
	}
	if open != os {
		t.Errorf("round trip: quiero %+v, tengo %+v", os, open)
	}
	if _, err := UnmarshalOpenStream([]byte{1, 2, 3}); err == nil {
		t.Error("payload corto debe fallar")
	}
}

// AC1: el fixture del nodo con bug que ecota nonce-1 se rechaza con
// ErrNonceMismatch (el stream muere con closed_reason='error').
func TestNonceEchoBug(t *testing.T) {
	issued, err := IssueSessionNonce()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyNonce(issued, issued); err != nil {
		t.Errorf("echo correcto: %v", err)
	}
	if err := VerifyNonce(issued, issued-1); err != ErrNonceMismatch {
		t.Errorf("nodo con bug nonce-1: quiero ErrNonceMismatch, tengo %v", err)
	}
}

// AC2: el primer frame validado es el nonce, no datos; un edge falso que
// responde sin prueba propia no recibe bytes de tenant.
func TestFirstFrameMustBeOpen(t *testing.T) {
	if FirstFrameMustBeOpen([]Kind{KindData}) {
		t.Error("KindData primero: el edge falso recibio bytes sin prueba propia")
	}
	if !FirstFrameMustBeOpen([]Kind{KindOpen, KindData}) {
		t.Error("OPEN primero debe pasar")
	}
	if !FirstFrameMustBeOpen(nil) {
		t.Error("stream vacio: nada que validar, debe pasar")
	}
}

// AC3: dos tenants sobre el mismo nodo — el demux valida el nonce por
// stream y suma los bytes por session_id sin cruzarlos (±0,5 %). Los
// bytes fluyen de verdad por UNA sola conexion (net.Pipe): el remitente
// serializa con WriteFrame y el lector re-demuxea con FrameReader por
// Header.Stream; cada payload lleva la firma byte a byte de su tenant,
// asi que un cruce se pilla aunque los totales cuadren. Las sumas de
// remitente y receptor se contrastan cruzadamente y el desvio se mide
// contra la tolerancia de producto (±0,5 %).
func TestTwoTenantsNoCrossedBytes(t *testing.T) {
	s1, s2 := uint32(11), uint32(22)
	issued := map[uint32]uint32{}
	for _, sid := range []uint32{s1, s2} {
		n, err := IssueSessionNonce()
		if err != nil {
			t.Fatal(err)
		}
		issued[sid] = n
	}
	chunk := func(sig byte, n int) []byte {
		p := make([]byte, n)
		for i := range p {
			p[i] = sig
		}
		return p
	}
	sign := map[uint32]byte{s1: 0xAA, s2: 0xBB}
	// frames entrelazados: OPEN(s1) OPEN(s2) DATA(s1)... DATA(s2)...
	frames := []struct {
		kind    Kind
		stream  uint32
		payload []byte
	}{
		{KindOpen, s1, OpenStream{SessionID: uint64(s1), Nonce: issued[s1]}.Marshal()},
		{KindOpen, s2, OpenStream{SessionID: uint64(s2), Nonce: issued[s2]}.Marshal()},
		{KindData, s1, chunk(0xAA, 1500)},
		{KindData, s2, chunk(0xBB, 900)},
		{KindData, s1, chunk(0xAA, 1500)},
		{KindData, s2, chunk(0xBB, 900)},
		{KindData, s1, chunk(0xAA, 100)},
	}
	want := map[uint32]int{s1: 3100, s2: 1800}
	sent := map[uint32]int{}

	client, server := net.Pipe()
	werr := make(chan error, 1)
	go func() {
		for _, f := range frames {
			if f.kind == KindData {
				sent[f.stream] += len(f.payload)
			}
			if err := WriteFrame(client, Header{Kind: f.kind, Stream: f.stream}, f.payload); err != nil {
				werr <- err
				client.Close()
				return
			}
		}
		werr <- nil
		client.Close() // EOF: el lector cierra el bucle
	}()

	fr := NewFrameReader(server, MaxFramePayload)
	got := map[uint32]int{}
	for {
		h, payload, err := fr.ReadFrame()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadFrame: %v", err)
		}
		switch h.Kind {
		case KindOpen:
			os, err := UnmarshalOpenStream(payload)
			if err != nil {
				t.Fatalf("OPEN stream %d: %v", h.Stream, err)
			}
			if os.SessionID != uint64(h.Stream) {
				t.Fatalf("OPEN en stream %d declara sesion %d", h.Stream, os.SessionID)
			}
			if err := VerifyNonce(issued[h.Stream], os.Nonce); err != nil {
				t.Fatalf("nonce stream %d: %v", h.Stream, err)
			}
		case KindData:
			sig, ok := sign[h.Stream]
			if !ok {
				t.Fatalf("DATA de stream desconocido: %d", h.Stream)
			}
			for i, b := range payload {
				if b != sig {
					t.Fatalf("bytes cruzados: stream %d recibio %#x en %d (firma %#x)", h.Stream, b, i, sig)
				}
			}
			got[h.Stream] += len(payload)
		}
	}
	if err := <-werr; err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if fr.CorruptCount() != 0 {
		t.Errorf("frames con CRC invalido: %d", fr.CorruptCount())
	}

	// Sumas cruzadas remitente/receptor y desvio medido (±0,5 %).
	const tolerancia = 0.005
	totalSent, totalGot := 0, 0
	for sid, w := range want {
		totalSent += sent[sid]
		totalGot += got[sid]
		if sent[sid] != w {
			t.Fatalf("remitente stream %d: %d bytes, quiero %d", sid, sent[sid], w)
		}
		dev := math.Abs(float64(got[sid]-w)) / float64(w)
		if dev > tolerancia {
			t.Errorf("stream %d: bytes_in=%d, quiero %d +-=0,5%% (desvio %.3f%%)", sid, got[sid], w, dev*100)
		}
		t.Logf("desvio stream %d: %.3f%% (remitente %d, receptor %d)", sid, dev*100, sent[sid], got[sid])
	}
	devTotal := math.Abs(float64(totalGot-totalSent)) / float64(totalSent)
	if devTotal > tolerancia {
		t.Errorf("total nodo: recibio %d de %d (desvio %.3f%%, quiero +-=0,5%%)", totalGot, totalSent, devTotal*100)
	}
	t.Logf("desvio total: %.3f%% (tolerancia ±0,5%%)", devTotal*100)
}
