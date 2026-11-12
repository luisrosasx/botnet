package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/accounting"
)

// flushNode — POST /v1/nodes/{id}/flush (T-1002): el nodo vierte el
// pending de sus contadores con flush_id. El reenvio del mismo
// flush_id (durable acct.{node_id}.flush re-consumido) responde
// 202 {dedup:true} y NO mueve la suma de usage.
//
// Fuente canónica: `usage` se construye de este flush del nodo;
// `usage.audit` del gateway (NATS) entra solo en conciliación — un
// evento de auditoría perdido no altera la factura.
func flushNode(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if d.FlushSink == nil || d.Dedup == nil {
			return jsonError(c, http.StatusServiceUnavailable, "plano contable apagado")
		}
		env, err := accounting.DecodeEnvelope(c.Body())
		if err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		if env.NodeID != c.Params("node_id") {
			return jsonError(c, http.StatusBadRequest, "node_id no coincide con el envelope")
		}
		deduped, err := accounting.ApplyFlush(env, d.FlushSink, d.Dedup)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		if deduped {
			return c.Status(http.StatusAccepted).JSON(fiber.Map{"dedup": true, "flush_id": env.FlushID})
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"applied":  true,
			"flush_id": env.FlushID,
			"sessions": len(env.Sessions),
			"bytes":    accounting.FlushTotalBytes(env),
		})
	}
}

// RedisDedup es el DedupStore de produccion sobre el SetNX de Redis:
// «primera vez que veo este flush_id» es exactamente un SETNX. TTL de
// 48 h cubre de sobra la ventana de redelivery del durable.
type RedisDedup struct {
	RDB *redis.Client
	TTL time.Duration
}

// MarkSeen implementa accounting.DedupStore.
func (r RedisDedup) MarkSeen(flushID string) (bool, error) {
	ttl := r.TTL
	if ttl <= 0 {
		ttl = 48 * time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.RDB.SetNX(ctx, "flush:"+flushID, "1", ttl).Result()
}

// FlushRepo vierte un flush dedup en el DDL contable: los contadores
// van a sessions.bytes_in/out por session_id y el total del flush se
// acumula en la fila diaria usage(day, node_id) — el grano del
// rollup, nunca por minuto (leccion A2-07).
type FlushRepo struct{ Repo *repo.Data }

// ApplyFlush implementa accounting.FlushSink.
func (f FlushRepo) ApplyFlush(env accounting.FlushEnvelope) error {
	ctx := context.Background()
	var total int64
	for _, s := range env.Sessions {
		sid, err := strconv.ParseInt(s.SessionID, 10, 64)
		if err != nil {
			return err
		}
		if err := f.Repo.AddSessionBytes(ctx, sid, int64(s.BytesIn), int64(s.BytesOut)); err != nil {
			return err
		}
	}
	total = accounting.FlushTotalBytes(env)
	day := env.At.UTC().Format("2006-01-02")
	return f.Repo.UpsertUsage(ctx, day, env.NodeID, total)
}
