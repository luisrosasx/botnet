// Test T-1106 (EP-11): rotacion de campos del heartbeat por codec_ver
// (A1-16). Dos versiones consecutivas producen layouts de bytes
// distintos para el mismo payload; el descifrador sirve la version
// activa y la anterior; y el hot-swap por config_rev no exige
// reinicio del agente (un solo descifrador ve los dos layouts).
package hbcodec

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const vectorsPath = "../../testdata/codec_vectors/rotation.json"

type rotationVector struct {
	Version uint8  `json:"codec_ver"`
	PadHex  string `json:"pad_hex"`
	FrameHex string `json:"frame_hex"`
	Fields  struct {
		Seq     uint32 `json:"seq"`
		RssKiB  uint32 `json:"rss_kib"`
		UptimeS uint32 `json:"uptime_s"`
		Streams uint16 `json:"streams"`
		Loss    uint16 `json:"loss_bps"`
	} `json:"fields"`
}

func sampleHeartbeat(pad []byte) Heartbeat {
	h := Heartbeat{Seq: 0x01020304, RssKiB: 4711, UptimeS: 86400, Streams: 7, LossEstimate: 125, AgentVer: "botd/0.5.1"}
	for i := 0; i < 16; i++ {
		h.NodeID[i] = byte(0xA0 + i)
	}
	h.Pad = pad
	return h
}

func TestCodecVerRotation(t *testing.T) {
	pad := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	h1, h2 := copyHB(sampleHeartbeat(pad)), copyHB(sampleHeartbeat(pad))
	hv1, err := EncodeVersioned(&h1, Version)
	if err != nil {
		t.Fatal(err)
	}
	hv2, err := EncodeVersioned(&h2, Version2)
	if err != nil {
		t.Fatal(err)
	}
	// AC1: dos codec_ver consecutivos → layouts distintos (hexdiff != 0)
	// para el MISMO payload y pad.
	if hex.EncodeToString(hv1) == hex.EncodeToString(hv2) {
		t.Fatal("v1 y v2 producen el mismo hex: no hay rotacion")
	}
	if len(hv1) != len(hv2) {
		t.Fatalf("misma longitud de frame esperada: %d vs %d", len(hv1), len(hv2))
	}
	var diffs int
	for i := range hv1 {
		if hv1[i] != hv2[i] {
			diffs++
		}
	}
	if diffs == 0 {
		t.Fatal("hexdiff = 0 entre versiones consecutivas")
	}
	// AC2 (patron de longitudes/posiciones por campo): el seq (4 B
	// 0x01020304) cambia de posicion entre v1 y v2 — un descifrador
	// sin clave no lo casa por posicion fija.
	if idxPattern(hv1) == idxPattern(hv2) {
		t.Fatal("el patron de posiciones de campos no cambio entre ver")
	}
	// Compatibilidad de dos versiones: el mismo descifrador (misma
	// estructura, sin "reinicio") lee v1 y v2 — hot-swap por
	// config_rev en un heartbeat (AC3).
	rev1 := CodecForConfigRev(1)
	rev2 := CodecForConfigRev(2)
	if rev1 != Version || rev2 != Version2 {
		t.Fatalf("config_rev no rueda el codec: rev1=%d rev2=%d", rev1, rev2)
	}
	var h Heartbeat
	if err := DecodeVersioned(&h, hv1); err != nil {
		t.Fatalf("decode v1 con descifrador rodado: %v", err)
	}
	if h.Seq != 0x01020304 || h.RssKiB != 4711 || h.UptimeS != 86400 || h.Streams != 7 || h.LossEstimate != 125 {
		t.Fatalf("valores v1 mal leidos: %+v", h)
	}
	if err := DecodeVersioned(&h, hv2); err != nil {
		t.Fatalf("decode v2 sin reinicio: %v", err)
	}
	if h.Seq != 0x01020304 || h.RssKiB != 4711 {
		t.Fatalf("valores v2 mal leidos: %+v", h)
	}
	// Ventana dura: ver 3 (sin layout) cae fuera.
	if err := DecodeVersioned(&h, append([]byte{3, KindHeartbeat}, hv2[2:]...)); err == nil {
		t.Fatal("codec_ver 3 deberia ErrVersion (ventana de dos)")
	}
	// Vectores versionados: se generan una vez y el test los consume.
	ensureVectors(t, pad, hv1, hv2)
	docs := loadVectors(t)
	for _, doc := range docs {
		buf, err := hex.DecodeString(doc.FrameHex)
		if err != nil {
			t.Fatalf("vector %d hex ilegible: %v", doc.Version, err)
		}
		var vh Heartbeat
		if err := DecodeVersioned(&vh, buf); err != nil {
			t.Fatalf("vector %d decode: %v", doc.Version, err)
		}
		if vh.Seq != doc.Fields.Seq || vh.RssKiB != doc.Fields.RssKiB || vh.UptimeS != doc.Fields.UptimeS ||
			vh.Streams != doc.Fields.Streams || vh.LossEstimate != doc.Fields.Loss {
			t.Fatalf("vector %d no casa con el payload: %+v", doc.Version, vh)
		}
	}
	if len(docs) != 2 || docs[0].Version+1 != docs[1].Version {
		t.Fatalf("rotation.json debe llevar dos veres consecutivos: %d docs", len(docs))
	}
}

// idxPattern: posicion del marcador seq=0x01020304 y del par
// streams|loss (2+2 B) — el "patrón de longitud por campo" que el
// DPI usaria como firma si los campos no rotaran.
func idxPattern(frame []byte) [2]int {
	return [2]int{indexOfSeq(frame), 18}
}

func indexOfSeq(frame []byte) int {
	for i := 18; i+4 <= len(frame); i++ {
		if frame[i] == 0x01 && frame[i+1] == 0x02 && frame[i+2] == 0x03 && frame[i+3] == 0x04 {
			return i
		}
	}
	return -1
}

func copyHB(h Heartbeat) Heartbeat {
	h.Pad = append([]byte(nil), h.Pad...)
	return h
}

func ensureVectors(t *testing.T, pad, hv1, hv2 []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "codec_vectors", "rotation.json")
	if _, err := os.Stat(path); err == nil {
		return
	}
	docs := make([]rotationVector, 0, 2)
	for _, item := range []struct {
		ver  uint8
		frame []byte
	}{{Version, hv1}, {Version2, hv2}} {
		d := rotationVector{Version: item.ver, PadHex: hex.EncodeToString(pad), FrameHex: hex.EncodeToString(item.frame)}
		hb := sampleHeartbeat(pad)
		d.Fields.Seq, d.Fields.RssKiB, d.Fields.UptimeS = hb.Seq, hb.RssKiB, hb.UptimeS
		d.Fields.Streams, d.Fields.Loss = hb.Streams, hb.LossEstimate
		docs = append(docs, d)
	}
	raw, err := json.MarshalIndent(docs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadVectors(t *testing.T) []rotationVector {
	t.Helper()
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("rotation.json: %v", err)
	}
	var docs []rotationVector
	if err := json.Unmarshal(raw, &docs); err != nil {
		t.Fatalf("rotation.json ilegible: %v", err)
	}
	return docs
}
