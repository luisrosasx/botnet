package main

import (
	"fmt"
	"math"
	"sort"
)

// Scoring de densidad por destino (T-1304, A1-06/A1-17): la prima
// residencial se defiende midiendo la vista del destino — cuantos
// inquilinos distintos ve la IP residencial por hora. El score del pool
// (en produccion, clave score:{node} en Redis) decae con la densidad
// sostenida sobre el tope blando de 8 tenants/h y el nodo cronicamente
// denso sale del pool (quema), con re-asignacion rdv rapida.

// NodeStatus refleja nodes.status (AC2).
type NodeStatus string

const (
	NodeActive   NodeStatus = "active"
	NodeRetiring NodeStatus = "retiring"
	NodeRetired  NodeStatus = "retired"
)

// DensityScorer puntua la densidad observada en el destino.
type DensityScorer struct {
	SoftCap        int     // tenants/h que el destino tolera sin castigo (8, A1-17)
	DecayPerHour   float64 // multiplica el score por (1-Decay) cada hora sobre tope
	RecoverPerHour float64 // suma por hora bajo tope (hasta 1.0)
	Threshold      float64 // por debajo, el nodo deja de ser seleccionable (0,5)
	RetireHours    int     // horas bajo umbral = densidad cronica -> retiring
}

// NewDensityScorer fija la politica canonica: tope 8, decaimiento 5 %/h,
// recuperacion 4 pp/h, umbral 0,5, quema a las 6 h cronica.
func NewDensityScorer() *DensityScorer {
	return &DensityScorer{
		SoftCap:        8,
		DecayPerHour:   0.05,
		RecoverPerHour: 0.04,
		Threshold:      0.5,
		RetireHours:    6,
	}
}

// Update aplica una hora de observacion al score: sobre SoftCap decae;
// bajo el tope, recupera topado en 1.0.
func (s *DensityScorer) Update(score float64, tenantCount int) float64 {
	if tenantCount > s.SoftCap {
		score *= 1 - s.DecayPerHour
	} else {
		score += s.RecoverPerHour
	}
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}
	return score
}

// Selectable: con score bajo umbral el destino ya ve datacenter: fuera.
func (s *DensityScorer) Selectable(score float64) bool { return score >= s.Threshold }

// DensitySample: la densidad del destino se mide como par del RSS
// (AC3, dashboard T-1202): una serie por hora con tenant_count y rss_kib
// del heartbeat, nunca una en lugar de la otra.
type DensitySample struct {
	Hour        int
	TenantCount int
	RSSKiB      int
}

// PoolNode es un nodo del pool con su score, estado y serie de densidad.
type PoolNode struct {
	ID          string
	Score       float64
	Status      NodeStatus
	BelowSinceH int // primera hora bajo umbral (-1 si esta sano)
	Subtenants  []string
	Samples     []DensitySample
}

// DensityPool: pool de nodos con seleccion por score. En produccion el
// score vive en Redis y nodes.status en Postgres; aqui se modela la
// logica, que es lo que el test puede asertar sin servicios.
type DensityPool struct {
	Scorer *DensityScorer
	order  []string
	Nodes  map[string]*PoolNode
}

func NewDensityPool(ids ...string) *DensityPool {
	p := &DensityPool{Scorer: NewDensityScorer(), Nodes: map[string]*PoolNode{}}
	for _, id := range ids {
		p.order = append(p.order, id)
		p.Nodes[id] = &PoolNode{ID: id, Score: 1, Status: NodeActive, BelowSinceH: -1}
	}
	return p
}

// Tick aplica una hora de tenant_count observado por nodo: score, serie
// AC3 y transicion a retiring por densidad cronica (AC2).
func (p *DensityPool) Tick(h int, tenantCountByNode map[string]int, rssKiBByNode map[string]int) {
	for _, id := range p.order {
		n := p.Nodes[id]
		if n.Status != NodeActive {
			continue
		}
		tc := tenantCountByNode[id]
		n.Score = p.Scorer.Update(n.Score, tc)
		n.Samples = append(n.Samples, DensitySample{Hour: h, TenantCount: tc, RSSKiB: rssKiBByNode[id]})
		if !p.Scorer.Selectable(n.Score) {
			if n.BelowSinceH < 0 {
				n.BelowSinceH = h
			}
			if h-n.BelowSinceH+1 >= p.Scorer.RetireHours {
				n.Status = NodeRetiring
			}
		} else {
			n.BelowSinceH = -1
		}
	}
}

// SelectableNodes son los candidates de seleccion (active con score
// sobre umbral), en orden determinista.
func (p *DensityPool) SelectableNodes() []*PoolNode {
	var out []*PoolNode
	for _, id := range p.order {
		n := p.Nodes[id]
		if n.Status == NodeActive && p.Scorer.Selectable(n.Score) {
			out = append(out, n)
		}
	}
	return out
}

// Select imita la seleccion del rotativo: la carga menor entre
// seleccionables; nil si el destino ya no admite el pool.
func (p *DensityPool) Select() *PoolNode {
	best := p.SelectableNodes()
	if len(best) == 0 {
		return nil
	}
	var out *PoolNode
	for _, n := range best {
		if out == nil || len(n.Subtenants) < len(out.Subtenants) {
			out = n
		}
	}
	return out
}

// AssignAncestors: los tenants que vivian en un nodo retiring se
// re-asignan a los seleccionables restantes (rdv re-asigna, AC2).
// Devuelve tenant -> nuevo nodo; un pool sin seleccionables no puede
// re-asignar (devuelve vacio).
func (p *DensityPool) Redistribute(retiringID string) map[string]string {
	out := map[string]string{}
	n := p.Nodes[retiringID]
	if n == nil {
		return out
	}
	n.Status = NodeRetired
	for _, tenant := range n.Subtenants {
		target := p.Select()
		if target == nil {
			return out
		}
		target.Subtenants = append(target.Subtenants, tenant)
		out[tenant] = target.ID
	}
	n.Subtenants = nil
	return out
}

// P99TenantCount es el panel `tenant_count` p99 por nodo (AC3): percentil
// sobre la serie de densidad del nodo.
func P99TenantCount(samples []DensitySample) (int, bool) {
	if len(samples) == 0 {
		return 0, false
	}
	v := make([]int, 0, len(samples))
	for _, s := range samples {
		v = append(v, s.TenantCount)
	}
	sort.Ints(v)
	idx := int(math.Ceil(0.99*float64(len(v)))) - 1
	if idx < 0 {
		idx = 0
	}
	return v[idx], true
}

// String ayuda a logs/asserts.
func (n *PoolNode) String() string {
	return fmt.Sprintf("%s score=%.2f %s", n.ID, n.Score, n.Status)
}
