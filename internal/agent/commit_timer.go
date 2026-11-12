package agent

import "time"

// CommitTimer arma el commit de una actualización ya descargada tras
// CommitAfter de latidos sanos (T-1404 AC2, cierra H2). Antes de esto el
// commit solo ocurría si el proceso reiniciaba por exit 75: un update
// descargado podía quedarse sin fijar indefinidamente.
//
// Sano = 200 con sesiones servidas (streams > 0). Un fallo, un cero de
// streams o un bye reinician la racha: no se fija una versión que no ha
// demostrado servir tráfico.
type CommitTimer struct {
	after time.Duration
	now   func() time.Time
	since time.Time
}

// NewCommitTimer: `after` <= 0 usa CommitAfter.
func NewCommitTimer(after time.Duration, now func() time.Time) *CommitTimer {
	if after <= 0 {
		after = CommitAfter
	}
	if now == nil {
		now = time.Now
	}
	return &CommitTimer{after: after, now: now}
}

// Beat registra un latido y dice si toca commitear. Al disparar reinicia
// la racha para no commitear dos veces seguidas.
func (c *CommitTimer) Beat(healthy bool, streams int) bool {
	now := c.now()
	if !healthy || streams <= 0 {
		c.since = time.Time{}
		return false
	}
	if c.since.IsZero() {
		c.since = now
		return false
	}
	if now.Sub(c.since) >= c.after {
		c.since = time.Time{}
		return true
	}
	return false
}

// Streak devuelve cuánto lleva la racha sana (0 si está rota).
func (c *CommitTimer) Streak() time.Duration {
	if c.since.IsZero() {
		return 0
	}
	return c.now().Sub(c.since)
}
