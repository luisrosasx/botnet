package tunnel

import (
	"fmt"
	"testing"
	"time"
)

// AC2: captura de 1 h del nodo simulado — los intervalos entre frames de
// control (cada 3.er frame de la cadencia unica) caen en la ventana
// 60-150 s en >= 95 % de los casos. Con cadencia 40 s ± 4 s la suma de 3
// tick cae en 108-132 s por construccion.
func TestT1302ControlWindow95Pct(t *testing.T) {
	var all []time.Duration
	for i := 0; i < 20; i++ {
		seed := fmt.Sprintf("node-%02d", i)
		iv := ControlIntervals(seed, 86400/40) // 1 h de frames
		if len(iv) < 700 {
			t.Fatalf("%s: %d intervalos de control en 1 h, want ~720", seed, len(iv))
		}
		all = append(all, iv...)
	}
	rate := WindowPassRate(all)
	if rate < 0.95 {
		t.Fatalf("intervalos en ventana 60-150 s = %.3f, want >= 0,95", rate)
	}
	for _, d := range all {
		if d < ControlWindowMin || d > ControlWindowMax {
			t.Fatalf("intervalo fuera de ventana: %v", d)
		}
	}
}

// Ventanas, no constantes globales (exigencia 4 de A1): dos nodos con
// seeds distintos producen series de control distintas entre si.
func TestT1302ControlWindowNotGlobalConstant(t *testing.T) {
	a := ControlIntervals("node-alfa", 300)
	b := ControlIntervals("node-beta", 300)
	if len(a) != len(b) {
		t.Fatalf("longitudes de serie distintas: %d vs %d", len(a), len(b))
	}
	same := 0
	for i := range a {
		if a[i] == b[i] {
			same++
		}
	}
	if same == len(a) {
		t.Fatal("dos nodos con la misma cadencia global: el DPI ve un unico pico")
	}
}

// AC1 (parte medible): el CNAME del cliente es estable por nodo y rota
// entre >= 2 nombres compartibles.
func TestT1302CNAMEStableAndRotated(t *testing.T) {
	d := NewDispersion()
	if d.CNAMEForNode("node-77") != d.CNAMEForNode("node-77") {
		t.Fatal("CNAME inestable por nodo")
	}
	seen := map[string]struct{}{}
	for i := 0; i < 200; i++ {
		seen[d.CNAMEForNode(fmt.Sprintf("node-%03d", i))] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("CNAMEs distintos = %d, want >= 2", len(seen))
	}
}

// AC3: con el CNAME del cliente en el SNI, el histograma de destinos del
// nodo simulado no tiene un unico pico (>= 2 buckets y ASNs de T-0407).
func TestT1302HistogramNoSinglePeak(t *testing.T) {
	d := NewDispersion()
	seeds := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		seeds = append(seeds, fmt.Sprintf("node-%03d", i))
	}
	h := d.DestinationHistogram(seeds)
	if h.Buckets() < 2 {
		t.Fatalf("buckets del histograma = %d, want >= 2", h.Buckets())
	}
	if share := h.MaxShare(); share > 0.5 {
		t.Fatalf("pico unico: bucket dominante %.1f%% del trafico", share*100)
	}
	if cn := h.DistinctCNAMEs(); cn < 2 {
		t.Fatalf("CNAMEs en histograma = %d, want >= 2", cn)
	}
	asns := ASNBySNI()
	distinct := map[uint32]struct{}{}
	for _, asn := range asns {
		distinct[asn] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatalf("ASNs de los destinos de control = %d, want >= 2 (A1-18)", len(distinct))
	}
}
