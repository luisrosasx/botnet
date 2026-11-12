package proxy

import (
	"context"
	"io"
	"testing"
	"time"
)

// dummies del plano: Redis quota:{t}:state, NATS quota.exhausted y sink
// contable sessions.closed_reason.
type fixtureState struct {
	states map[string]string
	calls  int
}

func (f *fixtureState) QuotaState(_ context.Context, tenant string) (string, error) {
	f.calls++
	return f.states[tenant], nil
}

type fakeNATS struct {
	handlers map[string]func([]byte)
}

func (f *fakeNATS) Subscribe(subject string, fn func([]byte)) error {
	f.handlers[subject] = fn
	return nil
}
func (f *fakeNATS) publish(subject, payload string) {
	if h := f.handlers[subject]; h != nil {
		h([]byte(payload))
	}
}

type sinkDouble struct {
	rows map[string]ClosedReason
}

func (s *sinkDouble) Closed(sessionID string, reason ClosedReason) {
	if s.rows == nil {
		s.rows = map[string]ClosedReason{}
	}
	s.rows[sessionID] = reason
}

func newLiveSession(reg *Registry, opener *FakeOpener, tenant, id string) (*Session, io.ReadWriteCloser, error) {
	s, err := opener.OpenStream(context.Background(), StreamMeta{SessionID: id})
	if err != nil {
		return nil, nil, err
	}
	sess := &Session{SessionID: id, TenantID: tenant, OpenedAt: time.Now().UTC(), stream: s}
	reg.Register(sess)
	return sess, s, nil
}

// T-0605 AC1: fixture quota:{t}:state=exhausted + NATS quota.exhausted ->
// el stream vivo del tenant recibe GOAWAY con closed_reason='quota' y
// libera contadores/registro en < 60 s (cronometro del test).
func TestQuotaExhaustedEventGOAWAYUnder60s(t *testing.T) {
	opener := &FakeOpener{}
	sink := &sinkDouble{}
	reg := NewRegistry(sink)
	var sa io.ReadWriteCloser
	if _, s, err := newLiveSession(reg, opener, "t1", "sess-a"); err != nil {
		t.Fatal(err)
	} else {
		sa = s
	}
	if _, _, err := newLiveSession(reg, opener, "t1", "sess-b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := newLiveSession(reg, opener, "t2", "sess-c"); err != nil {
		t.Fatal(err)
	}

	fixture := &fixtureState{states: map[string]string{"t1": QuotaStateExhausted}}
	nats := &fakeNATS{handlers: map[string]func([]byte){}}
	guard := NewGuard(reg, fixture)
	if err := guard.Start(nats); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	nats.publish(SubjectQuotaExhausted, "t1")
	elapsed := time.Since(start)

	if reg.Count() != 1 {
		t.Fatalf("registro tras GOAWAY = %d sesiones, want 1 (solo t2 viva)", reg.Count())
	}
	if got := sink.rows["sess-a"]; got != ClosedQuota {
		t.Fatalf("sessions.closed_reason sess-a = %q, want %q", got, ClosedQuota)
	}
	if got := sink.rows["sess-b"]; got != ClosedQuota {
		t.Fatalf("sessions.closed_reason sess-b = %q, want %q", got, ClosedQuota)
	}
	if sink.rows["sess-c"] != "" {
		t.Fatalf("t2 cortada sin razon: %q", sink.rows["sess-c"])
	}
	if elapsed >= 60*time.Second {
		t.Fatalf("corte por cuota tardo %v, want < 60s", elapsed)
	}
	// el stream del doble esta cerrado (GOAWAY libera el stream)
	if _, err := sa.Read(make([]byte, 1)); err == nil {
		t.Fatal("stream sess-a seguia vivo tras GOAWAY")
	}
	t.Logf("GOAWAY por cuota completo en %v", elapsed)
}

// T-0605 AC2: la carrera del cache de 30 s queda acotada por el tope del
// bucket: a 5 Mbps, <= 19 MB (MiB) por sesion cortada. Se comprueban las
// dos formulas vigentes (cable Mbps y entitlement kbps x 1024).
func TestOverchargeBoundAt5Mbps(t *testing.T) {
	const mib19 = 19 * 1024 * 1024.0
	if got := MaxOverchargeBytes(MbpsToBytes(5)); got > mib19 {
		t.Fatalf("sobre-precio 5 Mbps cable = %.0f B > 19 MiB", got)
	}
	// formula del entitlement del agente: 5 Mbps ~ bandwidth_cap_kbps 625
	if got := MaxOverchargeBytes(KbpsToBytes(625)); got > mib19 {
		t.Fatalf("sobre-precio 5 Mbps entitlement = %.0f B > 19 MiB", got)
	}
	// fixture del criterio: sesion cortada tras la carrera de 30 s
	fixture := &fixtureState{states: map[string]string{"t9": QuotaStateExhausted}}
	nats := &fakeNATS{handlers: map[string]func([]byte){}}
	reg := NewRegistry(nil)
	opener := &FakeOpener{}
	if _, _, err := newLiveSession(reg, opener, "t9", "sess-x"); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(reg, fixture)
	if err := guard.Start(nats); err != nil {
		t.Fatal(err)
	}
	// la via cache (sin evento) corta igual: CheckTenant lee una vez y
	// corta; el TTL evita la tormenta de lecturas.
	if err := guard.CheckTenant(context.Background(), "t9"); err != nil {
		t.Fatal(err)
	}
	if reg.Count() != 0 {
		t.Fatalf("t9 no fue cortada por la via cache")
	}
}

// T-0605 AC3: sessions.closed_reason de la sesion cortada es 'quota' —
// causa tipada, no 'error' ni 'client' (separacion contable A3-04).
func TestClosedReasonIsTypedQuota(t *testing.T) {
	sink := &sinkDouble{}
	reg := NewRegistry(sink)
	opener := &FakeOpener{}
	sess, _, err := newLiveSession(reg, opener, "t1", "sess-q")
	if err != nil {
		t.Fatal(err)
	}
	sess.CloseWith(ClosedQuota)
	if sink.rows["sess-q"] != ClosedQuota {
		t.Fatalf("closed_reason = %q, want %q", sink.rows["sess-q"], ClosedQuota)
	}
	if ClosedQuota == ClosedError || ClosedQuota == ClosedClient {
		t.Fatal("ClosedQuota no puede coincidir con error/client")
	}
	if sess.Alive() {
		t.Fatal("sesion viva tras cierre")
	}
	// idempotencia: el segundo cierre no pisa la causa
	sess.CloseWith(ClosedError)
	if sess.Reason() != ClosedQuota {
		t.Fatalf("causa pisada tras segundo cierre: %q", sess.Reason())
	}
}
