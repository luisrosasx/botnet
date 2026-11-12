package tunnel

import (
	"fmt"
	"net"
	"testing"

	"github.com/botnet/botnet/pkg/geoip"
)

// T-0407 AC2 (parte medible en host): capture de 50 agentes simulados —
// SNI con >= 2 dominios y histograma de puertos destino con >= 8 buckets
// (jitter de puerto activo). La captura tshark de 1 h es del soak.
func TestT0407CaptureSNIAndPorts(t *testing.T) {
	d := NewDispersion()
	seeds := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		seeds = append(seeds, fmt.Sprintf("agent-%03d", i))
	}
	rep := d.Capture(seeds)
	if rep.SNICount < 2 {
		t.Fatalf("SNI distintos = %d, want >= 2 dominios", rep.SNICount)
	}
	if got := len(rep.SortedBuckets()); got < 8 {
		t.Fatalf("buckets de puerto = %d, want >= 8", got)
	}
}

// T-0407 AC1 (parte de destino): los gateways de la fixture cubren al
// menos 2 ASNs — la condicion A1-18 vivita en la tabla de control.
func TestT0407PoolTwoASNs(t *testing.T) {
	table := geoip.DefaultFixture()
	pool := []string{"192.0.2.10:443", "192.0.2.11:443", "198.51.100.20:443"}
	seen := map[uint32]struct{}{}
	for _, gw := range pool {
		asn, ok := table.ASNOfGateway(gw, nil)
		if !ok {
			t.Fatalf("%s sin ASN en la tabla fixture", gw)
		}
		seen[asn] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("ASNs del pool = %d, want >= 2 (A1-18)", len(seen))
	}
}

// La dispersion es estable por nodo: el mismo seed conserva el destino
// (la rotacion cambia con la identidad, no con el reloj) y la tabla ASN
// fixture resuelve los dos ASNs de control.
func TestT0407DispersionStableWithASN(t *testing.T) {
	d := NewDispersion()
	sni1, port1 := d.ForNode("node-77")
	sni2, port2 := d.ForNode("node-77")
	if sni1 != sni2 || port1 != port2 {
		t.Fatalf("destino inestable: %s:%d vs %s:%d", sni1, port1, sni2, port2)
	}
	table := geoip.DefaultFixture()
	asnA, okA := table.Lookup(net.ParseIP("192.0.2.10"))
	asnB, okB := table.Lookup(net.ParseIP("198.51.100.20"))
	if !okA || !okB || asnA == asnB {
		t.Fatalf("fixture ASN: A=%d/%v B=%d/%v, want dos ASNs distintos", asnA, okA, asnB, okB)
	}
}
