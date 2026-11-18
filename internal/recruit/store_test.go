package recruit

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/migrations"
)

var scratchSeqTest int64

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
	seq := atomic.AddInt64(&scratchSeqTest, 1)
	name := fmt.Sprintf("botnet_recruit_u_%d_%d", os.Getpid(), seq)
	// El motor portable suelta dial-timeout: reintento acotado en DDL
	// administrativo y en la migracion (Apply es idempotente por version).
	if _, err := admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)"); err != nil {
		t.Fatalf("drop previo: %v", err)
	}
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
	seedAttempts(t, st, 2)
	got, _ := st.Claim(ctx, "enrolling", "w", 1)
	c := got[0]
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

// TestClaimLatencyP95: 5.000 en cola, 4 reclamadores, 1.000 claims y cero
// perdidas; p95 por debajo de 250 ms. Se usa la fase `deliver` (lease 900 s)
// porque con el lease corto de `sweep` una suite lenta reclamaria lo ya
// reclamado y el conteo confundiria expiracion con solape.
func TestClaimLatencyP95(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.db.Exec(`
		INSERT INTO recruit_candidates (ip, port, state)
		SELECT ('198.18.' || (g/250) || '.' || (g%250+1))::inet, 10000 + (g%20000), 'eligible'
		  FROM generate_series(1, 5000) g`); err != nil {
		t.Fatalf("siembra: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	vistas := map[int64]time.Time{}
	perdidas := 0
	claims := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 250; n++ {
				items, err := st.Claim(ctx, "deliver", fmt.Sprintf("w-%d", i), 1)
				mu.Lock()
				if err != nil {
					perdidas++
					t.Logf("claim erro: %v", err)
				}
				claims++
				for _, c := range items {
					// Un repetido solo es fallo si su lease anterior seguia
					// vivo; si habia vencido, reclamarlo es lo correcto.
					if prev, ok := vistas[c.CandidateID]; ok {
						if time.Now().Before(prev) {
							perdidas++
							var tok int64
							var estado string
							var seg float64
							st.db.QueryRow(`SELECT lease_token, state, extract(epoch FROM (lease_expires_at-now())) FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&tok, &estado, &seg)
							t.Logf("repetido id=%d token_reclamado=%d token_bd=%d estado=%s restante_s=%.0f", c.CandidateID, c.LeaseToken, tok, estado, seg)
						}
					}
					vistas[c.CandidateID] = c.LeaseUntil
				}
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if claims < 1000 {
		t.Fatalf("claims=%d (quiero 1000)", claims)
	}
	if perdidas != 0 {
		t.Fatalf("reclamos perdidos o repetidos=%d", perdidas)
	}
	if p95 := st.ClaimP95(); p95 > 250 {
		t.Fatalf("p95 de claim=%d ms (tope 250)", p95)
	} else {
		t.Logf("claim p95=%d ms sobre %d muestras", p95, claims)
	}
}

// TestColaSobrevive: cerrar y reabrir el pool (reinicio del proceso) no
// pierde la cola: el estado vive en Postgres.
func TestColaSobrevive(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 12)
	var antes int
	if err := st.db.QueryRow(`SELECT count(*) FROM recruit_candidates WHERE state IN ('eligible','deferred')`).Scan(&antes); err != nil {
		t.Fatalf("antes: %v", err)
	}
	// Reclama 4 para que haya filas en lease y comprueba que tambien siguen.
	if _, err := st.Claim(ctx, "sweep", "w", 4); err != nil {
		t.Fatalf("claim: %v", err)
	}
	dsn := os.Getenv("DATABASE_URL")
	_ = dsn
	st.Close()
	st2, err := Open(st.dsnForTest(), 4)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	var despues int
	if err := st2.db.QueryRow(`SELECT count(*) FROM recruit_candidates`).Scan(&despues); err != nil {
		t.Fatalf("despues: %v", err)
	}
	if despues != antes {
		t.Fatalf("filas tras reabrir=%d (habia %d)", despues, antes)
	}
}

// TestRenewLeaseLog: cada renew deja su linea `lease_renew` y estira la
// fila. (El AC pide 9 en 10 min por journalctl; aqui el mismo camino se
// ejerce a ritmo rapido y se cuentan las lineas.)
func TestRenewLeaseLog(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	got, _ := st.Claim(ctx, "cred", "w", 1)
	c := got[0]

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	for i := 0; i < 10; i++ {
		if err := st.RenewLease(ctx, c.CandidateID, c.LeaseOwner, c.LeaseToken, "cred"); err != nil {
			t.Fatalf("renew %d: %v", i, err)
		}
	}
	if n := strings.Count(buf.String(), "lease_renew"); n < 9 {
		t.Fatalf("lineas lease_renew=%d (quiero >=9)", n)
	}
	var vencido bool
	if err := st.db.QueryRow(`SELECT lease_expires_at > now() + interval '4 min' FROM recruit_candidates WHERE candidate_id=$1`, c.CandidateID).Scan(&vencido); err != nil || !vencido {
		t.Fatalf("el lease no se estiro: %v (vencido=%v)", err, vencido)
	}
}

// TestReclaimTrasMuerte: el dueño muere, el lease vence, otro reclama y el
// muerto ya no escribe.
func TestReclaimTrasMuerte(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	got, _ := st.Claim(ctx, "sweep", "muerto", 1)
	c := got[0]

	// Vencer el lease sin esperar 30 s reales de reloj.
	if _, err := st.db.Exec(`UPDATE recruit_candidates SET lease_expires_at = now() - interval '1 s' WHERE candidate_id=$1`, c.CandidateID); err != nil {
		t.Fatalf("vencer: %v", err)
	}
	var vivos int
	st.db.QueryRow(`SELECT count(*) FROM recruit_candidates WHERE lease_owner IS NOT NULL AND lease_expires_at < now()`).Scan(&vivos)
	if vivos != 1 {
		t.Fatalf("leases vencidos=%d (quiero 1)", vivos)
	}
	got2, err := st.Claim(ctx, "sweep", "nuevo", 1)
	if err != nil || len(got2) != 1 {
		t.Fatalf("reclaim: %v %d", err, len(got2))
	}
	// El dueño difunto ya no puede escribir.
	if err := st.Transition(ctx, c.CandidateID, c.LeaseOwner, c.LeaseToken, StateBannered, "", "muerto"); err != ErrLeaseStale {
		t.Fatalf("escritura del muerto=%v (quiero lease_stale)", err)
	}
	// Y el nuevo si.
	if err := st.Transition(ctx, got2[0].CandidateID, got2[0].LeaseOwner, got2[0].LeaseToken, StateBannered, "", "nuevo"); err != nil {
		t.Fatalf("escritura del nuevo: %v", err)
	}
	var vencidos int
	if err := st.db.QueryRow(`SELECT count(*) FROM recruit_candidates WHERE lease_expires_at < now()`).Scan(&vencidos); err != nil || vencidos != 0 {
		t.Fatalf("quedan %d leases vencidos (err=%v)", vencidos, err)
	}
}

// seedAttempts deja el siguiente candidato con n intentos previos.
func seedAttempts(t *testing.T, st *Store, n int) {
	t.Helper()
	if _, err := st.db.Exec(`UPDATE recruit_candidates SET stage_attempts = $1 WHERE state='eligible'`, n); err != nil {
		t.Fatalf("attempts: %v", err)
	}
}
