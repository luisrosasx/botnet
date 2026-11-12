package main

import "testing"

// AC1: fixture con densidad sostenida > 8 tenants/h durante 24 h ->
// score < 0,5 y el nodo deja de ser seleccionado (simulacion de
// seleccion: una sesion por hora sobre el pool de dos nodos).
func TestT1304SustainedDensityDecaysScoreAndSelection(t *testing.T) {
	pool := NewDensityPool("hot", "calm")

	selectedHot := 0
	for h := 1; h <= 24; h++ {
		pool.Tick(h,
			map[string]int{"hot": 10, "calm": 3}, // hot: 10 tenants/h > tope 8
			map[string]int{"hot": 3100, "calm": 2900})
		if n := pool.Select(); n != nil { // la simulacion de seleccion enlaza una sesion por hora
			if n.ID == "hot" {
				selectedHot++
			}
			n.Subtenants = append(n.Subtenants, "s"+string(rune('A'+h%26)))
		}
	}

	hot := pool.Nodes["hot"]
	if hot.Score >= 0.5 {
		t.Fatalf("score de densidad tras 24 h a 10 tenants/h = %.3f, want < 0,5", hot.Score)
	}
	if pool.Scorer.Selectable(hot.Score) {
		t.Fatalf("score %.3f sigue seleccionable con umbral %.2f", hot.Score, pool.Scorer.Threshold)
	}
	// Seleccion tras el decaimiento: el nodo caliente ya no entra.
	for h := 25; h <= 30; h++ {
		pool.Tick(h, map[string]int{"hot": 10, "calm": 3}, map[string]int{"hot": 3100, "calm": 2900})
		if n := pool.Select(); n != nil && n.ID == "hot" {
			t.Fatalf("hora %d: el nodo denso sigue entrando en seleccion (score %.2f)", h, hot.Score)
		}
	}
	if selectedHot < 5 {
		t.Logf("nota: hot recien deja de ser seleccionable a mitad de ventana (selecciones tempranas: %d)", selectedHot)
	}
}

// AC2: quema por densidad cronica — el nodo pasa a retiring, sale del
// pool y el rdv re-asigna sus tenants en < 24 h (reloj acelerado: una
// hora por Tick). nodes.status lo refleja.
func TestT1304ChronicDensityRetiresAndRedistributes(t *testing.T) {
	pool := NewDensityPool("hot", "calm")
	pool.Nodes["hot"].Subtenants = []string{"t1", "t2", "t3"}

	retiredHour := 0
	for h := 1; h <= 48 && retiredHour == 0; h++ {
		pool.Tick(h, map[string]int{"hot": 10, "calm": 3}, map[string]int{"hot": 3100, "calm": 2900})
		if pool.Nodes["hot"].Status == NodeRetiring {
			retiredHour = h
			break
		}
	}
	if retiredHour == 0 {
		t.Fatal("el nodo denso cronico nunca paso a retiring en 48 h")
	}
	if got := pool.Nodes["hot"].Status; got != NodeRetiring {
		t.Fatalf("nodes.status = %s, want retiring", got)
	}
	// Re-asignacion rdv: sale del pool y sus tenants vuelven a nodos
	// seleccionables; la ventana total (densidad cronica + re-asignacion)
	// debe quedar por debajo de 24 h.
	moved := pool.Redistribute("hot")
	if len(moved) != 3 {
		t.Fatalf("tenants re-asignados = %d, want 3", len(moved))
	}
	for tenant, node := range moved {
		if node != "calm" {
			t.Fatalf("tenant %s re-asignado a %q, want un nodo del pool", tenant, node)
		}
	}
	if pool.Nodes["hot"].Status != NodeRetired {
		t.Fatalf("tras re-asignar, status = %s, want retired", pool.Nodes["hot"].Status)
	}
	if retiredHour >= 24 {
		t.Fatalf("la quema+re-asignacion tardo %d h, want < 24 h", retiredHour)
	}
}

// Recuperacion: cesada la densidad, el score vuelve a la franja util y
// el nodo recupera seleccion (la quema es por densidad, no perpetua).
func TestT1304ScoreRecoversBelowCap(t *testing.T) {
	s := NewDensityScorer()
	score := 1.0
	for h := 0; h < 20; h++ { // 20 h de datacenter en casa
		score = s.Update(score, 12)
	}
	if score >= 0.5 {
		t.Fatalf("20 h a 12 tenants/h: score %.3f, want < 0,5", score)
	}
	for h := 0; h < 24; h++ { // 24 h de trafico residencial limpio
		score = s.Update(score, 2)
	}
	if !s.Selectable(score) || score < 0.9 {
		t.Fatalf("tras 24 h bajo tope: score %.3f, want recuperado (~1)", score)
	}
}

// AC3: la metrica de densidad del destino se registra como par del RSS y
// el panel tenant_count p99 por nodo existe (serie + percentil).
func TestT1304DensitySeriesP99AlongsideRSS(t *testing.T) {
	pool := NewDensityPool("n1")
	for h := 1; h <= 24; h++ {
		tc := 2
		if h%6 == 0 {
			tc = 9 // picos de tenant_count
		}
		pool.Tick(h, map[string]int{"n1": tc}, map[string]int{"n1": 3000 + h})
	}
	samples := pool.Nodes["n1"].Samples
	if len(samples) != 24 {
		t.Fatalf("serie de densidad = %d muestras, want 24 (una por hora)", len(samples))
	}
	for _, s := range samples {
		if s.RSSKiB == 0 {
			t.Fatalf("hora %d: RSS ausente de la serie — la densidad se mide como PAR del RSS", s.Hour)
		}
	}
	p99, ok := P99TenantCount(samples)
	if !ok || p99 != 9 {
		t.Fatalf("tenant_count p99 = %d/%v, want 9", p99, ok)
	}
}
