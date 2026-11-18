package recruit

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/migrations"
)

// testStore abre una base scratch migrada y la borra al terminar.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatalf("DATABASE_URL sin definir: la prueba no se omite")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	defer admin.Close()
	name := fmt.Sprintf("botnet_recruit_u_%d", time.Now().UnixNano()%1e9)
	// El motor portable suelta dial-timeout: reintento acotado en DDL
	// administrativo y en la migracion (Apply es idempotente por version).
	var cerr error
	for i := 0; i < 5; i++ {
		if _, cerr = admin.Exec("CREATE DATABASE " + name); cerr == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
	}
	if cerr != nil {
		t.Fatalf("create: %v", cerr)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
	dsn2 := replaceDBTest(dsn, name)
	var st *Store
	for i := 0; i < 5; i++ {
		st, err = Open(dsn2, 4)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var merr error
	for i := 0; i < 5; i++ {
		if merr = migrations.Apply(st.db); merr == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 400 * time.Millisecond)
	}
	if merr != nil {
		t.Fatalf("migrate: %v", merr)
	}
	return st
}

func replaceDBTest(dsn, db string) string {
	if i := strings.LastIndex(dsn, "/"); i >= 0 {
		if j := strings.Index(dsn[i:], "?"); j > 0 {
			return dsn[:i] + "/" + db + dsn[i+j:]
		}
		return dsn[:i] + "/" + db
	}
	return dsn
}

func seedCandidates(t *testing.T, st *Store, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := st.db.Exec(`
			INSERT INTO recruit_candidates (ip, port, state)
			VALUES ($1, $2, 'eligible')`,
			fmt.Sprintf("203.0.113.%d", (i%250)+1), 20000+i); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
}

// TestClaimAtomic: dos reclamadores concurrentes no comparten una fila.
func TestClaimAtomic(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 40)

	a, err := st.Claim(ctx, "sweep", "w-a", 20)
	if err != nil {
		t.Fatalf("claim a: %v", err)
	}
	b, err := st.Claim(ctx, "sweep", "w-b", 20)
	if err != nil {
		t.Fatalf("claim b: %v", err)
	}
	if len(a) != 20 || len(b) != 20 {
		t.Fatalf("reparto desigual: %d y %d (quiero 20 y 20)", len(a), len(b))
	}
	seen := map[int64]bool{}
	for _, c := range a {
		seen[c.CandidateID] = true
	}
	solape := 0
	for _, c := range b {
		if seen[c.CandidateID] {
			solape++
		}
	}
	if solape != 0 {
		t.Fatalf("filas compartidas entre reclamadores=%d", solape)
	}
	// Tercer claim no debe dar nada: todo esta bajo lease.
	c, err := st.Claim(ctx, "sweep", "w-c", 5)
	if err != nil {
		t.Fatalf("claim c: %v", err)
	}
	if len(c) != 0 {
		t.Fatalf("claim sobre filas en lease devolvio %d", len(c))
	}
}

// TestFencing: un token viejo no escribe.
func TestFencing(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	got, err := st.Claim(ctx, "sweep", "w-1", 1)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim: %v %d", err, len(got))
	}
	c := got[0]
	if err := st.Transition(ctx, c.CandidateID, c.LeaseOwner, c.LeaseToken, StateBannered, "", "w-1"); err != nil {
		t.Fatalf("transicion buena: %v", err)
	}
	// Mismo token otra vez: la fila ya no esta bajo lease con ese token.
	if err := st.Transition(ctx, c.CandidateID, c.LeaseOwner, c.LeaseToken, StateAssessed, "", "w-1-zombi"); err == nil {
		t.Fatal("un token ya consumido debe dar lease_stale")
	}
}

// TestTransitionAudit: cada transicion deja su fila de auditoria.
func TestTransitionAudit(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	got, _ := st.Claim(ctx, "sweep", "w-1", 1)
	c := got[0]
	if err := st.Transition(ctx, c.CandidateID, c.LeaseOwner, c.LeaseToken, StateDeferred, "no_cred", "w-1"); err != nil {
		t.Fatalf("deferred: %v", err)
	}
	var n int
	var actor string
	if err := st.db.QueryRow(`SELECT count(*), max(actor) FROM recruit_candidate_events WHERE candidate_id=$1`, c.CandidateID).Scan(&n, &actor); err != nil {
		t.Fatalf("eventos: %v", err)
	}
	if n != 1 || actor != "w-1" {
		t.Fatalf("auditoria n=%d actor=%q", n, actor)
	}
	var reason sql.NullString
	if err := st.db.QueryRow(`SELECT fail_reason FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&reason); err != nil {
		t.Fatalf("reason: %v", err)
	}
	if reason.String != "no_cred" {
		t.Fatalf("fail_reason=%q (quiero no_cred)", reason.String)
	}
	// Deferred exige motivo.
	if err := st.Transition(ctx, c.CandidateID, "", 0, StateDeferred, "", "op"); err == nil {
		t.Fatal("deferred sin motivo debe fallar")
	}
}

// TestHealthzMudo: latido viejo o ausente da 503; fresco da 200.
func TestHealthzMudo(t *testing.T) {
	st := testStore(t)
	srv := httptest.NewServer((&API{Store: st, MasscanOK: func() bool { return true }}).Handler())
	defer srv.Close()

	get := func(path string) (int, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		defer resp.Body.Close()
		buf := make([]byte, 512)
		n, _ := resp.Body.Read(buf)
		return resp.StatusCode, string(buf[:n])
	}

	if code, _ := get("/healthz"); code != http.StatusServiceUnavailable {
		t.Fatalf("sin latido healthz=%d (quiero 503)", code)
	}
	ctx := context.Background()
	if err := st.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	code, body := get("/healthz")
	if code != http.StatusOK {
		t.Fatalf("latido fresco healthz=%d (quiero 200)", code)
	}
	for _, k := range []string{"status=", "db=", "masscan=", "tick_age_s=", "rate_class="} {
		if !strings.Contains(body, k) {
			t.Errorf("healthz sin la clave %s: %s", k, body)
		}
	}
	if _, err := st.db.Exec(`UPDATE recruit_control SET tick_at = now() - interval '2 min'`); err != nil {
		t.Fatalf("envejecer: %v", err)
	}
	if code, _ := get("/healthz"); code != http.StatusServiceUnavailable {
		t.Fatalf("latido de 2 min healthz=%d (quiero 503)", code)
	}
}

// TestPausaCortaElReparto: en pausa el claim no reparte (204).
func TestPausaCortaElReparto(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if err := st.Heartbeat(ctx, 1); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	api := &API{Store: st}
	h := api.Handler()

	post := func(path, body string) int {
		resp, err := http.Post("http://x"+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/control/pause", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("pause=%d", rec.Code)
	}
	var paused bool
	st.db.QueryRow(`SELECT paused FROM recruit_control WHERE id=1`).Scan(&paused)
	if !paused {
		t.Fatal("paused no quedo en true")
	}

	seedCandidates(t, st, 3)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/work/claim",
		strings.NewReader(`{"stage":"sweep","worker":"w","limit":3}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("claim en pausa=%d (quiero 204)", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/control/resume", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resume=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/work/claim",
		strings.NewReader(`{"stage":"sweep","worker":"w","limit":3}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("claim tras resume=%d (quiero 200)", rec.Code)
	}
	_ = post
}

// TestRollWindow: la ventana rueda sola una vez y es idempotente.
func TestRollWindow(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	n, err := RollWindow(ctx, st)
	if err != nil || n != 1 {
		t.Fatalf("roll n=%d err=%v (quiero 1)", n, err)
	}
	n, err = RollWindow(ctx, st)
	if err != nil || n != 0 {
		t.Fatalf("roll repetido n=%d err=%v (quiero 0)", n, err)
	}
	var alineadas, abiertas int
	st.db.QueryRow(`SELECT count(*) FROM recruit_windows WHERE date_trunc('hour', window_start) <> window_start`).Scan(&alineadas)
	st.db.QueryRow(`SELECT count(*) FROM recruit_windows WHERE closed_at IS NULL`).Scan(&abiertas)
	if alineadas != 0 || abiertas != 1 {
		t.Fatalf("ventanas desalineadas=%d abiertas=%d", alineadas, abiertas)
	}
}

// TestReintentoNoEsMuerte: un fallo deja `deferred` con respaldo 2^n h.
func TestReintentoNoEsMuerte(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	got, _ := st.Claim(ctx, "enrolling", "w", 1)
	c := got[0]
	if _, err := st.db.Exec(`UPDATE recruit_candidates SET stage_attempts=2 WHERE candidate_id=$1`, c.CandidateID); err != nil {
		t.Fatalf("attempts: %v", err)
	}
	if err := st.TransitionOutcome(ctx, c.CandidateID, c.LeaseOwner, c.LeaseToken, StateDeferred, "no_delivery", "w", "fail"); err != nil {
		t.Fatalf("deferred: %v", err)
	}
	var horas float64
	var attempts int
	if err := st.db.QueryRow(`
		SELECT extract(epoch FROM (next_attempt_at - now()))/3600, stage_attempts
		  FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&horas, &attempts); err != nil {
		t.Fatalf("lectura: %v", err)
	}
	// 2^3 = 8 h: el intento se incremento antes de calcular el respaldo.
	if horas < 7 || horas > 9 {
		t.Fatalf("respaldo=%0.2f h (quiero 7-9) attempts=%d", horas, attempts)
	}
	if attempts != 3 {
		t.Fatalf("stage_attempts=%d (quiero 3)", attempts)
	}
}

// TestRelojDeEdad: `skipped` no mueve la marca, `ok` si.
func TestRelojDeEdad(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	got, _ := st.Claim(ctx, "sweep", "w", 1)
	c := got[0]
	var marca time.Time
	st.db.QueryRow(`SELECT age_clock_at FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&marca)
	for i := 0; i < 10; i++ {
		if _, err := st.db.Exec(`UPDATE recruit_candidates SET state='sweeping', lease_owner='w', lease_token=lease_token+1, lease_expires_at=now()+interval '1 min' WHERE candidate_id=$1`, c.CandidateID); err != nil {
			t.Fatalf("reclaim %d: %v", i, err)
		}
		var tok int64
		st.db.QueryRow(`SELECT lease_token FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&tok)
		if err := st.TransitionOutcome(ctx, c.CandidateID, "w", tok, StateDeferred, "no_answer", "w", "skipped"); err != nil {
			t.Fatalf("skipped %d: %v", i, err)
		}
	}
	var igual time.Time
	st.db.QueryRow(`SELECT age_clock_at FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&igual)
	if !igual.Equal(marca) {
		t.Fatalf("el reloj de edad se movio con skipped: %v -> %v", marca, igual)
	}
	// Un ciclo con trabajo si lo mueve.
	st.db.Exec(`UPDATE recruit_candidates SET state='sweeping', lease_owner='w', lease_token=lease_token+1, lease_expires_at=now()+interval '1 min' WHERE candidate_id=$1`, c.CandidateID)
	var tok int64
	st.db.QueryRow(`SELECT lease_token FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&tok)
	if err := st.TransitionOutcome(ctx, c.CandidateID, "w", tok, StateDeferred, "no_banner", "w", "ok"); err != nil {
		t.Fatalf("ok: %v", err)
	}
	var movido time.Time
	st.db.QueryRow(`SELECT age_clock_at FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&movido)
	if !movido.After(marca) {
		t.Fatal("el reloj de edad no se movio con outcome=ok")
	}
}
