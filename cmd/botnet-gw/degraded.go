package main

import (
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/internal/backend"
)

// RedisDownStatus: el contrato del modo degradado (T-0806) — sin Redis
// el gateway/backend no admite sesiones nuevas (503) y no corta las
// vivas: el plano de datos jamas depende de Redis por byte.
const RedisDownStatus = 503

// PresenceTTL es el TTL de node:{id}:live (§9): la presencia se
// re-anuda por heartbeat en <=180 s tras recuperar Redis, sin
// re-enrolls.
const PresenceTTL = 180 * time.Second

// gatewayRedisWatch monta la vigilia de Redis del gateway: ping
// periodico, puerta de admision y reconstruccion de
// tenant:{t}:gb_used desde Postgres al recuperar (sobre-cobro dentro de
// la tolerancia de 0,5 %).
func gatewayRedisWatch(rdb *redis.Client, rb backend.SessionBytes, period string, tenants func() []string) *backend.RedisWatch {
	return backend.NewRedisWatch(rdb, rb, period, tenants)
}
