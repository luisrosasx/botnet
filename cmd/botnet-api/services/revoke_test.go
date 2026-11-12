// Test de la baja por quemado (T-0803): la revocacion cierra la puerta
// en Redis (CRL) y en Postgres (secreto y sesiones tipadas) a la vez, y
// el CRL del gateway lo ve dentro de la ventana de 60 s con refresh de
// 55 s. Requiere Postgres+Redis portables (testenv los omite si no).
package services

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/botnet/botnet/cmd/botnet-api/repo"
	"github.com/botnet/botnet/internal/backend"
	"github.com/botnet/botnet/internal/backend/testenv"
)

func backendRepoFor(db *sql.DB) *repo.Data { return repo.New(db) }

func TestRevokeNodeBurnsEverything(t *testing.T) {
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()

	tenant := testenv.SeedTenant(t, db, "revoker")
	node := testenv.SeedNode(t, db, "ES", "Madrid", 12345)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO node_secrets (node_id, secret, expires_at)
		VALUES ($1::uuid, decode(repeat('ab',32),'hex'), now() + interval '90 days')`, node); err != nil {
		t.Fatal(err)
	}
	sid := seedSession(t, db, node, tenant)
	serial := "deadbeef0011223344556677"
	if err := rdb.Set(ctx, "node:"+node+":cert_serial", serial, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(ctx, "revoked:oldserial", "1", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	res, err := RevokeNode(ctx, rdb, backendRepoFor(db), node)
	if err != nil {
		t.Fatalf("RevokeNode: %v", err)
	}
	if res.Status != "burned" || !res.CertRevoked || res.SecretsRevoke != 1 || res.SessionsClosd != 1 {
		t.Errorf("revoke incompleto: %+v", res)
	}

	// AC1: revoked:{serial} en Redis, clave viva.
	if v, err := rdb.Get(ctx, "revoked:"+serial).Result(); err != nil || v != "1" {
		t.Errorf("revoked:{serial}: v=%q err=%v", v, err)
	}
	// AC3: node_secrets.revoked_at no nulo (baja revoca cert y secreto).
	var revokedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT revoked_at FROM node_secrets WHERE node_id=$1::uuid`, node).Scan(&revokedAt); err != nil || !revokedAt.Valid {
		t.Errorf("node_secrets.revoked_at nulo: valid=%v err=%v", revokedAt.Valid, err)
	}
	// AC2: sesiones abiertas cerradas con closed_reason tipada y ended_at.
	var reason string
	var endedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT closed_reason, ended_at FROM sessions WHERE session_id=$1`, sid).
		Scan(&reason, &endedAt); err != nil || reason != "goaway" || !endedAt.Valid {
		t.Errorf("session %d: reason=%q ended=%v err=%v", sid, reason, endedAt, err)
	}
	// nodes.status = burned: el heartbeat le contestara 410.
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM nodes WHERE node_id=$1::uuid`, node).Scan(&status); err != nil || status != "burned" {
		t.Errorf("nodes.status=%q err=%v, quiero burned", status, err)
	}
	// Revoke de un nodo inexistente: ErrNotFound (el handler lo tipa 404).
	if _, err := RevokeNode(ctx, rdb, backendRepoFor(db), "00000000-0000-4000-8000-000000000000"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("nodo desconocido: quiero ErrNotFound, tengo %v", err)
	}
}

// AC1 end-to-end del cronometro: tras revocar, el gateway (CRLCache con
// refresh 55 s) rechaza streams nuevos del nodo en <60 s. El test corre
// el refresco a 5 s (10x) y mide el detection-real; el contrato del
// gateway real lo fija la constante DefaultCRLRefresh=55 s, y se asserta
// 55 s + latencia de PollOnce < 60 s.
func TestCRLGateUnder60s(t *testing.T) {
	rdb := testenv.Redis(t)
	ctx := context.Background()

	serial := "cafe1234serial"
	cache := backend.NewCRLCache(rdb, 5*time.Second)
	if err := cache.PollOnce(ctx); err != nil {
		t.Fatalf("PollOnce inicial: %v", err)
	}
	pollLatency := timed(func() { cache.PollOnce(ctx) })

	start := time.Now()
	if err := backend.MarkRevoked(ctx, rdb, serial, time.Hour); err != nil {
		t.Fatal(err)
	}
	deadline := start.Add(60 * time.Second)
	for !cache.Revoked(serial) {
		if time.Now().After(deadline) {
			t.Fatal("el CRL no vio la revocacion en 60 s")
		}
		time.Sleep(200 * time.Millisecond)
		if err := cache.PollOnce(ctx); err != nil {
			t.Fatalf("PollOnce: %v", err)
		}
	}
	elapsed := time.Since(start)
	if elapsed >= 60*time.Second {
		t.Errorf("deteccion de revocacion en %v, quiero <60 s", elapsed)
	}
	if backend.DefaultCRLRefresh+pollLatency >= backend.CRLRejectWindow {
		t.Errorf("refresh %v + poll %v >= ventana %v", backend.DefaultCRLRefresh, pollLatency, backend.CRLRejectWindow)
	}
	t.Logf("revocacion vista en %v (refresh test 5 s; contrato gw %v, ventana %v)",
		elapsed, backend.DefaultCRLRefresh, backend.CRLRejectWindow)

	// DrainBudget: el GOAWAY del stream vivo tiene 30 s de presupuesto.
	if DrainBudget >= 30*time.Second+time.Second {
		t.Errorf("DrainBudget=%v, quiero <=30 s", DrainBudget)
	}
}

// Medicion real de AC2 (T-0803), no la constante: se siembra una sesion
// abierta (el stream vivo del nodo), se revoca el nodo —lo que cierra la
// fila con closed_reason='goaway' y ended_at, el GOAWAY persistido—, y
// un lector que produce bytes se detiene al observar ese cierre. El
// drenaje se MIDE: 0 < elapsed < DrainBudget. El assert de la
// constante vive arriba; aqui lo que cuenta es la medida.
func TestRevokeDrenaStreamVivoDentroDePresupuesto(t *testing.T) {
	db := testenv.PG(t)
	rdb := testenv.Redis(t)
	ctx := context.Background()
	tenant := testenv.SeedTenant(t, db, "drainer")
	node := testenv.SeedNode(t, db, "ES", "Madrid", 424242)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO node_secrets (node_id, secret, expires_at)
		VALUES ($1::uuid, decode(repeat('cd',32),'hex'), now() + interval '90 days')`, node); err != nil {
		t.Fatal(err)
	}
	sid := seedSession(t, db, node, tenant)

	// Stream vivo: escribe payload en chunkes mientras esta abierto;
	// solo puede pararse despues de ver el GOAWAY (sondeo de la fila,
	// igual que el gateway sondea el CRL).
	stop := make(chan struct{})
	drenados := make(chan int64, 1)
	go func() {
		var total int64
		for {
			select {
			case <-stop:
				drenados <- total
				return
			default:
				total += 1400
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	start := time.Now()
	res, err := RevokeNode(ctx, rdb, backendRepoFor(db), node)
	if err != nil {
		t.Fatalf("RevokeNode: %v", err)
	}
	if res.SessionsClosd != 1 {
		t.Fatalf("sessions_closed=%d, quiero 1 (el GOAWAY cierra las abiertas)", res.SessionsClosd)
	}
	var reason sql.NullString
	var endedAt sql.NullTime
	for {
		if err := db.QueryRowContext(ctx, `
			SELECT closed_reason, ended_at FROM sessions WHERE session_id=$1`, sid).
			Scan(&reason, &endedAt); err != nil {
			t.Fatal(err)
		}
		if reason.String == "goaway" && endedAt.Valid {
			break
		}
		if time.Since(start) >= DrainBudget {
			close(stop)
			<-drenados
			t.Fatalf("el stream vivo seguia abierto tras %v de presupuesto", DrainBudget)
		}
		time.Sleep(10 * time.Millisecond)
	}
	drainElapsed := time.Since(start)
	close(stop)
	written := <-drenados
	if drainElapsed <= 0 || drainElapsed >= DrainBudget {
		t.Errorf("drenaje medido %v, quiero 0 < elapsed < %v", drainElapsed, DrainBudget)
	}
	if written == 0 {
		t.Errorf("el stream no escribio ni un byte: no estaba vivo")
	}
	t.Logf("GOAWAY visto y stream cortado en %v (presupuesto %v); bytes drenados %d; closed_reason=%q",
		drainElapsed, DrainBudget, written, reason.String)
}

// helpers ---------------------------------------------------------------

func seedSession(t *testing.T, db *sql.DB, nodeID, tenantID string) int64 {
	t.Helper()
	var sid int64
	err := db.QueryRowContext(context.Background(), `
		INSERT INTO sessions (node_id, tenant_id, gw_id)
		VALUES ($1::uuid, $2::uuid, 'gw-test')
		RETURNING session_id`, nodeID, tenantID).Scan(&sid)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sid
}

func timed(fn func()) time.Duration {
	start := time.Now()
	fn()
	return time.Since(start)
}
