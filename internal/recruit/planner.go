package recruit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Lazo de histeresis del ritmo (BLUEPRINT §7).
//
// Sube de una clase a la vez y solo con tres ventanas propias buenas; baja
// de una clase de golpe ante la primera metrica mala y se queda tres
// ventanas sin subir. Cada decision se escribe con su motivo: el ritmo no
// lo decide nadie a ojo.

// Clases de ritmo y su pps objetivo.
var ppsDeClase = map[int]int{1: 1500, 2: 2500, 3: 4000, 4: 5000}

// VentanasCooldown son las ventanas sin subir tras una reversion.
const VentanasCooldown = 3

// VentanasProbes es el minimo de sondas en 15 min para que la subida tenga
// denominador.
const VentanasProbes = 200

// EmbudoMaximo es el numero de filas en staging|enrolling que frena la
// promocion: mas que esto y el cuello esta mas adelante.
const EmbudoMaximo = 500

// Metricas es la foto de una ventana que alimenta la decision.
type Metricas struct {
	SuccessRatio   float64 // socks5 con exito / intentos
	EffectiveRatio float64 // pps_efectivo / pps_objetivo
	LeaveRatio24h  float64 // bajas del dueño en 24 h
	Probes15m      int
	NodeLoad1      float64
	HostCPUKnown   bool
	Embudo         int // filas en staging|enrolling
}

// Decision es el tick escrito.
type Decision struct {
	Action       string
	RateClass    int
	Reason       string
	PPSObjetivo  int
	HostFallback bool
}

// Umbrales de subida.
func metricasBuenas(m Metricas) bool {
	return m.SuccessRatio >= 0.95 && m.EffectiveRatio >= 0.90 && m.LeaveRatio24h <= 0.05
}

// Decide calcula el tick. `actual` es la clase vigente y `upsRecientes` las
// subidas dentro del cooldown.
func Decide(actual int, m Metricas, upsRecientes int, fixed int) Decision {
	d := Decision{RateClass: actual, Action: "hold"}
	if fixed > 0 {
		// El pps fijo no es una clase: la clase se queda y el objetivo manda.
		d.PPSObjetivo = fixed
		d.Reason = "pps_fijo"
		return d
	}
	// Host sin CPU fiable: se acepta load1 como sustituto.
	if !m.HostCPUKnown {
		d.HostFallback = true
	}
	hostMal := m.NodeLoad1 > 6
	if !m.HostCPUKnown {
		hostMal = m.NodeLoad1 > 6
	}
	// Freno de embudo: mas de 500 atascadas y no se promueve nada nuevo.
	if m.Embudo > EmbudoMaximo {
		d.Action = "hold"
		d.Reason = "enroll_backlog"
		d.PPSObjetivo = 0
		return d
	}
	// Reversion: la primera metrica mala baja una clase.
	malas := !metricasBuenas(m) || hostMal
	if malas {
		if actual > 1 {
			d.RateClass = actual - 1
		}
		d.Action = "down"
		d.Reason = motivoMalo(m, hostMal)
		d.PPSObjetivo = ppsDeClase[d.RateClass]
		return d
	}
	// Subida: una clase, con denominador y sin cooldown activo.
	if upsRecientes > 0 {
		d.Reason = "cooldown"
		d.PPSObjetivo = ppsDeClase[actual]
		return d
	}
	if m.Probes15m < VentanasProbes {
		d.Reason = "denominador_corto"
		d.PPSObjetivo = ppsDeClase[actual]
		return d
	}
	if actual >= 4 {
		d.Reason = "techo"
		d.PPSObjetivo = ppsDeClase[actual]
		return d
	}
	d.Action = "up"
	d.RateClass = actual + 1
	d.Reason = "tres_ventanas_buenas"
	d.PPSObjetivo = ppsDeClase[d.RateClass]
	return d
}

func motivoMalo(m Metricas, hostMal bool) string {
	switch {
	case hostMal:
		return "host_cargado"
	case m.SuccessRatio < 0.95:
		return "exito_bajo"
	case m.EffectiveRatio < 0.90:
		return "pps_efectivo_bajo"
	default:
		return "bajas_altas"
	}
}

// Tick lee el estado, decide y escribe la decision. Es el unico punto que
// cambia la clase.
func (s *Store) Tick(ctx context.Context, m Metricas) (Decision, error) {
	var actual int
	var manual sql.NullInt64
	var paused bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT rate_class, manual_rate_class, paused FROM recruit_control WHERE id=1`).
		Scan(&actual, &manual, &paused); err != nil {
		return Decision{}, err
	}
	if paused {
		d := Decision{Action: "paused", RateClass: 0, Reason: "pausa_manual", PPSObjetivo: 0}
		return d, s.escribirTick(ctx, d, m)
	}
	if manual.Valid {
		clase := int(manual.Int64)
		if clase == 0 {
			d := Decision{Action: "paused", RateClass: 0, Reason: "clase_manual_0", PPSObjetivo: 0}
			return d, s.escribirTick(ctx, d, m)
		}
		actual = clase
	}
	ups, err := s.subidasEnCooldown(ctx)
	if err != nil {
		return Decision{}, err
	}
	d := Decide(actual, m, ups, ppsFijo())
	if d.Action == "up" || d.Action == "down" {
		if _, err := s.db.ExecContext(ctx, `
			UPDATE recruit_control SET rate_class=$1, updated_at=now() WHERE id=1`, d.RateClass); err != nil {
			return d, err
		}
	}
	return d, s.escribirTick(ctx, d, m)
}

func (s *Store) escribirTick(ctx context.Context, d Decision, m Metricas) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO recruit_planner_ticks
		  (tick_at, rate_class, action, reason, pps_target, pps_effective, probes_15m, host_fallback)
		VALUES (now(), $1, $2, $3, $4, $5, $6, $7)`,
		d.RateClass, d.Action, d.Reason, d.PPSObjetivo,
		int(m.EffectiveRatio*float64(d.PPSObjetivo)), m.Probes15m, d.HostFallback)
	return err
}

// subidasEnCooldown cuenta las subidas dentro de las ultimas N ventanas.
func (s *Store) subidasEnCooldown(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_planner_ticks
		 WHERE action='up' AND tick_at > now() - make_interval(hours => $1)`,
		VentanasCooldown).Scan(&n)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return n, nil
}

// ppsFijo lee RECRUIT_PPS_FIXED (0 si no esta).
func ppsFijo() int {
	v := getenv("RECRUIT_PPS_FIXED")
	if v == "" {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return 0
	}
	return n
}

var _ = time.Now

func getenv(k string) string { return os.Getenv(k) }

// MetricasDesdeBD arma la foto de la ventana con lo que hay en la base. Lo
// que no se puede medir queda en su valor neutro y el host se marca sin CPU
// fiable: el lazo decide igual, con load1 ausente.
func (s *Store) MetricasDesdeBD(ctx context.Context) (Metricas, error) {
	m := Metricas{HostCPUKnown: false, NodeLoad1: 0}
	var ok, total, probes, embudo int
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE outcome='ok'), count(*)
		  FROM recruit_jobs WHERE finished_at > now() - interval '15 min'`).Scan(&ok, &total); err != nil {
		return m, err
	}
	if total > 0 {
		m.SuccessRatio = float64(ok) / float64(total)
	} else {
		m.SuccessRatio = 1
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_jobs WHERE claimed_at > now() - interval '15 min'`).Scan(&probes); err != nil {
		return m, err
	}
	m.Probes15m = probes
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM recruit_candidates WHERE state IN ('staging','enrolling')`).Scan(&embudo); err != nil {
		return m, err
	}
	m.Embudo = embudo
	r, err := s.EffectiveRatio(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return m, err
	}
	m.EffectiveRatio = r
	return m, nil
}
