// Package jobs agrupa los trabajos horarios del backend (BLUEPRINT §9, §11).
// EP-14 T-1407: conciliación horaria de `bytes_delta` (que viaja en el
// heartbeat, sin requests extra) contra la tabla `usage` autoritativa. La
// tolerancia contable única es 0,5 % (A2-15): el desvío se audita publicando
// `usage.audit` por NATS y el cobro nunca se bloquea.
package jobs

import (
	"fmt"
	"io"
	"math/rand"
	"time"
)

// TolerancePct es la única tolerancia contable del sistema (BLUEPRINT §11),
// en porcentaje: 0,5 %.
const TolerancePct = 0.5

// HourSample es el agregado por nodo de los bytes_delta llegados en el
// heartbeat dentro de la hora (columna bytes_delta del hbcodec, EP-04/EP-12).
type HourSample struct {
	NodeID     string
	BytesDelta int64
}

// UsageRow es la fila de `usage` a conciliar (medidor autoritativo del nodo).
type UsageRow struct {
	NodeID string
	Bytes  int64
}

// DriftReport es el resultado de una hora: sumas y desvío porcentual.
type DriftReport struct {
	Hour       time.Time
	DeltaTotal int64
	UsageTotal int64
	DriftPct   float64
	Red        bool // true si DriftPct > tolerancia
}

// Reconciler lee ambas fuentes (solo servidor: cero requests al nodo,
// criterio 2) y publica alarmas usage.audit. Las funciones son inyectables
// para tests con fixture; en producción cierran sobre pgx y NATS.
type Reconciler struct {
	TolerancePct float64
	Deltas       func(hour time.Time) ([]HourSample, error)
	Usage        func(hour time.Time) ([]UsageRow, error)
	Publish      func(subject string, payload []byte) error
}

// RunHour concilia una hora. Nunca bloquea el cobro: una alarma publicada
// es éxito contable, no error.
func (r *Reconciler) RunHour(h time.Time) (DriftReport, error) {
	tol := r.TolerancePct
	if tol <= 0 {
		tol = TolerancePct
	}
	rep := DriftReport{Hour: h.UTC().Truncate(time.Hour)}
	deltas, err := r.Deltas(rep.Hour)
	if err != nil {
		return rep, err
	}
	rows, err := r.Usage(rep.Hour)
	if err != nil {
		return rep, err
	}
	for _, s := range deltas {
		rep.DeltaTotal += s.BytesDelta
	}
	for _, u := range rows {
		rep.UsageTotal += u.Bytes
	}
	if rep.UsageTotal > 0 {
		d := rep.DeltaTotal - rep.UsageTotal
		if d < 0 {
			d = -d
		}
		rep.DriftPct = 100 * float64(d) / float64(rep.UsageTotal)
	}
	rep.Red = rep.DriftPct > tol
	if rep.Red && r.Publish != nil {
		payload := []byte(fmt.Sprintf(
			"hour=%s delta_total=%d usage_total=%d drift_pct=%.3f tol_pct=%.2f",
			rep.Hour.Format(time.RFC3339), rep.DeltaTotal, rep.UsageTotal,
			rep.DriftPct, tol))
		if err := r.Publish("usage.audit", payload); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// RunHours recorre horas consecutivas y devuelve el número de horas en rojo.
// Sigue adelante tras una hora roja: la auditoría no detiene la facturación.
func (r *Reconciler) RunHours(start time.Time, n int) (reps []DriftReport, red int, err error) {
	for i := 0; i < n; i++ {
		rep, herr := r.RunHour(start.Add(time.Duration(i) * time.Hour))
		if herr != nil {
			return reps, red, herr
		}
		if rep.Red {
			red++
		}
		reps = append(reps, rep)
	}
	return reps, red, nil
}

// UsageAuditSubject es el tema NATS declarado en BLUEPRINT §9: auditoría,
// nunca facturable.
const UsageAuditSubject = "usage.audit"

// Fixture24h construye un fixture determinista de 24 horas con `nodes` nodos
// y un desvío objetivo driftPct por hora, perturbado ±tol/4 con semilla fija.
// Devuelve las dos fuentes y contadores de lecturas para asertar que el job
// no añade ni un request por nodo (criterio 2: lecturas locales, cero red).
func Fixture24h(driftPct float64, nodes int) (
	deltas func(time.Time) ([]HourSample, error),
	usage func(time.Time) ([]UsageRow, error),
	reads func() int64,
) {
	var readsN int64
	rng := rand.New(rand.NewSource(7))
	type hour struct {
		d, u int64
	}
	table := map[time.Time]hour{}
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		h := base.Add(time.Duration(i) * time.Hour)
		perNode := int64(200_000 + rng.Intn(50_000))
		total := perNode * int64(nodes)
		perturb := driftPct * (0.75 + rng.Float64()*0.5) // jitter multiplicative
		dtotal := int64(float64(total) * (1 + perturb/100))
		table[h] = hour{d: dtotal, u: total}
	}
	deltas = func(h time.Time) ([]HourSample, error) {
		readsN++
		row := table[h.UTC().Truncate(time.Hour)]
		out := make([]HourSample, 0, nodes)
		share := row.d / int64(nodes)
		for i := 0; i < nodes; i++ {
			out = append(out, HourSample{NodeID: fmt.Sprintf("node-%03d", i), BytesDelta: share})
		}
		return out, nil
	}
	usage = func(h time.Time) ([]UsageRow, error) {
		readsN++
		row := table[h.UTC().Truncate(time.Hour)]
		out := make([]UsageRow, 0, nodes)
		share := row.u / int64(nodes)
		for i := 0; i < nodes; i++ {
			out = append(out, UsageRow{NodeID: fmt.Sprintf("node-%03d", i), Bytes: share})
		}
		return out, nil
	}
	reads = func() int64 { return readsN }
	return deltas, usage, reads
}

// RunFixture24h ejecuta la conciliación del fixture de 24 h escribiendo la
// tabla por hora; usado por `make reconcile-hourly` (botnet-build).
// Devuelve las horas en rojo y publica en w las alarmas audited.
func RunFixture24h(w io.Writer, driftPct float64, nodes int) (red int, err error) {
	var published int
	var hours []time.Time
	base := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 24; i++ {
		hours = append(hours, base.Add(time.Duration(i)*time.Hour))
	}
	deltas, usage, _ := Fixture24h(driftPct, nodes)
	r := &Reconciler{
		TolerancePct: TolerancePct,
		Deltas:       deltas,
		Usage:        usage,
		Publish: func(subject string, payload []byte) error {
			published++
			fmt.Fprintf(w, "ALARM %s %s\n", subject, payload)
			return nil
		},
	}
	reps, _, err := r.RunHours(base, 24)
	if err != nil {
		return 0, err
	}
	for _, rep := range reps {
		red++
		if !rep.Red {
			red--
		}
		fmt.Fprintf(w, "hour=%s delta=%d usage=%d drift=%.3f%% %s\n",
			rep.Hour.Format("15:04"), rep.DeltaTotal, rep.UsageTotal,
			rep.DriftPct, map[bool]string{true: "RED", false: "ok"}[rep.Red])
	}
	fmt.Fprintf(w, "reconcile-hourly: 24h, %d horas en rojo (tol %.2f%%), %d alarmas %s, cobro no bloqueado\n",
		red, TolerancePct, published, UsageAuditSubject)
	return red, nil
}
