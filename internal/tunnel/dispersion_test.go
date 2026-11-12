package tunnel

import (
	"fmt"
	"testing"
)

// Criterio 1 (T-1406): capture de 50 agentes simulados actualizando ->
// histograma de puertos con >= 8 buckets y >= 2 SNI; destinos dispersos.
func TestDispersionCapture50Agents(t *testing.T) {
	d := NewDispersion()
	seeds := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		seeds = append(seeds, fmt.Sprintf("rel-agent-%02d", i))
	}
	rep := d.Capture(seeds)
	if got := len(rep.SortedBuckets()); got < 8 {
		t.Fatalf("buckets de puerto %d, want >= 8 (%v)", got, rep.SortedBuckets())
	}
	if rep.SNICount < 2 {
		t.Fatalf("SNI distintos %d, want >= 2", rep.SNICount)
	}
	if rep.PortMax-rep.PortMin < 8 {
		t.Fatalf("rango de puertos %d..%d demasiado estrecho para jitter", rep.PortMin, rep.PortMax)
	}
}

// Determinismo por nodo: el mismo seed da el mismo destino (estable durante
// la vida del proceso; rota solo con la identidad).
func TestDispersionStablePerNode(t *testing.T) {
	d := NewDispersion()
	s1, p1 := d.ForNode("node-42-mac-aabb")
	s2, p2 := d.ForNode("node-42-mac-aabb")
	if s1 != s2 || p1 != p2 {
		t.Fatalf("destino inestable: %s:%d vs %s:%d", s1, p1, s2, p2)
	}
}
