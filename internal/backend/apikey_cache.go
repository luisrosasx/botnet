package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrKeyUnknown: la api key no existe (hash sin fila).
var ErrKeyUnknown = errors.New("backend: api key desconocida")

// ErrKeyRevoked: la fila existe pero el tenant la revoco.
var ErrKeyRevoked = errors.New("backend: api key revocada")

// ErrRedisDown: en modo degradado no hay admision a ciegas (A3-X3).
var ErrRedisDown = errors.New("backend: redis caido: sin admision de sesiones")

// KeyInfo es la proyeccion que el gateway/cache necesita de api_keys.
// KeyHash solo viaja del repo al cache (warmup); el cliente nunca lo
// necesita: Validate lo recalcula desde la key en claro.
type KeyInfo struct {
	KeyID     int64  `json:"key_id"`
	KeyHash   string `json:"key_hash,omitempty"`
	TenantID  string `json:"tenant_id"`
	Mode      string `json:"mode"`
	StickyTTL int    `json:"sticky_ttl_s"`
	Status    string `json:"status"`
}

// KeyStore es la cara de Postgres que usa el cache (repo lo implementa).
// LookupKey debe ser UNA query; ListActiveKeys alimenta el warmup (T-0804).
type KeyStore interface {
	LookupKey(ctx context.Context, hash string) (KeyInfo, error)
	ListActiveKeys(ctx context.Context) ([]KeyInfo, error)
}

// ApiKeyCache: «validar contra Redis» es literal (A3-07) — la clave
// apikey:{sha256} vive en Redis con TTL, el warmup la calienta al
// arrancar y Postgres solo es fuente de verdad fria: una validacion fria
// son exactamente 1 query y las siguientes, 0.
type ApiKeyCache struct {
	rdb    *redis.Client
	store  KeyStore
	ttl    time.Duration
	warmed atomic.Bool
	missTTL time.Duration // cache negativo: claves basura no martillan Postgres
}

func NewApiKeyCache(rdb *redis.Client, store KeyStore, ttl time.Duration) *ApiKeyCache {
	return &ApiKeyCache{rdb: rdb, store: store, ttl: ttl, missTTL: 30 * time.Second}
}

func apiKey(hash string) string { return "apikey:" + hash }

// HashKey: sha256 hex de la api key en claro; la key en claro nunca se
// guarda ni se loguea.
func HashKey(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// Validate resuelve la api key: Redis primero; en frio, exactamente una
// query y SETEX apikey:{sha256}. ErrKeyUnknown/ErrKeyRevoked tipan el
// 403; cualquier otro error es de infraestructura (503, no 403).
func (c *ApiKeyCache) Validate(ctx context.Context, plain string) (KeyInfo, error) {
	hash := HashKey(plain)
	raw, err := c.rdb.Get(ctx, apiKey(hash)).Result()
	if err == nil {
		var info KeyInfo
		if jerr := json.Unmarshal([]byte(raw), &info); jerr != nil {
			return KeyInfo{}, fmt.Errorf("backend: cache apikey corrupto: %w", jerr)
		}
		switch info.Status {
		case "missing":
			return KeyInfo{}, ErrKeyUnknown
		case "revoked":
			return KeyInfo{}, ErrKeyRevoked
		}
		return info, nil
	}
	if err != redis.Nil {
		return KeyInfo{}, fmt.Errorf("backend: cache apikey: %w", err)
	}
	info, err := c.store.LookupKey(ctx, hash) // unica ida a Postgres
	if errors.Is(err, ErrKeyUnknown) {
		c.rdb.Set(ctx, apiKey(hash), `{"status":"missing"}`, c.missTTL).Err()
		return KeyInfo{}, ErrKeyUnknown
	}
	if err != nil {
		return KeyInfo{}, err
	}
	if info.Status != "active" {
		info.Status = "revoked"
	}
	buf, _ := json.Marshal(info)
	c.rdb.Set(ctx, apiKey(hash), buf, c.ttl).Err()
	if info.Status == "revoked" {
		return info, ErrKeyRevoked
	}
	return info, nil
}

// Warmup carga las keys activas al arrancar el gateway; /healthz no pasa
// hasta que termina (T-0804 AC2). Un fallo aqui es error de arranque:
// medio cacheado vale menos que no arrancar.
func (c *ApiKeyCache) Warmup(ctx context.Context) error {
	keys, err := c.store.ListActiveKeys(ctx)
	if err != nil {
		return fmt.Errorf("backend: warmup apikeys: %w", err)
	}
	pipe := c.rdb.TxPipeline()
	for _, k := range keys {
		buf, _ := json.Marshal(k)
		pipe.Set(ctx, apiKey(k.KeyHash), buf, c.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("backend: warmup pipeline: %w", err)
	}
	c.warmed.Store(true)
	return nil
}

// Warmed dice si el cache esta cargado (gate de /healthz).
func (c *ApiKeyCache) Warmed() bool { return c.warmed.Load() }
