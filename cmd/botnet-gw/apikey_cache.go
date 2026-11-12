package main

import (
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/internal/backend"
)

// gatewayApiKeyCache: validacion de api keys contra apikey:{sha256} en
// Redis con warmup al arrancar (T-0804, A3-07). Postgres solo es la
// fuente fria: la primera validacion de una key son exactamente 1 query
// y las siguientes 0. TTL fijo 300 s del contrato Redis de §9.
func gatewayApiKeyCache(rdb *redis.Client, store backend.KeyStore) *backend.ApiKeyCache {
	return backend.NewApiKeyCache(rdb, store, 300*time.Second)
}
