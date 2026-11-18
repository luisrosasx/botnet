// EP-15: pruebas de la 0009 (esquema de reclutamiento). Aplica el DDL
// sobre una base scratch y comprueba las restricciones por insercion
// negativa: un CHECK que no rechaza es un CHECK que no existe.
//
// A diferencia de la prueba de EP-08, aqui la falta de DATABASE_URL es
// FALLO y no omision: una migracion que nunca se aplica en el test daba
// verde mudo (gate G4, hallazgo A3-12).
package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/botnet/botnet/migrations"
)

// scratch abre una base nueva migrada y la borra al terminar.
func scratch(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatalf("DATABASE_URL sin definir: la prueba de DDL no se omite (gate G4)")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open admin: %v", err)
	}
	name := fmt.Sprintf("botnet_recruit_ddl_%d", os.Getpid())
	for i := 0; i < 5; i++ {
		_, err = admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		if err == nil {
			break
		}
		time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("drop scratch: %v", err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	t.Cleanup(func() {
		admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		admin.Close()
	})
	db, err := sql.Open("pgx", replaceDB(dsn, name))
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// El motor portable suelta dial-timeout y reset bajo carga: reintento
	// acotado. Apply es idempotente por version, asi que repetir es seguro.
	var applyErr error
	for i := 0; i < 5; i++ {
		if applyErr = migrations.Apply(db); applyErr == nil {
			return db
		}
		time.Sleep(time.Duration(i+1) * 400 * time.Millisecond)
	}
	t.Fatalf("Apply: %v", applyErr)
	return db
}

func mustFail(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err == nil {
		t.Fatalf("se esperaba rechazo y paso: %.70s", q)
	}
}

func mustOK(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("se esperaba aceptacion y fallo: %s: %v", q, err)
	}
}

// Test0009IsIdempotentYNoDuplicaVersion: dos Apply seguidos y dos en
// paralelo dejan una sola version y once tablas.
func Test0009IsIdempotentYNoDuplicaVersion(t *testing.T) {
	db := scratch(t)
	ctx := context.Background()
	if err := migrations.Apply(db); err != nil {
		t.Fatalf("segundo Apply: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version LIKE '0009%'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("versiones 0009=%d err=%v (quiero 1)", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE tablename LIKE 'recruit_%'`).Scan(&n); err != nil || n < 11 {
		t.Fatalf("tablas recruit_=%d err=%v (quiero >=11)", n, err)
	}

	// Dos aplicadores a la vez sobre la misma base: el cerrojo advisory
	// serializa y ninguno ve "already exists".
	dsn := os.Getenv("DATABASE_URL")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := sql.Open("pgx", dsn)
			if err != nil {
				errs[i] = err
				return
			}
			defer c.Close()
			errs[i] = migrations.Apply(c)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil && strings.Contains(err.Error(), "already exists") {
			t.Fatalf("aplicador %d choco: %v", i, err)
		}
	}
}

// Test0009Candidates: idempotencia de alta, dominios cerrados, cola y
// fencing.
func Test0009Candidates(t *testing.T) {
	db := scratch(t)
	ctx := context.Background()

	mustOK(t, db, `INSERT INTO recruit_candidates (ip,port) VALUES ('203.0.113.7',7547) ON CONFLICT (ip,port) DO NOTHING`)
	mustOK(t, db, `INSERT INTO recruit_candidates (ip,port) VALUES ('203.0.113.7',7547) ON CONFLICT (ip,port) DO NOTHING`)
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM recruit_candidates WHERE ip='203.0.113.7' AND port=7547`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alta repetida count=%d err=%v (quiero 1)", n, err)
	}

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname IN ('recruit_q_claim','recruit_q_lease','recruit_q_state','recruit_q_node','recruit_q_verified')`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("indices de cola=%d err=%v (quiero 5)", n, err)
	}
	// Con pocas filas el planificador prefiere seq scan; se le quita. El SET
	// es de sesion, asi que va y se lee en la MISMA conexion.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET enable_seqscan=off`); err != nil {
		t.Fatalf("enable_seqscan: %v", err)
	}
	planRows, err := conn.QueryContext(ctx, `EXPLAIN SELECT candidate_id FROM recruit_candidates WHERE state IN ('new','eligible','deferred') ORDER BY next_attempt_at LIMIT 5`)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plan strings.Builder
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	planRows.Close()
	if !strings.Contains(plan.String(), "recruit_q_claim") {
		t.Errorf("el plan no usa recruit_q_claim: %s", plan.String())
	}

	mustFail(t, db, `INSERT INTO recruit_candidates (ip,port,state) VALUES ('203.0.113.8',22,'leased')`)

	var nodeID string
	mustOK(t, db, `INSERT INTO nodes (node_id,slug,device_token_hash,arch,device_class,consent_at) VALUES (gen_random_uuid(),'recruitddl02',repeat('a',64),'armv7','B',now())`)
	if err := db.QueryRowContext(ctx, `SELECT node_id::text FROM nodes WHERE slug='recruitddl02'`).Scan(&nodeID); err != nil {
		t.Fatalf("node_id: %v", err)
	}

	estados := []string{"new", "excluded", "eligible", "sweeping", "bannered", "assessed",
		"staging", "enrolling", "enrolled", "verified", "deferred", "discarded", "burned", "rolled_back"}
	for i, st := range estados {
		cols, vals := "ip,port,state", fmt.Sprintf("'198.51.100.%d',%d,'%s'", i+10, 1000+i, st)
		if st == "verified" {
			cols += ",node_id,verified_at"
			vals += fmt.Sprintf(",'%s',now()", nodeID)
		}
		mustOK(t, db, fmt.Sprintf(`INSERT INTO recruit_candidates (%s) VALUES (%s)`, cols, vals))
	}
	motivos := []string{"excluded", "no_answer", "no_banner", "no_cred", "no_delivery",
		"flash_cap", "age", "socks_fail", "ip_mismatch", "leave", "burned"}
	for i, m := range motivos {
		mustOK(t, db, fmt.Sprintf(`INSERT INTO recruit_candidates (ip,port,fail_reason) VALUES ('192.0.2.10',%d,$1)`, 3000+i), m)
	}
	mustOK(t, db, `INSERT INTO recruit_candidates (ip,port,fail_reason) VALUES ('192.0.2.11',9998,'age') ON CONFLICT DO NOTHING`)
	mustFail(t, db, `INSERT INTO recruit_candidates (ip,port,fail_reason) VALUES ('192.0.2.12',9998,'inventado')`)

	// verified exige nodo y fecha.
	mustFail(t, db, `INSERT INTO recruit_candidates (ip,port,state) VALUES ('192.0.2.13',9999,'verified')`)
	mustOK(t, db, `INSERT INTO recruit_candidates (ip,port,state) VALUES ('192.0.2.14',9999,'enrolling')`)
	mustFail(t, db, `UPDATE recruit_candidates SET state='verified' WHERE ip='192.0.2.14'`)

	// Fencing: clave de idempotencia y unicidad por (candidate, stage, attempt).
	var cid int64
	if err := db.QueryRowContext(ctx, `SELECT candidate_id FROM recruit_candidates WHERE ip='192.0.2.14'`).Scan(&cid); err != nil {
		t.Fatalf("candidate_id: %v", err)
	}
	mustOK(t, db, `INSERT INTO recruit_jobs (candidate_id,stage,attempt,idempotency_key) VALUES ($1,'sweep',1,repeat('a',64))`, cid)
	mustFail(t, db, `INSERT INTO recruit_jobs (candidate_id,stage,attempt,idempotency_key) VALUES ($1,'sweep',1,repeat('b',64))`, cid)
	mustFail(t, db, `INSERT INTO recruit_jobs (candidate_id,stage,attempt,idempotency_key) VALUES ($1,'sweep',2,repeat('a',64))`, cid)
}

// Test0009Ritmo: unidad tipada, deuda con techo, singleton y decision.
func Test0009Ritmo(t *testing.T) {
	db := scratch(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `INSERT INTO recruit_windows (window_start,unit,pps_budget,conn_budget,attempt_budget) VALUES (date_trunc('hour',now()),'gbit',1500,30,60)`); err == nil {
		t.Error("unit='gbit' debe violar CHECK")
	}
	mustFail(t, db, `INSERT INTO recruit_windows (window_start,pps_budget,pps_used,pps_granted,conn_budget,attempt_budget) VALUES (now(),1000,6001,1000,10,10)`)

	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM recruit_control`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("singleton de control=%d err=%v (quiero 1)", n, err)
	}
	mustFail(t, db, `INSERT INTO recruit_control (id) VALUES (2)`)
	var nullable string
	if err := db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_name='recruit_control' AND column_name='tick_at'`).Scan(&nullable); err != nil || nullable != "YES" {
		t.Fatalf("tick_at nullable=%q err=%v (quiero YES)", nullable, err)
	}

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM recruit_window_policy`).Scan(&n); err != nil || n != 24 {
		t.Fatalf("politica horaria=%d err=%v (quiero 24)", n, err)
	}
	rows, err := db.QueryContext(ctx, `SELECT hour_utc, rate_class FROM recruit_window_policy WHERE hour_utc IN (0,3,12) ORDER BY hour_utc`)
	if err != nil {
		t.Fatalf("politica: %v", err)
	}
	want := map[int]int{0: 0, 3: 3, 12: 1}
	for rows.Next() {
		var h, rc int
		if err := rows.Scan(&h, &rc); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if want[h] != rc {
			t.Errorf("hora %d clase=%d (quiero %d)", h, rc, want[h])
		}
	}
	rows.Close()

	mustFail(t, db, `INSERT INTO recruit_planner_ticks (tick_at,rate_class,action,reason) VALUES (now(),1,'wobble','x')`)
}

// Test0009EvidenciaSocks5: un solo ganador y cuatro condiciones duras.
func Test0009EvidenciaSocks5(t *testing.T) {
	db := scratch(t)
	ctx := context.Background()

	var nodeID string
	mustOK(t, db, `INSERT INTO nodes (node_id,slug,device_token_hash,arch,device_class,consent_at) VALUES (gen_random_uuid(),'recruitddl01',repeat('a',64),'armv7','B',now())`)
	if err := db.QueryRowContext(ctx, `SELECT node_id::text FROM nodes WHERE slug='recruitddl01'`).Scan(&nodeID); err != nil {
		t.Fatalf("node_id: %v", err)
	}
	mustOK(t, db, `INSERT INTO recruit_candidates (ip,port) VALUES ('203.0.113.20',8080)`)
	var cid int64
	if err := db.QueryRowContext(ctx, `SELECT candidate_id FROM recruit_candidates WHERE ip='203.0.113.20'`).Scan(&cid); err != nil {
		t.Fatalf("candidate_id: %v", err)
	}

	// Cada condicion por separado rompe el CHECK.
	mustFail(t, db, `INSERT INTO recruit_socks5_checks (candidate_id,node_id,target,http_status,bytes_in,latency_ms,ip_match,ok) VALUES ($1,$2,'http://x',200,0,100,true,true)`, cid, nodeID)
	mustFail(t, db, `INSERT INTO recruit_socks5_checks (candidate_id,node_id,target,http_status,bytes_in,latency_ms,ip_match,ok) VALUES ($1,$2,'http://x',200,10,1501,true,true)`, cid, nodeID)
	mustFail(t, db, `INSERT INTO recruit_socks5_checks (candidate_id,node_id,target,http_status,bytes_in,latency_ms,ip_match,ok) VALUES ($1,$2,'http://x',200,10,100,false,true)`, cid, nodeID)
	mustOK(t, db, `INSERT INTO recruit_socks5_checks (candidate_id,node_id,target,http_status,bytes_in,latency_ms,ip_match,ok) VALUES ($1,$2,'http://x',200,10,100,true,true)`, cid, nodeID)

	// Segundo ganador del mismo candidato: UNIQUE parcial.
	mustFail(t, db, `INSERT INTO recruit_socks5_checks (candidate_id,node_id,target,attempt,http_status,bytes_in,latency_ms,ip_match,ok) VALUES ($1,$2,'http://x',2,200,10,100,true,true)`, cid, nodeID)
	// Mismo (candidate, attempt) sin ok.
	mustFail(t, db, `INSERT INTO recruit_socks5_checks (candidate_id,node_id,target,attempt) VALUES ($1,$2,'http://x',1)`, cid, nodeID)

	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conname LIKE 'recruit_%'`).Scan(&n); err != nil || n < 12 {
		t.Fatalf("restricciones recruit_=%d err=%v (quiero >=12)", n, err)
	}
	var nullable string
	if err := db.QueryRowContext(ctx, `SELECT is_nullable FROM information_schema.columns WHERE table_name='recruit_socks5_checks' AND column_name='node_id'`).Scan(&nullable); err != nil || nullable != "NO" {
		t.Fatalf("node_id nullable=%q err=%v (quiero NO)", nullable, err)
	}

	// Gobierno: dominios cerrados de exclusion y estado de oleada.
	mustFail(t, db, `INSERT INTO recruit_exclusions (kind,pattern,category) VALUES ('asn','1234','inventada')`)
	mustOK(t, db, `INSERT INTO recruit_exclusions (kind,pattern,category) VALUES ('asn','1234','banco')`)
	mustFail(t, db, `INSERT INTO recruit_waves (wave_no,state) VALUES (1,'volando')`)
	mustOK(t, db, `INSERT INTO recruit_waves (wave_no) VALUES (1) ON CONFLICT (wave_no) DO NOTHING`)
}
