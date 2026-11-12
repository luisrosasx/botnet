// ratelimit_test.go — T-0706 AC2 en el doble: 61 requests en la
// ventana de 60 s -> exactamente 60 pasan y >= 1 da 429; la ventana
// se reabre al pasar el reloj. RedisWindow usa el motor portable de
// testenv (sin motor, skip).
package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestFixedWindowSesentaUNo(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	f := NewFixedWindow(LeaveRateLimitPerWindow, LeaveWindow)
	f.SetClock(func() time.Time { return now })

	ctx := context.Background()
	allowed, blocked := 0, 0
	for i := 0; i < 61; i++ {
		ok, err := f.Allow(ctx, LeaveRateKey("10.0.0.1"))
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			allowed++
		} else {
			blocked++
		}
	}
	if allowed != 60 || blocked < 1 {
		t.Errorf("61 requests: %d pasan, %d bloqueados — want 60/≥1", allowed, blocked)
	}

	// Ventana nueva: el reloj cruza el borde y la clave vuelve a
	// admitir (sin sleeps: reloj inyectable).
	now = now.Add(LeaveWindow)
	if ok, _ := f.Allow(ctx, LeaveRateKey("10.0.0.1")); !ok {
		t.Error("tras 60 s la ventana debe reabrirse")
	}
	// Claves distintas no comparten ventana (por IP, AC2).
	other := NewFixedWindow(1, time.Minute)
	if ok, _ := other.Allow(ctx, LeaveRateKey("10.0.0.2")); !ok {
		t.Error("primera request de otra IP bloqueada")
	}
	if ok, _ := other.Allow(ctx, LeaveRateKey("10.0.0.2")); ok {
		t.Error("segunda request de la misma IP debio bloquearse")
	}
	if ok, _ := other.Allow(ctx, LeaveRateKey("10.0.0.3")); !ok {
		t.Error("otra IP no paga la ventana ajena")
	}
}

func TestRedisWindowConMotorPortable(t *testing.T) {
	rdb := testenv.Redis(t) // redis 7.4.3 portable en ~/tools; sin motor, skip
	w := &RedisWindow{RDB: rdb, Limit: 2, Window: 2 * time.Second}
	ctx := context.Background()
	key := "rate:leave:127.0.0.1-test"
	defer rdb.Del(ctx, key)

	for i := 0; i < 2; i++ {
		if ok, err := w.Allow(ctx, key); err != nil || !ok {
			t.Fatalf("request %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, err := w.Allow(ctx, key); err != nil || ok {
		t.Errorf("request 3 con limite 2: ok=%v err=%v, want bloqueado", ok, err)
	}
	ttl := rdb.TTL(ctx, key).Val()
	if ttl <= 0 {
		t.Errorf("la clave no tiene expiracion de ventana: ttl=%v", ttl)
	}
}
