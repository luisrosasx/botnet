package main

import (
	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/internal/backend"
)

// gatewayCRL cablea el CRL en Redis del gateway (T-0803): refresca
// revoked:{serial} cada 55 s para que una revocacion cierre la puerta
// de admision en <60 s sin reiniciar el proceso. La logica vive en
// internal/backend — «sin logica de negocio en el gateway» (§9).
func gatewayCRL(rdb *redis.Client) *backend.CRLCache {
	return backend.NewCRLCache(rdb, backend.DefaultCRLRefresh)
}
