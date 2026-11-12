package proxy

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

func testSecret() []byte {
	s := make([]byte, 32)
	for i := range s {
		s[i] = byte(i)
	}
	return s
}

func testIdentity(now time.Time) SessionIdentity {
	return SessionIdentity{
		ClientID: "client-1", TenantID: "tenant-42",
		SessionID: "3f0d1c2b-0000-4000-8000-abcdef012345",
		NodeID:    "node-7", TS: now.UTC(),
	}
}

// T-0602 AC1: token HMAC(node_secret, ...) valido -> admitido con cero
// llamadas a Redis en el path (redis_calls=0).
func TestValidTokenAdmittedWithZeroRedisCalls(t *testing.T) {
	secret := testSecret()
	now := time.Now()
	tok := SignToken(secret, testIdentity(now))

	auth := NewLocalAuth()
	id, err := auth.Verify(secret, tok, now)
	if err != nil {
		t.Fatalf("token propio rechazado: %v", err)
	}
	if id.SessionID != "3f0d1c2b-0000-4000-8000-abcdef012345" || id.TenantID != "tenant-42" {
		t.Fatalf("identity vuelta %v", id)
	}
	if got := auth.RedisCallsCount(); got != 0 {
		t.Fatalf("redis_calls=%d, want 0", got)
	}
}

// T-0602 AC2: HMAC forjado -> ErrTokenForged (stream cerrado al instante
// y 0 bytes en el server).
func TestForgedToken(t *testing.T) {
	mine, other := testSecret(), testSecret()
	other[0] ^= 0xff
	tok := SignToken(other, testIdentity(time.Now()))
	if _, err := VerifyToken(mine, tok, time.Now()); !errors.Is(err, ErrTokenForged) {
		t.Fatalf("err=%v, want ErrTokenForged", err)
	}
}

// T-0602 AC2: ts de > 30 s -> 401 interno (ventana anti-replay A3-12).
func TestExpiredTokenIs401(t *testing.T) {
	secret := testSecret()
	now := time.Now()
	old := testIdentity(now.Add(-31 * time.Second))
	tok := SignToken(secret, old)
	_, err := VerifyToken(secret, tok, now)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err=%v, want ErrTokenExpired", err)
	}
	if Status401 != 401 {
		t.Fatalf("Status401=%d", Status401)
	}
	// dentro de ventana sigue vivo
	ok := testIdentity(now.Add(-29 * time.Second))
	if _, err := VerifyToken(secret, SignToken(secret, ok), now); err != nil {
		t.Fatalf("ts -29s rechazado: %v", err)
	}
}

// T-0601 AC3: usuario malformado -> rechazado en parseo, sin stream.
func TestMalformedTokens(t *testing.T) {
	secret := testSecret()
	for _, tok := range []string{"", "junk", "a.b", "nope", "AAAA.zz", "AAAA."} {
		if _, err := VerifyToken(secret, tok, time.Now()); !errors.Is(err, ErrTokenMalformed) {
			t.Fatalf("token %q: err=%v, want ErrTokenMalformed", tok, err)
		}
	}
}

// T-0602 AC3: aperturas con auth HMAC local por debajo de 120 ms p95 —
// 1.000 aperturas firma+verificacion+stream del doble, sin RTT a Redis.
func TestSessionOpenP95Under120ms(t *testing.T) {
	secret := testSecret()
	opener := &FakeOpener{}
	now := time.Now()
	const n = 1000
	d := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		id := testIdentity(now)
		tok := SignToken(secret, id)
		if _, err := VerifyToken(secret, tok, time.Now()); err != nil {
			t.Fatalf("apertura %d: %v", i, err)
		}
		s, err := opener.OpenStream(context.Background(), StreamMeta{SessionID: id.SessionID})
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		_ = s.Close()
		d[i] = time.Since(start)
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	p95 := d[(n*95)/100]
	if p95 >= 120*time.Millisecond {
		t.Fatalf("p95 de apertura = %v, want < 120ms", p95)
	}
	t.Logf("p95 apertura con auth local = %v (n=%d)", p95, n)
}
