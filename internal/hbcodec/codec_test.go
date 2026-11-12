package hbcodec

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- T-0404 AC1: round-trip exacto sobre los vectores versionados -------

func readVector(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "codec_vectors", name))
	if err != nil {
		t.Fatal(err)
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("%s: hex: %v", name, err)
	}
	return b
}

func TestVectors(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "codec_vectors", "*.hex"))
	if err != nil || len(files) < 6 {
		t.Fatalf("vectores: %v (%d ficheros)", err, len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		raw := readVector(t, name)
		// 0 panicos en cualquier entrada del corpus (base del fuzz).
		var hb Heartbeat
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panico %v", name, r)
				}
			}()
			if err := hb.Decode(raw); err != nil {
				return // vectores "junk": error limpio, no panico
			}
			// encode∘decode = identidad byte a byte.
			out, err := hb.Encode()
			if err != nil {
				t.Fatalf("%s: Encode: %v", name, err)
			}
			if string(out) != string(raw) {
				t.Fatalf("%s: round-trip distinto (%d B vs %d B)", name, len(out), len(raw))
			}
			var again Heartbeat
			if err := again.Decode(out); err != nil {
				t.Fatalf("%s: re-Decode: %v", name, err)
			}
			if again.Seq != hb.Seq || again.RssKiB != hb.RssKiB || again.Streams != hb.Streams {
				t.Fatalf("%s: campos no preservados", name)
			}
		}()
	}
}

// --- T-0404 AC2: el relleno mata la longitud fija ------------------------

func TestPaddingVariation(t *testing.T) {
	hb := &Heartbeat{Seq: 7, RssKiB: 1024, UptimeS: 60, AgentVer: "v0.1.0"}
	sizes := map[int]struct{}{}
	minS, maxS := 1<<20, 0
	for i := 0; i < 100; i++ {
		hb.Pad = nil
		b, err := hb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		sizes[len(b)] = struct{}{}
		if len(b) < minS {
			minS = len(b)
		}
		if len(b) > maxS {
			maxS = len(b)
		}
		if len(b) > MaxFrameLen {
			t.Fatalf("frame de %d B > %d B", len(b), MaxFrameLen)
		}
	}
	if len(sizes) < 3 {
		t.Fatalf("100 encodes dieron %d longitudes, want >= 3", len(sizes))
	}
	if maxS-minS > MaxPad {
		t.Fatalf("max-min = %d B, want <= %d B", maxS-minS, MaxPad)
	}
}

// frame de heartbeat <= 255 B (criterio 3, parte medible).
func TestHeartbeatFrameMaxSize(t *testing.T) {
	hb := &Heartbeat{
		NodeID: [16]byte{}, Seq: 1, RssKiB: 6144, UptimeS: 1 << 30,
		Streams: 65535, LossEstimate: 10000, AgentVer: strings.Repeat("v", MaxAgentVer),
		Pad: make([]byte, MaxPad),
	}
	b, err := hb.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 255 {
		t.Fatalf("heartbeat peor caso = %d B, want <= 255 B", len(b))
	}
}

// ping y cmdack: round-trip de sus cargas.
func TestPingAndCmdAck(t *testing.T) {
	p := PingRand(99, 5)
	seq, err := DecodePing(p)
	if err != nil || seq != 99 {
		t.Fatalf("ping: seq %d err %v", seq, err)
	}
	if len(PingRand(1, 0)) != 6 || len(PingRand(1, MaxPingFiller)) != 6+MaxPingFiller {
		t.Fatal("tamano del ping fuera de [6, 14] B")
	}
	ids := []string{"update:a1b2c3", "reboot:x9"}
	b, err := CmdAck(ids)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCmdAck(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != ids[0] || got[1] != ids[1] {
		t.Fatalf("cmdack round-trip: %v", got)
	}
}

// rechazos de contrato: version extrana, kind desconocido, truncado.
func TestDecodeRejections(t *testing.T) {
	hb := &Heartbeat{Seq: 1}
	good, err := hb.Encode()
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), good...)
	bad[0] = 99
	var out Heartbeat
	if err := out.Decode(bad); err == nil {
		t.Fatal("version 99 deberia rechazarse")
	}
	bad = append([]byte(nil), good...)
	bad[1] = 77
	if err := out.Decode(bad); err == nil {
		t.Fatal("kind 77 deberia rechazarse")
	}
	if err := out.Decode(good[:3]); err == nil {
		t.Fatal("truncado deberia rechazarse")
	}
	tooLong := make([]byte, MaxFrameLen+1)
	if err := out.Decode(tooLong); err == nil {
		t.Fatal("frame > 255 B deberia rechazarse")
	}
}
