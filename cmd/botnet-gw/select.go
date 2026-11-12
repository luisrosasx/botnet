package main

import (
	"errors"
	"math/rand"
	"time"

	"github.com/botnet/botnet/internal/agent"
)

// Seleccion local de pool (EP-09 T-0903, §10): el gateway lee SOLO su
// gw:pool:{gw_id} y sus caps locales; sin pool coincidente responde 307
// con Location al gateway cuyo pool coincide (indice geo→gateways). El
// reenvio es de control: 0 bytes de sesion copiados entre gateways. Seleccionar nunca es un viaje inter-gateway: el endpoint unico no se fragmenta a 100k (S1 A2-01).

// NodeCaps es una entrada del pool local construida desde Redis
// (SMEMBERS del pool propio + HGETALL node:{id}:caps).
type NodeCaps struct {
	ID          string
	BWMbps      uint16
	ISO2        string
	DeviceClass byte
}

// PoolReader es la lectura local del gateway. En produccion cae sobre el
// shard local de Redis; el doble de test registra que teclas se tocaron
// (AC3: 0 queries a pools ajenos durante 1.000 selecciones).
type PoolReader interface {
	Members(poolKey string) ([]string, error)
	Caps(nodeID string) (NodeCaps, bool)
}

// PickFunc elige un nodo del pool coincidente. El rotativo ponderado y el
// sticky viven en sticky.go (T-0904) y se pegan aqui como estrategia.
type PickFunc func(pool []NodeCaps, rnd *rand.Rand) (NodeCaps, bool)

// ErrNoLocalPool: ningun gateway cubre el pais pedido — no hay 307 que
// ofrecer, el contrato responde 404 de enrutado.
var ErrNoLocalPool = errors.New("gw: sin pool coincidente en ninguna parte")

// SelectReq es lo que trae POST /api/v1/sessions: {country, asn?, city?,
// mode, sticky_ttl_s}. El pais manda para el pool (A2-01).
type SelectReq struct {
	ISO2 string
}

// SelectOutcome: nodo admitido, redireccion de control, o error.
type SelectOutcome struct {
	Node        NodeCaps
	Redirect    Redirect
	HasRedirect bool
	Elapsed     time.Duration
}

// Selector selecciona solo del pool propio. SelfAddr permite descartar
// el 307 contra si mismo (el indice dice "tu eres el dueño" y aun asi
// falto el pool: no hay ping-pong).
type Selector struct {
	GWID     string
	SelfAddr string
	Reader   PoolReader
	Index    *GeoIndex
	Strat    PickFunc // nil = uniforme
	Rnd      *rand.Rand
	now      func() time.Time
}

func (s *Selector) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Selector) pick(pool []NodeCaps) (NodeCaps, bool) {
	if len(pool) == 0 {
		return NodeCaps{}, false
	}
	if s.Strat != nil {
		return s.Strat(pool, s.Rnd)
	}
	i := 0
	if s.Rnd != nil {
		i = s.Rnd.Intn(len(pool))
	}
	return pool[i], true
}

// Select: lee el pool local, filtra por el pais del request y elige. Sin
// coincidencia local: 307 al dueño del pool de ese pais (AC2), jamas
// copia de bytes. Devuelve ErrNoLocalPool si el indice no conoce el pais.
func (s *Selector) Select(req SelectReq) (SelectOutcome, error) {
	t0 := s.clock()
	out := func(n NodeCaps, rd Redirect, red bool) (SelectOutcome, error) {
		return SelectOutcome{Node: n, Redirect: rd, HasRedirect: red,
			Elapsed: s.clock().Sub(t0)}, nil
	}
	members, err := s.Reader.Members(PoolKey(s.GWID))
	if err != nil {
		return SelectOutcome{}, err
	}
	var matched []NodeCaps
	for _, id := range members {
		if c, ok := s.Reader.Caps(id); ok && c.ISO2 == req.ISO2 {
			matched = append(matched, c)
		}
	}
	if n, ok := s.pick(matched); ok {
		return out(n, Redirect{}, false)
	}
	if rd, ok := NewRedirect(req.ISO2, s.Index); ok && rd.Location != s.SelfAddr {
		return out(NodeCaps{}, rd, true)
	}
	return SelectOutcome{}, ErrNoLocalPool
}

// RedisPoolReader es el lector de produccion: siempre el shard local del
// gateway (Redis por cercania), nunca pools de otros gateways. Ambas
// inyecciones son las unicas lecturas Redis del plano de seleccion.
type RedisPoolReader struct {
	MembersOf func(poolKey string) ([]string, error) // SMEMBERS gw:pool:{gw}
	Lookup    func(nodeID string) (NodeCaps, bool)   // HGETALL node:{id}:caps cacheado
}

// Members resuelve SMEMBERS del pool local.
func (r *RedisPoolReader) Members(poolKey string) ([]string, error) {
	if r.MembersOf == nil {
		return nil, nil
	}
	return r.MembersOf(poolKey)
}

// Caps delega en la tabla de caps (cache local del gateway).
func (r *RedisPoolReader) Caps(nodeID string) (NodeCaps, bool) {
	if r.Lookup == nil {
		return NodeCaps{}, false
	}
	return r.Lookup(nodeID)
}

// Ensure: el formateador de ids del agent es el mismo que publico el
// HELLO (T-0901), para que el pool y las caps hablen el mismo uuid.
var _ = agent.FormatUUID
