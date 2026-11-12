package jobs

import (
	"fmt"
	"io"
	"time"

	"github.com/botnet/botnet/internal/accounting"
)

// BillingReconciler es la conciliacion DIARIA de las tres patas de
// BLUEPRINT §11: `usage` (reloj maestro del nodo) ↔ sumas de
// `sessions` (gateway) ↔ Stripe (GB facturado; en EP-10, el doble
// HTTP local del webhook firmado). El desvio maximo se mide contra la
// tolerancia unica: si pasa, alarma + usage.reconciled=false +
// metrica, y el cobro NO se bloquea.
type BillingReconciler struct {
	TolerancePct float64 // 0 => accounting.TolerancePct (unica)

	Usage    func(day time.Time) (int64, error)   // bytes autoritativos
	Sessions func(day time.Time) (int64, error)   // suma sessions del dia
	StripeGB func(day time.Time) (float64, error) // GB de la factura Stripe
	Mark     func(day time.Time, driftPct float64, reconciled bool) error
	Publish  func(subject string, payload []byte) error // alarma usage.audit
}

// BillingReport es el veredicto de un dia.
type BillingReport struct {
	Day        time.Time
	UsageBytes int64
	SessBytes  int64
	StripeByt  int64
	DriftPct   float64
	Reconciled bool
}

// RunDay concilia un dia. El error solo sale de las fuentes (BD o
// Stripe); un desvio rojo es alarma, no error: nunca bloquea el cobro.
func (r *BillingReconciler) RunDay(day time.Time) (BillingReport, error) {
	tol := r.TolerancePct
	if tol <= 0 {
		tol = TolerancePct
	}
	d := day.UTC().Truncate(24 * time.Hour)
	rep := BillingReport{Day: d}
	master, err := r.Usage(d)
	if err != nil {
		return rep, err
	}
	rep.UsageBytes = master
	if r.Sessions != nil {
		sb, err := r.Sessions(d)
		if err != nil {
			return rep, err
		}
		rep.SessBytes = sb
		rep.DriftPct = accounting.DriftPct(master, sb)
	}
	if r.StripeGB != nil {
		gb, err := r.StripeGB(d)
		if err != nil {
			return rep, err
		}
		rep.StripeByt = int64(gb * accounting.GB)
		if d2 := accounting.DriftPct(master, rep.StripeByt); d2 > rep.DriftPct {
			rep.DriftPct = d2
		}
	}
	rep.Reconciled = rep.DriftPct <= tol
	if r.Mark != nil {
		if err := r.Mark(d, rep.DriftPct, rep.Reconciled); err != nil {
			return rep, err
		}
	}
	if !rep.Reconciled && r.Publish != nil {
		payload := []byte(fmt.Sprintf(
			"day=%s usage=%d sessions=%d stripe=%d drift_pct=%.3f tol_pct=%.2f alarm=1 cobro=no_bloqueado",
			d.Format("2006-01-02"), rep.UsageBytes, rep.SessBytes, rep.StripeByt, rep.DriftPct, tol))
		if err := r.Publish(UsageAuditSubject, payload); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// RunDays recorre dias consecutivos y cuenta las alarmas.
func (r *BillingReconciler) RunDays(start time.Time, n int) (reps []BillingReport, alarms int, err error) {
	for i := 0; i < n; i++ {
		rep, e := r.RunDay(start.AddDate(0, 0, i))
		if e != nil {
			return reps, alarms, e
		}
		if !rep.Reconciled {
			alarms++
		}
		reps = append(reps, rep)
	}
	return reps, alarms, nil
}

// BillingFixture3Patas arma el fixture de 24 h (un dia) con un desvio
// objetivo aplicando perturbation determinista a sessions y Stripe
// sobre el usage del nodo. Con driftPct=0.4 la conciliacion pasa; con
// 0.6 dispara la alarma sin bloquear el cobro (criterio 3 T-1004).
func BillingFixture3Patas(day time.Time, usageBytes int64, driftPct float64) *BillingReconciler {
	return &BillingReconciler{
		TolerancePct: TolerancePct,
		Usage: func(time.Time) (int64, error) {
			return usageBytes, nil
		},
		Sessions: func(time.Time) (int64, error) {
			return int64(float64(usageBytes) * (1 + driftPct/100)), nil
		},
		StripeGB: func(time.Time) (float64, error) {
			return accounting.GBFloat(int64(float64(usageBytes) * (1 - driftPct/100))), nil
		},
	}
}

// RunBillingFixture escribe la conciliacion del fixture para el
// comando de release (docs/runbooks/billing.md).
func RunBillingFixture(w io.Writer, usageBytes int64, driftPct float64) error {
	day := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var alarms int
	r := BillingFixture3Patas(day, usageBytes, driftPct)
	r.Mark = func(d time.Time, drift float64, ok bool) error {
		fmt.Fprintf(w, "day=%s drift=%.3f%% reconciled=%v\n", d.Format("2006-01-02"), drift, ok)
		return nil
	}
	r.Publish = func(subject string, payload []byte) error {
		alarms++
		fmt.Fprintf(w, "ALARM %s %s\n", subject, payload)
		return nil
	}
	rep, err := r.RunDay(day)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "billing-reconcile: drift=%.3f%% tol=%.2f%% alarmes=%d cobro=no_bloqueado\n",
		rep.DriftPct, TolerancePct, alarms)
	return nil
}
