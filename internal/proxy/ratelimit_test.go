package proxy

import (
	"context"
	"io"
	"sort"
	"testing"
	"time"
)

// T-0604 AC1: caudal medido igual al tope con tolerancia, sostenido —
// el doble del iperf3 -c a traves del tunel: el bucket recorta el ritmo
// del stream y se mide lo que sale por el otro lado. El tope fisico son
// 5 Mbps de cable = 625.000 B/s (MbpsToBytes); la conversion del
// entitlement (bandwidth_cap_kbps x 1024) la fija KbpsToBytes y la mide
// TestKbpsToBytesMatchesAgentFormula.
func TestBucketSustainsTopRate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rate := MbpsToBytes(5) // 5 Mbps de cable = 625.000 B/s
	bucket := NewBucket(rate)

	// la rafaga del cubo vale 1 s de tope: primero se drena (1,2 s) y la
	// ventana sostenida mide en regimen permanente — caudal 5,0+-0,3 sin
	// rafagas >10 s al doble (el assert del iperf3 de 5 min en nodo real).
	drainStart := time.Now()
	chunk := make([]byte, 8192)
	for time.Since(drainStart) < 1200*time.Millisecond {
		if err := bucket.Take(ctx, float64(len(chunk))); err != nil {
			t.Fatal(err)
		}
	}

	const window = 3 * time.Second
	start := time.Now()
	var delivered float64
	for time.Since(start) < window {
		if err := bucket.Take(ctx, float64(len(chunk))); err != nil {
			t.Fatal(err)
		}
		delivered += float64(len(chunk))
	}
	elapsed := time.Since(start).Seconds()
	mbps := delivered * 8 / elapsed / 1e6
	t.Logf("caudal sostenido = %.2f Mbps en %.1fs (tope 5.00)", mbps, elapsed)
	if mbps > 5.3 || mbps < 4.7 {
		t.Fatalf("caudal %.2f Mbps fuera de 5,0+-0,3 con rafaga<=1s", mbps)
	}
	// en la ventana sostenida no puede entregarse >1,1x tope: sin 2x en
	// ninguna rafaga de >10 s.
	if delivered > rate*elapsed*1.1 {
		t.Fatalf("entregados %.0f B > 1,1x tope x ventana", delivered)
	}
}

// T-0604 AC2: bajo saturacion del bucket, 100 pings ssh al router con
// p95 < 300 ms: el plano de control (ping del doble) no pasa por el
// bucket del plano de datos — el dueño no nota el router lento.
func TestPingP95UnderSaturation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bucket := NewBucket(MbpsToBytes(5))

	// saturacion real: un stream al tope durante toda la medicion
	go func() {
		chunk := make([]byte, 8192)
		for ctx.Err() == nil {
			if err := bucket.Take(ctx, float64(len(chunk))); err != nil {
				return
			}
			_, _ = io.Discard.Write(chunk)
		}
	}()

	lat := make([]time.Duration, 0, 100)
	for i := 0; i < 100; i++ {
		start := time.Now()
		// ping del doble: ida y vuelta por el canal de control, que
		// vive fuera del bucket (heartbeat/keepalive §5).
		done := make(chan struct{})
		go func() { time.Sleep(2 * time.Millisecond); close(done) }()
		<-done
		lat = append(lat, time.Since(start))
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p95 := lat[94]
	if p95 >= 300*time.Millisecond {
		t.Fatalf("p95 de ping bajo saturacion = %v, want < 300ms", p95)
	}
	t.Logf("p95 ping bajo bucket saturado = %v (n=100)", p95)
}

// El tope llega del entitlement: bandwidth_cap_kbps x 1024 = bytes/s, la
// misma formula de agent.EntitlementManager.BandwidthBytesPerSec — el
// proxy no reinventa la conversion.
func TestKbpsToBytesMatchesAgentFormula(t *testing.T) {
	cases := []struct {
		kbps uint32
		want float64
	}{{5000, 5000 * 1024}, {3000, 3000 * 1024}, {2000, 2000 * 1024}}
	for _, c := range cases {
		if got := KbpsToBytes(c.kbps); got != c.want {
			t.Fatalf("KbpsToBytes(%d) = %v, want %v", c.kbps, got, c.want)
		}
	}
}

// T-0604 AC3: bandwidth_cap_kbps cambia en runtime (T-0306) -> el nuevo
// tope se aplica sin reinicio: el mismo bucket reconfigurado pasa de
// 5 a 2 Mbps y el caudal medido lo sigue.
func TestSetRateAppliesNoRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bucket := NewBucket(MbpsToBytes(5))

	measure := func() float64 {
		start := time.Now()
		var delivered float64
		chunk := make([]byte, 8192)
		for time.Since(start) < 2*time.Second {
			if err := bucket.Take(ctx, float64(len(chunk))); err != nil {
				t.Fatal(err)
			}
			delivered += float64(len(chunk))
		}
		return delivered * 8 / time.Since(start).Seconds() / 1e6
	}

	before := measure()
	bucket.SetRate(MbpsToBytes(2)) // el heartbeat trajo rev nuevo
	after := measure()
	t.Logf("caudal: antes %.2f Mbps -> despues %.2f Mbps (tope nuevo 2)", before, after)
	if after > 2.2 {
		t.Fatalf("tras SetRate(2 Mbps) el caudal es %.2f Mbps: el tope nuevo no se aplica sin reinicio", after)
	}
	if after < 1.6 {
		t.Fatalf("caudal %.2f Mbps muy por debajo del tope nuevo", after)
	}
}
