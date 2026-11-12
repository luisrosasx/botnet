// Test T-1102 (EP-11): consentimiento y baja fechados en la fila del
// nodo. Las cuatro puertas escriben optout_at + fila en leaves; y el
// refrito de firmware no hereda consentimiento: la alta nueva nace
// con consent_at posterior al left_at anterior (A3-10/A3-14).
package services

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func TestStampOptoutFourDoors(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	for _, method := range LeaveMethods {
		nodeID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
		if err := StampOptout(ctx, repo.New(db), nodeID, method, "baja test", "ABCD1234"); err != nil {
			t.Fatalf("method %s: %v", method, err)
		}
		var optout sql.NullTime
		var left, blocked time.Time
		if err := db.QueryRowContext(ctx, `
			SELECT optout_at FROM nodes WHERE node_id = $1::uuid`, nodeID).Scan(&optout); err != nil {
			t.Fatalf("method %s optout_at: %v", method, err)
		}
		if !optout.Valid {
			t.Fatalf("method %s: optout_at sigue NULL en la fila del nodo", method)
		}
		if err := db.QueryRowContext(ctx, `
			SELECT left_at, reprobe_blocked_until FROM leaves
			 WHERE node_id = $1::uuid AND method = $2`, nodeID, method).
			Scan(&left, &blocked); err != nil {
			t.Fatalf("method %s leaves: %v", method, err)
		}
		if !blocked.After(left.Add(11 * 30 * 24 * time.Hour)) {
			t.Fatalf("method %s: el bloqueo de re-clutaje %s no cubre 12 meses desde %s", method, blocked, left)
		}
	}
	if err := StampOptout(ctx, repo.New(db), testenv.SeedNode(t, db, "ES", "Madrid", 2), "telegram", "x", "ABCD1234"); err == nil {
		t.Fatal("metodo fuera del CHECK deberia error")
	}
}

func TestReclutajeNoHeredaConsentimiento(t *testing.T) {
	db := testenv.PG(t)
	ctx := context.Background()
	oldID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	if err := StampOptout(ctx, repo.New(db), oldID, "owner_url", "dueno bajado", "ABCD1234"); err != nil {
		t.Fatal(err)
	}
	var leftCountBefore int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM leaves`).Scan(&leftCountBefore); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // now() transaccional: margen de reloj

	// Refrito de firmware: alta nueva (otro node_id, token reemitido).
	newID := testenv.SeedNode(t, db, "ES", "Madrid", 2)
	var oldConsent, newConsent time.Time
	var leftAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT (SELECT consent_at FROM nodes WHERE node_id = $1::uuid),
		       (SELECT consent_at FROM nodes WHERE node_id = $2::uuid),
		       (SELECT left_at  FROM leaves  WHERE node_id = $1::uuid LIMIT 1)`,
		oldID, newID).Scan(&oldConsent, &newConsent, &leftAt); err != nil {
		t.Fatal(err)
	}
	if !newConsent.After(leftAt) {
		t.Fatalf("el consentimiento del re-clutado (%s) no es posterior a su baja (%s): no se volvio a pedir", newConsent, leftAt)
	}
	if newConsent.Equal(oldConsent) {
		t.Fatal("el alta nueva heredo el consent_at anterior")
	}
	var leftCountAfter int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM leaves`).Scan(&leftCountAfter); err != nil {
		t.Fatal(err)
	}
	if leftCountAfter != leftCountBefore {
		t.Fatalf("el refrito borro historia de leaves: %d → %d", leftCountBefore, leftCountAfter)
	}
}
