package proto

import (
	"reflect"
	"testing"
)

func TestHeartbeatResponseRoundTrip(t *testing.T) {
	in := HeartbeatResponse{
		NextIntervalS:    120,
		ConfigRev:        7,
		PendingCmds:      []string{"update:1.2.3", "reboot"},
		Bye:              true,
		BandwidthCapKbps: 2048,
		MonthlyGbCap:     150,
		MaxStreams:       150,
		StableVersion:    "v0.1.0",
	}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out HeartbeatResponse
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("roundtrip: %+v != %+v", out, in)
	}
	if b[0] != CodecVersion {
		t.Fatalf("byte de version = %d != %d", b[0], CodecVersion)
	}
}

func TestHeartbeatResponseAbsentFields(t *testing.T) {
	// T-0306: un latido sin topes debe viajar con ceros (el agente
	// conserva los vigentes) y seguir siendo un frame valido.
	in := HeartbeatResponse{NextIntervalS: 40, ConfigRev: 3}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out HeartbeatResponse
	if err := out.Unmarshal(b); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.BandwidthCapKbps != 0 || out.MonthlyGbCap != 0 || out.MaxStreams != 0 || out.StableVersion != "" {
		t.Fatalf("campos ausentes no son ceros: %+v", out)
	}
}

func TestHeartbeatResponseTruncatedRejected(t *testing.T) {
	in := HeartbeatResponse{NextIntervalS: 40, ConfigRev: 3}
	b, err := in.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out HeartbeatResponse
	if err := out.Unmarshal(b[:len(b)-1]); err == nil {
		t.Fatal("frame truncado aceptado")
	}
}
