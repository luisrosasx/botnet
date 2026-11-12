package main

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Drenaje de gateway y censo de egress (EP-09 T-0908, cierra A2-06):
// `redis-cli SET gw:drain:{gw_id} 1` detiene admisiones, el gateway emite
// GOAWAY progresivo y las sesiones abiertas del gateway quedan a 0 en
// 60 s (ni un huerfano); el rdv re-asigna el 100 % de los nodos
// drenados. Apagar un gateway un martes a las 23:40 es un drenaje de
// 60 s. El egress por gateway se census-orea por provedor (herencia
// A2-06/A2-16).

// DrainBudget: presupuesto del drenaje completo (§10: drena con GOAWAY).
const DrainBudget = 60 * time.Second

// DrainKey: la bandera de drenaje en Redis. "1" = drenando.
func DrainKey(gwID string) string { return "gw:drain:" + gwID }

// ErrAdmissionDrained: el contrato sessions responde 503 suave mientras
// el gateway drena (el cliente reintenta; el rdv ya re-asigna los nodos).
var ErrAdmissionDrained = errors.New("gw: gateway en drenaje")

// SessionStore es el espejo de `sessions` que usa el gateway: en
// produccion son los services por NATS/API interna (SQL: ended_at IS
// NULL AND gw_id=$1); el doble de test lo resuelve en memoria y el
// assert psql del criterio 1 cae sobre la misma forma.
type SessionStore interface {
	OpenSessions(gwID string) []string
	EndSession(ctx context.Context, sessionID string) error
}

// GoawayFn emite GOAWAY_SESSION(session_id) por el tunel (§7).
type GoawayFn func(sessionID string)

// Drainer orquesta la bandera, el GOAWAY progresivo y el presupuesto.
type Drainer struct {
	GWID   string
	RDB    *redis.Client
	Store  SessionStore
	Goaway GoawayFn
	// Step: sesiones cerradas por tick (progresivo, no un portazo).
	Step int
}

// Draining: 0 admisiones nuevas mientras la bandera valga "1".
func (d *Drainer) Draining(ctx context.Context) bool {
	v, err := d.RDB.Get(ctx, DrainKey(d.GWID)).Result()
	return err == nil && v == "1"
}

// AdmitNew: la puerta de admision del contrato sessions.
func (d *Drainer) AdmitNew(ctx context.Context) error {
	if d.Draining(ctx) {
		return ErrAdmissionDrained
	}
	return nil
}

// Tick: GOAWAY y cierre de hasta Step sesiones abiertas. Devuelve las
// que quedan (huérfanos = sesiones abiertas tras el drenaje).
func (d *Drainer) Tick(ctx context.Context) (int, error) {
	open := d.Store.OpenSessions(d.GWID)
	step := d.Step
	if step <= 0 {
		step = 50
	}
	for i, sid := range open {
		if i >= step {
			break
		}
		if d.Goaway != nil {
			d.Goaway(sid)
		}
		if err := d.Store.EndSession(ctx, sid); err != nil {
			return len(open) - i, err
		}
	}
	return len(d.Store.OpenSessions(d.GWID)), nil
}

// DrainOnce: bucle de ticks dentro del presupuesto de 60 s. Devuelve el
// numero de sesiones vivas al final (0 = sin huerfanos) y error si el
// presupuesto se agoto con sesiones dentro.
func (d *Drainer) DrainOnce(ctx context.Context) (int, error) {
	deadline := time.Now().Add(DrainBudget)
	remaining := -1
	for {
		select {
		case <-ctx.Done():
			return remaining, ctx.Err()
		default:
		}
		n, err := d.Tick(ctx)
		remaining = n
		if err != nil || remaining == 0 {
			return remaining, err
		}
		if time.Now().After(deadline) {
			return remaining, errors.New("gw: drenaje sin cerrar sesiones en el presupuesto de 60 s")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ReassignDrained: el rdv re-asigna el 100 % de los nodos drenados a
// otros gateways (candidatos = mismo pais, via GeoMatcher T-0902).
// Devuelve nodo→gateway destino y cuantos se quedaron sin destino
// (exigido 0 en el fixture de 250).
func ReassignDrained(nodes []string, pick func(node string) string) (dest map[string]string, unassigned int) {
	dest = make(map[string]string, len(nodes))
	for _, n := range nodes {
		gw := pick(n)
		if gw == "" {
			unassigned++
			continue
		}
		dest[n] = gw
	}
	return dest, unassigned
}

// SustainedEgressMbps: dimensionado del edge — 250 nodos por gateway por
// el egress medio medido por nodo. El umbral del censo es >= 75 Mbps
// sostenidos (criterio 3); deploy/gw/egress_census.md registra la cifra
// por provedor.
func SustainedEgressMbps(nodes int, perNodeMbps float64) float64 {
	return float64(nodes) * perNodeMbps
}
