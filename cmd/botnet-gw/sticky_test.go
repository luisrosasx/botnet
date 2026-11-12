package main

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// AC1: sticky — mismo node_id en >= 95 % de los 50 requests, con clave
// sticky:{t}:{client}:{key} y TTL de la politica (api_keys.sticky_ttl_s).
func TestStickyKeepsSameNode(t *testing.T) {
	ctx := context.Background()
	store := NewStickyMapStore(nil)
	book := &StickyBook{Store: store, TTL: 300 * time.Second, PoolSizeTop: 100}
	rot := NewWeightedRotator(WeightedNode{ID: "n1", BWMbps: 40}, WeightedNode{ID: "n2", BWMbps: 30})

	first, _ := book.Pick(ctx, "t1", "client-a", "k1", rot.Next)
	if first == "" {
		t.Fatal("primer pick sticky")
	}
	same := 1 // el primer request fija el binding
	for i := 1; i < 50; i++ {
		node, _ := book.Pick(ctx, "t1", "client-a", "k1", rot.Next)
		if node == "" {
			t.Fatalf("pick %d: vacio", i)
		}
		if node == first {
			same++
		}
	}
	if pct := 100 * float64(same) / 50; pct < 95 {
		t.Fatalf("sticky: %.1f %% mismo node_id < 95 %%", pct)
	}
	if v, ok := store.vals[StickyKey("t1", "client-a", "k1")]; !ok || v != first {
		t.Fatalf("la clave sticky debe vivir en %s: %q", StickyKey("t1", "client-a", "k1"), v)
	}
	// TTL cumplido, binding fuera: sticky no es cadena vital.
	clock := time.Now()
	store2 := NewStickyMapStore(func() time.Time { return clock })
	book2 := &StickyBook{Store: store2, TTL: 10 * time.Second, PoolSizeTop: 100}
	a, _ := book2.Pick(ctx, "t2", "c", "k", rot.Next)
	clock = clock.Add(11 * time.Second)
	b, _ := book2.Pick(ctx, "t2", "c", "k", func() string { return "otro" })
	if a == "" || b != "otro" {
		t.Fatalf("el binding vencido debe re-elegir: a=%q b=%q", a, b)
	}
}

// AC2: rotativo — >= 80 % node_id distintos y el reparto pondera
// bw_mbps: nodo de 5 Mbps recibe >= 2x las sesiones del de 2 Mbps.
func TestRotatingDistinctAndWeighted(t *testing.T) {
	nodes := make([]WeightedNode, 0, 50)
	for i := 0; i < 50; i++ {
		nodes = append(nodes, WeightedNode{ID: itoa(i), BWMbps: 10})
	}
	rot := NewWeightedRotator(nodes...)
	seen := map[string]int{}
	for i := 0; i < 50; i++ {
		seen[rot.Next()]++
	}
	if float64(len(seen))/50*100 < 80 {
		t.Fatalf("distinct %d/50 < 80 %%", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("SWRR reparte parejo: %s recibi %d", id, n)
		}
	}

	// Ponderacion por bw_mbps medido (HELLO, T-0901).
	w := NewWeightedRotator(WeightedNode{ID: "rapido", BWMbps: 5}, WeightedNode{ID: "lento", BWMbps: 2})
	counts := map[string]int{}
	for i := 0; i < 140; i++ {
		counts[w.Next()]++
	}
	if counts["rapido"] < 2*counts["lento"] {
		t.Fatalf("bw 5 vs 2 Mbps: %d < 2x%d", counts["rapido"], counts["lento"])
	}
	t.Logf("rotativo ponderado: rapido=%d lento=%d (ratio %.2f)", counts["rapido"], counts["lento"],
		float64(counts["rapido"])/float64(counts["lento"]))
}

// AC3: pool chico (< 50 nodos) topa el sticky_ttl efectivo a <= 120 s
// (politica asertada, no fijar nodos).
func TestSmallPoolStickyTTLCapped(t *testing.T) {
	if got := EffectiveStickyTTL(300*time.Second, 49); got > SmallPoolStickyMaxT {
		t.Fatalf("pool 49: TTL efectivo %v > 120s", got)
	}
	if got := EffectiveStickyTTL(300*time.Second, 50); got != 300*time.Second {
		t.Fatalf("pool 50: el sticky_ttl de la clave manda, got %v", got)
	}
	if got := EffectiveStickyTTL(60*time.Second, 10); got != 60*time.Second {
		t.Fatalf("TTL menor no se infla: %v", got)
	}
	// Y se aplica de verdad: el binding de pool chico expira a los 120 s.
	clock := time.Now()
	store := NewStickyMapStore(func() time.Time { return clock })
	book := &StickyBook{Store: store, TTL: 300 * time.Second, PoolSizeTop: 10}
	if n7, _ := book.Pick(context.Background(), "t", "c", "k", func() string { return "n7" }); n7 != "n7" {
		t.Fatal("pick pool chico")
	}
	if exp := store.exp[StickyKey("t", "c", "k")].Sub(clock); exp > SmallPoolStickyMaxT {
		t.Fatalf("binding vivo %v > 120s en pool chico", exp)
	}
}

// TestStickinessThresholds es el objetivo de scripts/gw/stickiness.sh:
// --mode/--n/--ttl via env (GW_STICKY_*); el script aprueba o suspende
// con la salida del test, no con parrafos.
func TestStickinessThresholds(t *testing.T) {
	mode := os.Getenv("GW_STICKY_MODE")
	if mode == "" {
		mode = "sticky"
	}
	n := envInt("GW_STICKY_N", 50)
	ttl := time.Duration(envInt("GW_STICKY_TTL", 300)) * time.Second

	pool := make([]WeightedNode, 0, 100)
	for i := 0; i < 100; i++ {
		pool = append(pool, WeightedNode{ID: "node-" + itoa(i), BWMbps: uint16(5 + i%40)})
	}

	switch mode {
	case "sticky":
		ctx := context.Background()
		rot := NewWeightedRotator(pool...)
		book := &StickyBook{Store: NewStickyMapStore(nil), TTL: ttl, PoolSizeTop: len(pool)}
		counts := map[string]int{}
		for i := 0; i < n; i++ {
			node, _ := book.Pick(ctx, "tenant-soak", "client-soak", "key-soak", rot.Next)
			if node == "" {
				t.Fatalf("request %d sin nodo", i)
			}
			counts[node]++
		}
		best := 0
		for _, c := range counts {
			if c > best {
				best = c
			}
		}
		if pct := 100 * float64(best) / float64(n); pct < 95 {
			t.Fatalf("sticky: mismo node_id %.1f %% < 95 %%", pct)
		}
		t.Logf("stickiness: mode=sticky n=%d ttl=%s → %.1f %% mismo node_id", n, ttl, 100*float64(best)/float64(n))
	case "rotating":
		rot := NewWeightedRotator(pool...)
		seen := map[string]struct{}{}
		for i := 0; i < n; i++ {
			seen[rot.Next()] = struct{}{}
		}
		if pct := 100 * float64(len(seen)) / float64(n); pct < 80 {
			t.Fatalf("rotating: %d node_id distintos (%.1f %%) < 80 %%", len(seen), pct)
		}
		t.Logf("stickiness: mode=rotating n=%d → %d node_id distintos (%.1f %%)", n, len(seen), 100*float64(len(seen))/float64(n))
	default:
		t.Fatalf("modo desconocido: %s", mode)
	}
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
