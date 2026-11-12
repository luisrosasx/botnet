// configrev_codec_test.go — T-1106 AC3 (lado agente): el config_rev que
// decrete el backend cambia el ALAMBRE del heartbeat. Sin rev, el nucleo
// proto historico; con rev, el frame compacto de hbcodec con el layout
// que dicta CodecForConfigRev — y el bucle consume ConfigRev de la
// respuesta sin reinicio (hot-swap).
package agent

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/botnet/botnet/internal/hbcodec"
	"github.com/botnet/botnet/pkg/proto"
)

func TestBeatBodyRespetaConfigRev(t *testing.T) {
	c := &HeartbeatClient{NodeID: [16]byte{7, 7, 7, 7}, AgentVer: "v0.1.0"}
	for _, tc := range []struct {
		rev     uint64
		wantV   uint8
		compact bool
	}{
		{rev: 0, wantV: 0, compact: false},               // nadie decreto: nucleo proto
		{rev: 1, wantV: hbcodec.Version, compact: true},  // v1 compacto
		{rev: 2, wantV: hbcodec.Version2, compact: true}, // layout rotado
	} {
		c.CodecRev = tc.rev
		body, err := c.beatBody(42, 3000, 60, 2)
		if err != nil {
			t.Fatalf("rev %d: %v", tc.rev, err)
		}
		if !tc.compact {
			var hb proto.Heartbeat
			if err := hb.Unmarshal(body); err != nil {
				t.Fatalf("rev 0 debe ser nucleo proto: %v", err)
			}
			if hb.Seq != 42 || hb.RssKiB != 3000 {
				t.Fatalf("nucleo proto sin telemetria: %+v", hb)
			}
			continue
		}
		if body[0] != tc.wantV {
			t.Fatalf("rev %d: codec_ver en alambre = %d, want %d", tc.rev, body[0], tc.wantV)
		}
		if len(body) >= 200 {
			t.Fatalf("rev %d: el frame compacto mide %d B, want < 200", tc.rev, len(body))
		}
		var back hbcodec.Heartbeat
		if err := hbcodec.DecodeVersioned(&back, body); err != nil {
			t.Fatalf("rev %d: DecodeVersioned: %v", tc.rev, err)
		}
		if back.Seq != 42 || back.RssKiB != 3000 || back.Streams != 2 || back.AgentVer != "v0.1.0" {
			t.Fatalf("rev %d: telemetria no viajo: %+v", tc.rev, back)
		}
	}
	// La rotacion es REAL en el alambre: mismo payload, rev 1 vs rev 2 →
	// el marcador seq cambia de offset (layoutFor) y el hex difiere.
	c.CodecRev = 1
	f1, _ := c.beatBody(0x01020304, 3000, 60, 2)
	c.CodecRev = 2
	f2, _ := c.beatBody(0x01020304, 3000, 60, 2)
	i1, i2 := bytes.Index(f1, []byte{0x01, 0x02, 0x03, 0x04}), bytes.Index(f2, []byte{0x01, 0x02, 0x03, 0x04})
	if i1 < 0 || i2 < 0 || i1 == i2 {
		t.Fatalf("rotacion no movio el seq: offets %d vs %d", i1, i2)
	}
}

// El bucle consume config_rev: una respuesta 200 con ConfigRev=2 deja al
// client en codec compacto v2 para el SIGUIENTE latido — sin reinicio.
func TestApplyBeatInstalaCodecRev(t *testing.T) {
	resp := proto.HeartbeatResponse{NextIntervalS: 120, ConfigRev: 2}
	rsBody, err := resp.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/heartbeat" {
			got, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(rsBody)
	}))
	defer srv.Close()

	c := &HeartbeatClient{Endpoint: srv.URL, NodeID: [16]byte{1, 2, 3}, AgentVer: "v0.1.0"}
	d := AgentDeps{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Client: c}
	if _, done := d.applyBeat(context.Background(), 1, 2000, 5, &[]string{}); done {
		t.Fatal("no debio cerrar el bucle")
	}
	if c.CodecRev != 2 {
		t.Fatalf("config_rev no instalo el codec: rev=%d", c.CodecRev)
	}
	// El latido que el bucle mando ya salio con el codec vigente en ese
	// momento (rev 0 = proto); el SIGUIENTE sale compacto v2: es el
	// hot-swap del criterio.
	var hb proto.Heartbeat
	if err := hb.Unmarshal(got); err != nil {
		t.Fatalf("primer latido (rev 0) no es nucleo proto: %v", err)
	}
	body, err := c.beatBody(2, 2000, 6, 0)
	if err != nil {
		t.Fatal(err)
	}
	if body[0] != hbcodec.Version2 {
		t.Fatalf("siguiente latido: codec_ver=%d, want v2 tras config_rev=2", body[0])
	}
}
