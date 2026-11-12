package tunnel

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/hbcodec"
)

// T-0403 AC1 (parte medible en host): el fixture testdata/hb_intervals.txt
// es la "captura de 1 h en reposo" de un nodo: delta maximo < 45 s en el
// 100 % de los intervalos, media 40 s ± 10 % y 0 intervalos identicos
// consecutivos (jitter activo). El tshark real queda al soak (docs/runbooks).
func TestCadenceFixtureIntervals(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "hb_intervals.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var intervals []time.Duration
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var ms int
		if _, err := fmt.Sscan(line, &ms); err != nil {
			t.Fatalf("linea %q: %v", line, err)
		}
		intervals = append(intervals, time.Duration(ms)*time.Millisecond)
	}
	if len(intervals) < 100 {
		t.Fatalf("fixture con %d intervalos, want >= 100 (simulacion de 1 h)", len(intervals))
	}
	var sum time.Duration
	for i, d := range intervals {
		if d >= 45*time.Second {
			t.Fatalf("intervalo[%d] = %v, want < 45 s (purge CGNAT)", i, d)
		}
		if i > 0 && d == intervals[i-1] {
			t.Fatalf("intervalos identicos consecutivos en %d (%v)", i, d)
		}
		sum += d
	}
	mean := sum / time.Duration(len(intervals))
	if mean < 36*time.Second || mean > 44*time.Second {
		t.Fatalf("media = %v, want 40 s ± 10 %%", mean)
	}
}

// El generador del fixture: la misma Cadence con el seed del fichero
// reproduce la serie exacta (el fixture no pudo inventarse a mano).
func TestCadenceFixtureReproducible(t *testing.T) {
	c := NewCadence("node-0001")
	f, err := os.Open(filepath.Join("..", "..", "testdata", "hb_intervals.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "#") && line != "" {
			lines = append(lines, line)
		}
	}
	for i, line := range lines {
		var ms int
		if _, err := fmt.Sscan(line, &ms); err != nil {
			t.Fatal(err)
		}
		// el fixture guarda milisegundos truncados: misma truncacion.
		if got := c.Next(); got.Milliseconds() != int64(ms) {
			t.Fatalf("intervalo[%d]: Cadence genero %v, fixture dice %d ms", i, got, ms)
		}
	}
}

// T-0403 AC2: cada 3.er frame porta el heartbeat completo — ratio
// frames-parciales vs heartbeat = 2:1 en cualquier ventana.
func TestBeatKindRatio(t *testing.T) {
	var partial, hb int
	for seq := uint64(1); seq <= 90; seq++ { // 90 ticks = 1 h a 40 s
		if BeatKind(seq) {
			hb++
		} else {
			partial++
		}
	}
	if hb != 30 || partial != 60 {
		t.Fatalf("en 1 h: parciales %d heartbeats %d, want 60/30 (ratio 2:1)", partial, hb)
	}
}

// T-0403 AC3: beacons por dia por nodo medidos en simulacion con el
// contador tx del agente (frame real del codec) — <= 120 KB/nodo/dia
// incluyendo keepalive.
func TestBeaconBytesPerDayBudget(t *testing.T) {
	hb := &hbcodec.Heartbeat{
		Seq: 1, RssKiB: 4096, UptimeS: 3600, Streams: 2, LossEstimate: 75, AgentVer: "v0.1.0",
	}
	// media de tamano de heartbeat sobre 2000 encodes (relleno variable).
	var sumHB, sumPing, n int
	for i := 0; i < 2000; i++ {
		hb.Pad = nil
		b, err := hb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > hbcodec.MaxFrameLen {
			t.Fatalf("heartbeat %d B > %d", len(b), hbcodec.MaxFrameLen)
		}
		sumHB += len(b)
	}
	for i := 0; i < 2000; i++ {
		p := hbcodec.Ping(uint32(i))
		sumPing += len(p)
	}
	n = 2000
	avgHB, avgPing := sumHB/n+frameHeaderOverhead, sumPing/n+frameHeaderOverhead
	total := BeaconBytesPerDay(40*time.Second, avgHB, avgPing)
	const budget = 120 * 1024
	if total > budget {
		t.Fatalf("beacon diario = %d B, want <= %d (120 KB)", total, budget)
	}
	t.Logf("beacon/dia = %d B (HB medio %d B x %d, ping medio %d B x %d)",
		total, avgHB, TicksPerDay(40*time.Second)/3, avgPing, TicksPerDay(40*time.Second)-TicksPerDay(40*time.Second)/3)
}

// frameHeaderOverhead: los 14 B de cabecera del framing del tunel que
// envuelven cada payload de control.
const frameHeaderOverhead = 14
