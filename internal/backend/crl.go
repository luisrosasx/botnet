package backend

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// CRL en Redis (§6): revoked:{serial} escrito por el backend al revocar
// y refrescado por el gateway cada 55 s. El contrato de producto es
// «revocar cierra la puerta en <60 s»; el margen de 5 s cubre el costo
// del refresco mismo.
const (
	DefaultCRLRefresh = 55 * time.Second
	CRLRejectWindow   = 60 * time.Second
)

// CRLCache es la vista local del gateway sobre revoked:* en Redis.
// Map en vivo + reemplazo atomico por refresco: la consulta de admision
// nunca bloquea ni espera a Redis (A2-14: sin RTT en el path caliente).
type CRLCache struct {
	rdb      *redis.Client
	interval time.Duration

	mu      sync.RWMutex
	serials map[string]bool
}

func NewCRLCache(rdb *redis.Client, refresh time.Duration) *CRLCache {
	return &CRLCache{rdb: rdb, interval: refresh, serials: map[string]bool{}}
}

// PollOnce reconstruye el conjunto desde Redis (SCAN paginado; el CRL de
// una flota de 100k con revocaciones contadas es pequeno).
func (c *CRLCache) PollOnce(ctx context.Context) error {
	found := map[string]bool{}
	var cursor uint64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, "revoked:*", 500).Result()
		if err != nil {
			return fmt.Errorf("backend: crl scan: %w", err)
		}
		for _, k := range keys {
			found[k] = true
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	c.mu.Lock()
	c.serials = found
	c.mu.Unlock()
	return nil
}

// Revoked responde sin tocar Redis: la admision de un stream es local.
func (c *CRLCache) Revoked(serial string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.serials["revoked:"+serial]
}

// Interval devuelve el periodo configurado (el test lo compara con el
// techo de 60 s del contrato).
func (c *CRLCache) Interval() time.Duration { return c.interval }

// PollLoop corre PollOnce cada interval hasta que ctx se cancele; los
// errores se acumulan en el ultimo devuelto por la ultima iteracion.
func (c *CRLCache) PollLoop(ctx context.Context) error {
	tick := time.NewTicker(c.interval)
	defer tick.Stop()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return lastErr
		case <-tick.C:
			lastErr = c.PollOnce(ctx)
		}
	}
}

// MarkRevoked es la escritura canonica del backend (T-0803): SET
// revoked:{serial} 1 EX ttl. ttl cubre la vida restante del certificado
// revocado con margen; tras ella la CRL en Redis puede expirar porque el
// certificado TLS ya murio por TTL propio (90 dias).
func MarkRevoked(ctx context.Context, rdb *redis.Client, serial string, ttl time.Duration) error {
	if err := rdb.Set(ctx, "revoked:"+serial, "1", ttl).Err(); err != nil {
		return fmt.Errorf("backend: mark revoked: %w", err)
	}
	return nil
}
