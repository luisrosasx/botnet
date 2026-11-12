package proto

import (
	"crypto/rand"
	"errors"
	"testing"
)

func TestLossPushRoundTrip(t *testing.T) {
	in := LossPush{Seq: 42, LossEstimateBPS: 150}
	if _, err := rand.Read(in.NodeID[:]); err != nil {
		t.Fatal(err)
	}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out LossPush
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip: got %+v want %+v", out, in)
	}
	if len(b) > 64 {
		t.Fatalf("frame demasiado grande: %d B (carril de control frugal)", len(b))
	}
}

func TestLossPushTrailingRejected(t *testing.T) {
	in := LossPush{LossEstimateBPS: 100}
	b, err := in.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&LossPush{}).Unmarshal(append(b, 0xAA)); !errors.Is(err, ErrInvalidLen) {
		t.Fatalf(" sobrantes deben fallar: %v", err)
	}
	// El frame debe poder viajar enlatado en CtrlFrame con su tipo.
	payload, _ := (&LossPush{LossEstimateBPS: 120}).Marshal()
	f := &CtrlFrame{Type: FrameLossPush, Seq: 7, Payload: payload}
	wire, err := EncodeFrame(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeFrame(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != FrameLossPush {
		t.Fatalf("tipo: %v", got.Type)
	}
	var lp LossPush
	if err := lp.Unmarshal(got.Payload); err != nil {
		t.Fatal(err)
	}
	if lp.LossEstimateBPS != 120 {
		t.Fatalf("loss: %d", lp.LossEstimateBPS)
	}
}

func TestFrameTypesStableOrder(t *testing.T) {
	// El cable F1 es estable: los tipos historicos no se reordenan ni borran;
	// FrameLossPush entra al final tras FrameCmdAck.
	want := []FrameType{FramePing, FrameHeartbeat, FrameHello, FrameRdvAssign,
		FrameSocksGreeting, FrameSocksRequest, FrameBye, FrameCmdAck, FrameLossPush}
	for i, wt := range want {
		if got := FrameType(i + 1); got != wt {
			t.Fatalf("tipo %d: got %v want %v (tabla historica intacta)", i+1, got, wt)
		}
	}
	// HelloCaps sigue siendo el payload de FrameHello (T-0901).
	p, err := (&HelloCaps{BWMbps: 10, ISO2: "ES", ASN: 1, DeviceClass: 'C'}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var back HelloCaps
	if err := back.Unmarshal(p); err != nil {
		t.Fatal(err)
	}
	if back.DeviceClass != 'C' || back.ISO2 != "ES" {
		t.Fatalf("caps: %+v", back)
	}
}
