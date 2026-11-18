package recruit

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// Snapshot es el catalogo botnet_recruit_* del BLUEPRINT §10. Todo lo que
// el gate, el smoke y el runbook leen sale de aqui: del servicio vivo, no de
// un numero escrito a mano (A3-15, A3-18).
type Snapshot struct {
	PPSTarget         int
	PPSEffective      int
	TickAgeSeconds    int
	RateClass         int
	Paused            bool
	HostLoad1         float64
	RBLListed         int
	StateRows         int
	QueueDepth        int
	EnrollBacklog     int
	Socks5SuccessRate float64
	NodeLeave24h      float64
	ClaimLatencyMS    int64
	ThrottleSkips     int64
	LeaseExpired      int64
	BudgetUnavailable int64
	PGFatal53300      int64
}

// pgFatal cuenta los errores fatales del motor (53300 = quedarse sin
// conexiones) que este proceso ha visto. Mirar solo el journal deja fuera
// las rafagas ya rotadas, y el SLO pide cero.
var pgFatal atomic.Int64

// NotePGError suma un error del motor si es el 53300. Lo llaman las rutas
// que ya tienen el error en la mano y cierran el SLO de §10.
func NotePGError(err error) {
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "53300") {
		pgFatal.Add(1)
	}
}

// Snapshot lee el catalogo. Cada consulta que falla vale 0 y no tumba la
// publicacion: con el motor caido el gate tiene que ver ceros, no un 500.
func (s *Store) Snapshot(ctx context.Context, h Health) Snapshot {
	sn := Snapshot{
		TickAgeSeconds: int(h.TickAge.Seconds()),
		RateClass:      h.RateClass,
		Paused:         h.Paused,
		HostLoad1:      hostLoad1(),
		ClaimLatencyMS: s.ClaimP95(),
		PGFatal53300:   pgFatal.Load(),
	}
	sn.PPSTarget, sn.PPSEffective = s.lastPPS(ctx)
	sn.RBLListed = int(s.scalar(ctx, `SELECT count(*) FROM recruit_source_ip WHERE rbl_listed`))
	sn.StateRows = int(s.scalar(ctx, `SELECT count(*) FROM recruit_candidates`))
	sn.QueueDepth = int(s.scalar(ctx, `SELECT count(*) FROM recruit_jobs WHERE finished_at IS NULL`))
	sn.EnrollBacklog = int(s.scalar(ctx, `
		SELECT count(*) FROM recruit_candidates
		 WHERE state IN ('staging','enrolling')
		    OR (state = 'enrolled' AND node_id IS NULL)`))
	sn.Socks5SuccessRate = s.ratio(ctx, `
		SELECT count(*) FILTER (WHERE ok)::numeric, count(*)::numeric FROM recruit_socks5_checks`, true)
	sn.NodeLeave24h = s.ratio(ctx, `
		SELECT count(*) FILTER (WHERE left_at > now() - interval '24 hours')::numeric,
		       GREATEST(count(*), 1)::numeric FROM leaves`, false)
	sn.ThrottleSkips = int64(s.scalar(ctx, `SELECT count(*) FROM recruit_jobs WHERE outcome = 'skipped'`))
	sn.LeaseExpired = int64(s.scalar(ctx, `SELECT count(*) FROM recruit_jobs WHERE attempt > 1`))
	sn.BudgetUnavailable = int64(s.scalar(ctx, `
		SELECT count(*) FROM recruit_planner_ticks WHERE action = 'blocked'`))
	return sn
}

// lastPPS devuelve el objetivo y el efectivo del ultimo tick. Son el par que
// el gate divide sin PromQL (A3-15).
func (s *Store) lastPPS(ctx context.Context) (int, int) {
	var target, eff *int
	err := s.db.QueryRowContext(ctx, `
		SELECT pps_target, pps_effective FROM recruit_planner_ticks
		 ORDER BY tick_at DESC LIMIT 1`).Scan(&target, &eff)
	if err != nil {
		return 0, 0
	}
	return derefInt(target), derefInt(eff)
}

// ratio divide dos columnas de la misma fila. `uno` devuelve 1 cuando no hay
// muestras: un servicio sin intentos no esta fallando.
func (s *Store) ratio(ctx context.Context, query string, uno bool) float64 {
	var num, den *float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&num, &den); err != nil {
		return 0
	}
	d := derefFloat(den)
	if d == 0 {
		if uno {
			return 1
		}
		return 0
	}
	return derefFloat(num) / d
}

// scalar ejecuta un count y devuelve 0 si la consulta falla.
func (s *Store) scalar(ctx context.Context, query string) float64 {
	var v *float64
	if err := s.db.QueryRowContext(ctx, query).Scan(&v); err != nil {
		return 0
	}
	return derefFloat(v)
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// hostLoad1 lee la carga del host. Sin /proc (Windows, contenedor raro) vale
// 0: el planificador decide por el mismo dato (T-1604), no por este.
func hostLoad1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return v
}

// Write expone el catalogo en formato Prometheus. Una familia por linea con
// su # TYPE, para que `grep -c '^botnet_recruit_'` cuente las familias y el
// valor se lea con awk.
func (sn Snapshot) Write(w interface{ Write([]byte) (int, error) }) {
	lines := []struct {
		name, typ, value string
	}{
		{"pps_target", "gauge", strconv.Itoa(sn.PPSTarget)},
		{"pps_effective", "gauge", strconv.Itoa(sn.PPSEffective)},
		{"orchestrator_tick_age_seconds", "gauge", strconv.Itoa(sn.TickAgeSeconds)},
		{"rate_class", "gauge", strconv.Itoa(sn.RateClass)},
		{"paused", "gauge", strconv.Itoa(boolToInt(sn.Paused))},
		{"host_load1", "gauge", strconv.FormatFloat(sn.HostLoad1, 'f', 2, 64)},
		{"rbl_listed", "gauge", strconv.Itoa(sn.RBLListed)},
		{"state_rows", "gauge", strconv.Itoa(sn.StateRows)},
		{"queue_depth", "gauge", strconv.Itoa(sn.QueueDepth)},
		{"enroll_backlog", "gauge", strconv.Itoa(sn.EnrollBacklog)},
		{"socks5_success_ratio", "gauge", strconv.FormatFloat(sn.Socks5SuccessRate, 'f', 3, 64)},
		{"node_leave_ratio_24h", "gauge", strconv.FormatFloat(sn.NodeLeave24h, 'f', 4, 64)},
		{"claim_latency_ms", "gauge", strconv.FormatInt(sn.ClaimLatencyMS, 10)},
		{"throttle_skips_total", "counter", strconv.FormatInt(sn.ThrottleSkips, 10)},
		{"lease_expired_total", "counter", strconv.FormatInt(sn.LeaseExpired, 10)},
		{"budget_unavailable_total", "counter", strconv.FormatInt(sn.BudgetUnavailable, 10)},
		{"pg_fatal_53300_total", "counter", strconv.FormatInt(sn.PGFatal53300, 10)},
	}
	for _, l := range lines {
		fmt.Fprintf(w, "# TYPE botnet_recruit_%s %s\n", l.name, l.typ)
		fmt.Fprintf(w, "botnet_recruit_%s %s\n", l.name, l.value)
	}
}
