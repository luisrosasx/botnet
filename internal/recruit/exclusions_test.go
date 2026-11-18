package recruit

import (
	"context"
	"testing"
)

// TestCargaYCoincide: la lista se carga con sus categorias y el prefijo
// propio y el honeypot casan con su motivo escrito.
func TestCargaYCoincide(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	n, err := st.CargarExclusions(ctx, "../../testdata/recruit/exclusions.csv")
	if err != nil {
		t.Fatalf("carga: %v", err)
	}
	if n < 10 {
		t.Fatalf("exclusiones cargadas=%d (quiero >=10)", n)
	}
	var cats int
	st.db.QueryRow(`SELECT count(DISTINCT category) FROM recruit_exclusions`).Scan(&cats)
	if cats < 5 {
		t.Fatalf("categorias=%d (quiero >=5)", cats)
	}
	var ptr int
	st.db.QueryRow(`SELECT count(*) FROM recruit_exclusions WHERE kind='ptr_regex'`).Scan(&ptr)
	if ptr < 1 {
		t.Fatal("falta al menos una exclusion por ptr_regex")
	}

	// Prefijo propio y honeypot.
	if m, err := st.Coincide(ctx, "74.208.94.91", ""); err != nil || m == "" {
		t.Fatalf("el prefijo propio no casa: %q %v", m, err)
	}
	if m, err := st.Coincide(ctx, "198.18.5.5", "cowrie.honeypot.net"); err != nil || m == "" {
		t.Fatalf("el honeypot no casa: %q %v", m, err)
	}
	if m, _ := st.Coincide(ctx, "198.18.9.9", "sin.nada"); m != "" {
		t.Fatalf("una IP limpia no debe casar: %q", m)
	}
}

// TestExcluirEsTipadoEIdempotente: el evento lleva el motivo y repetir no
// duplica.
func TestExcluirEsTipadoEIdempotente(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	seedCandidates(t, st, 1)
	var id int64
	st.db.QueryRow(`SELECT candidate_id FROM recruit_candidates LIMIT 1`).Scan(&id)
	if _, err := st.db.Exec(`UPDATE recruit_candidates SET state='assessed' WHERE candidate_id=$1`, id); err != nil {
		t.Fatalf("estado: %v", err)
	}
	if err := st.Excluir(ctx, id, "honeypot:cowrie", "probe"); err != nil {
		t.Fatalf("excluir: %v", err)
	}
	if err := st.Excluir(ctx, id, "honeypot:cowrie", "probe"); err != nil {
		t.Fatalf("excluir repetido: %v", err)
	}
	var n int
	var razon string
	st.db.QueryRow(`SELECT count(*), max(reason) FROM recruit_candidate_events WHERE candidate_id=$1`, id).Scan(&n, &razon)
	if n != 1 || razon != "honeypot:cowrie" {
		t.Fatalf("eventos=%d razon=%q (quiero 1 y honeypot:cowrie)", n, razon)
	}
	var estado, motivo string
	st.db.QueryRow(`SELECT state, fail_reason FROM recruit_candidates WHERE candidate_id=$1`, id).Scan(&estado, &motivo)
	if estado != "excluded" || motivo != "excluded" {
		t.Fatalf("estado=%s motivo=%s", estado, motivo)
	}
}
