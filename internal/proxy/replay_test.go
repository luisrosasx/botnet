package proxy

import (
	"errors"
	"testing"
	"time"
)

// TestReplayEnVentana: repetir a los 10 s se rechaza; a los 31 s se acepta.
func TestReplayEnVentana(t *testing.T) {
	g := NewReplayGuard()
	base := time.Now()
	g.now = func() time.Time { return base }

	if err := g.Check("tok"); err != nil {
		t.Fatalf("primer uso: %v", err)
	}
	g.now = func() time.Time { return base.Add(10 * time.Second) }
	if err := g.Check("tok"); !errors.Is(err, ErrTokenReplay) {
		t.Fatalf("a los 10 s quiero session_replay, dio %v", err)
	}
	g.now = func() time.Time { return base.Add(31 * time.Second) }
	if err := g.Check("tok"); err != nil {
		t.Fatalf("a los 31 s debe aceptarse: %v", err)
	}
}

// TestReplayTokensDistintos: dos tokens distintos no se estorban.
func TestReplayTokensDistintos(t *testing.T) {
	g := NewReplayGuard()
	if err := g.Check("a"); err != nil {
		t.Fatal(err)
	}
	if err := g.Check("b"); err != nil {
		t.Fatalf("token distinto rechazado: %v", err)
	}
}

// TestVerifyTokenYReplay: el MAC bueno pasa, un byte alterado no.
func TestVerifyTokenYReplay(t *testing.T) {
	secreto := make([]byte, 32)
	id := SessionIdentity{ClientID: "c", TenantID: "t", SessionID: "s", NodeID: "n", TS: time.Now().UTC()}
	tok := SignToken(secreto, id)
	if _, err := VerifyToken(secreto, tok, time.Now()); err != nil {
		t.Fatalf("token bueno: %v", err)
	}
	alterado := tok[:len(tok)-1] + map[bool]string{true: "0", false: "1"}[tok[len(tok)-1] != '0']
	if _, err := VerifyToken(secreto, alterado, time.Now()); err == nil {
		t.Fatal("un byte alterado debe fallar")
	}
}
