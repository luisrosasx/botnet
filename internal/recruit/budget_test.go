package recruit

import (
	"context"
	"testing"
	"time"
)

// TestVentanasAlineadas: toda ventana nace en la hora exacta y se cierra
// despues de empezar.
func TestVentanasAlineadas(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, _, err := st.OpenWindow(ctx); err != nil {
		t.Fatalf("abrir: %v", err)
	}
	if _, err := st.db.Exec(`UPDATE recruit_windows SET window_start = window_start - interval '60 min'`); err != nil {
		t.Fatalf("envejecer: %v", err)
	}
	if err := st.CerrarVentanas(ctx); err != nil {
		t.Fatalf("cerrar: %v", err)
	}
	if _, _, err := st.OpenWindow(ctx); err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	desalineadas, malCerradas, err := st.CheckVentanas(ctx)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if desalineadas != 0 {
		t.Fatalf("ventanas desalineadas=%d", desalineadas)
	}
	if malCerradas != 0 {
		t.Fatalf("ventanas cerradas antes de empezar=%d", malCerradas)
	}
}

// TestVentanaRuedaSola: sin orquestador, el propio claim rueda la ventana.
func TestVentanaRuedaSola(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	// La clase 0 (22-02 h UTC) deja presupuesto 0: se fija la hora a clase 1.
	if _, err := st.db.Exec(`UPDATE recruit_window_policy SET rate_class = 1`); err != nil {
		t.Fatalf("politica: %v", err)
	}
	if _, _, err := st.OpenWindow(ctx); err != nil {
		t.Fatalf("abrir: %v", err)
	}
	if _, err := st.db.Exec(`
		UPDATE recruit_windows SET pps_used = pps_budget,
		       window_start = window_start - interval '60 min'`); err != nil {
		t.Fatalf("gastar: %v", err)
	}
	seedCandidates(t, st, 1)
	if _, err := st.ClaimJobs(ctx, "sweep", "w", 1); err != nil {
		t.Fatalf("claim: %v", err)
	}
	var abiertas, gastadas int
	st.db.QueryRow(`SELECT count(*) FROM recruit_windows WHERE closed_at IS NULL`).Scan(&abiertas)
	st.db.QueryRow(`SELECT count(*) FROM recruit_windows WHERE closed_at IS NULL AND pps_used < pps_budget`).Scan(&gastadas)
	if abiertas != 1 {
		t.Fatalf("ventanas abiertas=%d (quiero 1)", abiertas)
	}
	if gastadas != 1 {
		t.Fatalf("la ventana nueva nace gastada=%d", gastadas)
	}
	// El comando de rueda es idempotente.
	if n, err := RollWindow(ctx, st); err != nil || n != 0 {
		t.Fatalf("roll repetido n=%d err=%v (quiero 0)", n, err)
	}
}

// TestGranelConDeuda: pide 3000 sobre presupuesto 1500 y comprueba que se
// concede con deuda acotada y que la ventana siguiente rinde >= 0,90.
func TestGranelConDeuda(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.db.Exec(`UPDATE recruit_window_policy SET rate_class = 1`); err != nil {
		t.Fatalf("politica: %v", err)
	}
	if _, err := st.db.Exec(`
		INSERT INTO recruit_windows (window_start, pps_budget, conn_budget, attempt_budget)
		VALUES (date_trunc('hour', now()), 1500, 30, 60)`); err != nil {
		t.Fatalf("ventana: %v", err)
	}
	concedido, err := st.Grant(ctx, 3000)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if concedido != 3000 {
		t.Fatalf("concedido=%d (quiero 3000, cabe en deuda)", concedido)
	}
	var deuda int
	st.db.QueryRow(`SELECT pps_debt FROM recruit_windows WHERE closed_at IS NULL`).Scan(&deuda)
	if deuda > DeudaMaxima {
		t.Fatalf("deuda=%d (tope %d)", deuda, DeudaMaxima)
	}
	if deuda != 1500 {
		t.Fatalf("deuda=%d (quiero 1500)", deuda)
	}
	// El techo corta: pedir 10.000 mas no pasa de 5000 de deuda.
	if _, err := st.Grant(ctx, 10000); err != nil {
		t.Fatalf("grant 2: %v", err)
	}
	st.db.QueryRow(`SELECT pps_debt FROM recruit_windows WHERE closed_at IS NULL`).Scan(&deuda)
	if deuda > DeudaMaxima {
		t.Fatalf("deuda tras pedir 10k=%d (tope %d)", deuda, DeudaMaxima)
	}
	ratio, err := st.EffectiveRatio(ctx)
	if err != nil || ratio < 0.90 {
		t.Fatalf("ratio efectivo=%.3f err=%v (quiero >=0,90)", ratio, err)
	}
}

// TestTopePorDestino: la cuarta tentativa de cred o deliver en la hora se
// rechaza, y el tope es compartido entre las dos etapas.
func TestTopePorDestino(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	if _, err := st.db.Exec(`
		INSERT INTO recruit_candidates (ip, port, state)
		VALUES ('203.0.113.40', 8100, 'eligible')`); err != nil {
		t.Fatalf("candidato: %v", err)
	}
	var id int64
	st.db.QueryRow(`SELECT candidate_id FROM recruit_candidates WHERE ip='203.0.113.40'`).Scan(&id)
	// El candidato ya lleva tres intentos: el claim debe dar el cuarto.
	if _, err := st.db.Exec(`UPDATE recruit_candidates SET stage_attempts=3 WHERE candidate_id=$1`, id); err != nil {
		t.Fatalf("attempts: %v", err)
	}
	for i := 1; i <= 3; i++ {
		stage := "cred"
		if i == 3 {
			stage = "deliver"
		}
		if _, err := st.db.Exec(`
			INSERT INTO recruit_jobs (candidate_id, stage, attempt, idempotency_key, claimed_at)
			VALUES ($1,$2,$3,$4,now())`, id, stage, i, jobKey("203.0.113.40", 8100, stage, i)); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	cabe, err := st.PuedeIntentar(ctx, "203.0.113.40")
	if err != nil {
		t.Fatalf("puede: %v", err)
	}
	if cabe {
		t.Fatal("la cuarta tentativa no deberia caber")
	}
	// Y el claim la deja fuera.
	if _, err := st.db.Exec(`UPDATE recruit_candidates SET state='eligible', lease_owner=NULL, lease_expires_at=NULL`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, err := st.ClaimJobs(ctx, "cred", "w", 5)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("claim devolvio %d con el tope agotado", len(got))
	}
	// Con dos intentos si cabe.
	if _, err := st.db.Exec(`DELETE FROM recruit_jobs WHERE attempt=3`); err != nil {
		t.Fatalf("borrar: %v", err)
	}
	got, err = st.ClaimJobs(ctx, "cred", "w", 5)
	if err != nil || len(got) != 1 {
		t.Fatalf("claim con dos intentos: %v %d", err, len(got))
	}
	if got[0].Attempt != 4 {
		t.Fatalf("intento reclamado=%d (quiero 4)", got[0].Attempt)
	}
	_ = time.Now
}
