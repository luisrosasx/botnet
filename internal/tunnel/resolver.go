// Package tunnel es el túnel TLS/HTTP2 propio (BLUEPRINT §5): una conexión
// larga saliente por tramo que lleva control y streams de sesión. EP-14
// aporta el resolver DNS de destinos de control (A2-10: mover el backend de
// IP es un registro DNS, no un release de 4,7 MB) y la dispersión de
// destino multi-SNI con jitter de puerto (A1-13) para que el DPI del enlace
// no correlacione cadencia con un único destino.
package tunnel

import (
	"errors"
	"fmt"
	"hash/fnv"
	"time"
)

// ErrNoDNS: primer arranque sin DNS y sin destino previo. El agente no entra
// en pánico: el error se propaga al supervisor (modo solo-control) y se
// reintenta; el log nunca debe contener "resolver fatal".
var ErrNoDNS = errors.New("tunnel: resolver sin destino")

// LookupFunc resuelve un host a destinos "host:port" o IPs. En produccion es
// net.Resolver.LookupHost; en tests, una zona DNS fixture mutable.
type LookupFunc func(host string) ([]string, error)

// Resolver re-resuelve el destino de control con TTL + jitter por nodo y
// conserva el ultimo destino valido cuando el DNS cae (criterio 3: reintento
// periodico < 5 min, cero panicos).
type Resolver struct {
	Host     string
	Look     LookupFunc
	TTL      time.Duration
	Jitter   time.Duration // maximo del jitter determinista por Seed
	Seed     string        // identidad del nodo: node_id o mac
	MaxRetry time.Duration // techo de reintento con DNS caido (< 5 min)

	Now func() time.Time

	dest      string
	expires   time.Time
	nextRetry time.Time
	retries   int
}

func NewResolver(host string, look LookupFunc, ttl, jitter time.Duration, seed string) *Resolver {
	return &Resolver{
		Host:     host,
		Look:     look,
		TTL:      ttl,
		Jitter:   jitter,
		Seed:     seed,
		MaxRetry: 3 * time.Minute, // < 5 min exigido por el criterio
		Now:      time.Now,
	}
}

// jitterFor deriva el desfase del seed: dos nodos con el mismo TTL nunca
// re-resuelven en el mismo tick (evita la avalancha del re-perfilado DNS).
func (r *Resolver) jitterFor() time.Duration {
	if r.Jitter <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(r.Seed))
	return time.Duration(h.Sum64() % uint64(r.Jitter))
}

// Resolve devuelve el destino activo. Si la entrada no ha expirado, sirve
// cache; si expiro o hay DNS caido pendiente, reintenta. Con error de DNS y
// destino previo, conserva el ultimo valido y programa un reintento <=
// MaxRetry (sin propagar error: el plano de control sigue vivo).
func (r *Resolver) Resolve() (string, error) {
	now := r.Now()
	if r.dest != "" && now.Before(r.expires) &&
		(r.nextRetry.IsZero() || now.Before(r.nextRetry)) {
		return r.dest, nil
	}
	ips, err := r.Look(r.Host)
	if err != nil || len(ips) == 0 {
		if r.dest != "" {
			r.nextRetry = now.Add(r.MaxRetry)
			if r.MaxRetry > 5*time.Minute {
				r.nextRetry = now.Add(5 * time.Minute)
			}
			return r.dest, nil // ultimo destino valido conservado
		}
		r.retries++
		r.nextRetry = now.Add(r.MaxRetry)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoDNS, err)
		}
		return "", ErrNoDNS
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(r.Seed))
	dest := ips[int(h.Sum64())%len(ips)]
	r.dest = dest
	r.expires = now.Add(r.TTL + r.jitterFor())
	r.nextRetry = time.Time{}
	r.retries = 0
	return dest, nil
}

// Dest devuelve el ultimo destino valido (vacio si nunca hubo).
func (r *Resolver) Dest() string { return r.dest }

// RetryIn es cuanto falta para el proximo reintento con DNS caido; 0 si esta
// sano. El criterio 3 lo aserta < 5 min.
func (r *Resolver) RetryIn() time.Duration {
	if r.nextRetry.IsZero() {
		return 0
	}
	d := r.Now().Sub(r.nextRetry)
	if d < 0 {
		d = -d
	}
	return d
}

// Expiry es cuando caduca el destino actual (TTL + jitter del nodo).
func (r *Resolver) Expiry() time.Time { return r.expires }
