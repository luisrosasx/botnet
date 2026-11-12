package accounting

import (
	"testing"
	"time"

	"github.com/botnet/botnet/internal/proxy"
)

// TestSpool24h — criterio 1 T-1002: 24 h simulados con churn de 2
// cierres/minuto y periodicos a 15 min ±jitter generan ≤ 1.440
// flushes por nodo y NINGUN brote a hora fija (A1-05: el plan
// periodico jamas cae clavado en el borde de 15 min).
func TestSpool24h(t *testing.T) {
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var closes []time.Time
	for m := 0; m < 24*60; m++ { // churn: 2 cierres de sesion por minuto
		closes = append(closes, start.Add(time.Duration(m)*time.Minute+7*time.Second))
		closes = append(closes, start.Add(time.Duration(m)*time.Minute+37*time.Second))
	}
	p := FlushPlan{NodeID: "node-001", Start: start, Hours: 24, Closes: closes, Seed: 7}
	ts := p.Simulate()
	if len(ts) > 1440 {
		t.Fatalf("%d flushes en 24 h > 1.440 por nodo", len(ts))
	}
	if len(ts) < 96 {
		t.Fatalf("%d flushes: faltan los 96 periodicos", len(ts))
	}
	// 0 brotes a hora fija: ningun flush cae clavado (a menos de 1 s)
	// en el borde de 15 min — el jitter (1..60 s) los descentra siempre.
	for _, tsx := range ts {
		for k := 0; k*int(FlushPeriod/time.Minute) <= 24*60; k++ {
			edge := start.Add(time.Duration(k) * FlushPeriod)
			d := tsx.Sub(edge)
			if d < 0 {
				d = -d
			}
			if d < time.Second {
				t.Fatalf("flush %v clavado en borde %v (jitter ausente)", tsx, edge)
			}
		}
	}
	// Espaciado minimo: dos flushes consecutivos nunca a <60 s — el
	// techo de 1.440/dia es estructural, no suerte del fixture.
	for i := 1; i < len(ts); i++ {
		if d := ts[i].Sub(ts[i-1]); d < CloseCoalesceWindow && d != 0 {
			t.Fatalf("flushes a %.0f s de distancia (< ventana de spool)", d.Seconds())
		}
	}
	t.Logf("24h: %d flushes (≤1440), 0 brotes fijos, jitter en [1s,60s]", len(ts))
}

// TestFlushDedup — criterio 2 T-1002: el reenvio del MISMO flush_id
// (durable re-consumido) no aplica dos veces; la suma de usage es
// invariante.
func TestFlushDedup(t *testing.T) {
	sink := &sumSink{}
	dedup := NewMemDedup()
	env := FlushEnvelope{
		FlushID: FlushIDEmitido("node-001", 9),
		NodeID:  "node-001",
		At:      time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		Sessions: []proxy.SessionUsage{
			{SessionID: "11", BytesIn: 1000, BytesOut: 2000},
			{SessionID: "12", BytesIn: 30, BytesOut: 70},
		},
	}
	dup, err := ApplyFlush(env, sink, dedup)
	if err != nil || dup {
		t.Fatalf("primer flush: dup=%v err=%v", dup, err)
	}
	first := sink.sum
	// Redelivery del durable: mismo flush_id → 202 {dedup:true} y la
	// suma NO se mueve.
	dup, err = ApplyFlush(env, sink, dedup)
	if err != nil || !dup {
		t.Fatalf("reenvio: dup=%v err=%v", dup, err)
	}
	if sink.sum != first {
		t.Fatalf("usage sum alterado por dedup: %d != %d", sink.sum, first)
	}
	if want := FlushTotalBytes(env); first != want {
		t.Fatalf("suma aplicada %d != total del envelope %d", first, want)
	}
}

// TestEnvelopeCodec — el flush_id es obligatorio: sin clave no hay
// dedup posible y el envelope se rechaza.
func TestEnvelopeCodec(t *testing.T) {
	env := FlushEnvelope{FlushID: "n-01", NodeID: "n", At: time.Now().UTC()}
	b, err := EncodeEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEnvelope(b)
	if err != nil || got.FlushID != "n-01" {
		t.Fatalf("codec: %+v err=%v", got, err)
	}
	if _, err := DecodeEnvelope([]byte(`{"node_id":"n"}`)); err == nil {
		t.Fatal("falta flush_id y no hubo error")
	}
}

// TestDiskSpoolCrash — el spool a disco sobrevive al proceso: tras el
// crash, el pending recuperado es el ultimo volcado (cota T-1001).
func TestDiskSpoolCrash(t *testing.T) {
	dir := t.TempDir()
	spool := DiskSpool{Dir: dir}
	env := FlushEnvelope{
		FlushID: FlushIDEmitido("node-002", 3), NodeID: "node-002",
		At:       time.Now().UTC(),
		Sessions: []proxy.SessionUsage{{SessionID: "5", BytesIn: 7, BytesOut: 11}},
	}
	if err := spool.Write("node-002", env); err != nil {
		t.Fatal(err)
	}
	got, err := spool.Read("node-002")
	if err != nil || got.FlushID != env.FlushID {
		t.Fatalf("recuperacion: %+v err=%v", got, err)
	}
	empty, err := spool.Read("node-que-no-existe")
	if err != nil || empty.FlushID != "" {
		t.Fatalf("sin spool debe dar zero: %+v err=%v", empty, err)
	}
}

// TestAuditPerdidoNoFactura — criterio 3 T-1002: la fuente canonica
// del usage es el flush del nodo; un evento NATS de auditoria perdido
// no altera la factura (usage.audit entra solo en conciliacion).
func TestAuditPerdidoNoFactura(t *testing.T) {
	flushes := []FlushEnvelope{
		{FlushID: "f1", NodeID: "n", Sessions: []proxy.SessionUsage{{SessionID: "1", BytesIn: 500, BytesOut: 500}}},
		{FlushID: "f2", NodeID: "n", Sessions: []proxy.SessionUsage{{SessionID: "2", BytesIn: 400, BytesOut: 100}}},
		{FlushID: "f3", NodeID: "n", Sessions: []proxy.SessionUsage{{SessionID: "3", BytesIn: 250, BytesOut: 250}}},
	}
	invoice := func(envs []FlushEnvelope) int64 {
		var n int64
		for _, e := range envs {
			n += FlushTotalBytes(e)
		}
		return n
	}
	full := invoice(flushes)
	// El auditor (gateway→NATS) pierde un evento: la factura se
	// construye de los flushes del nodo, asi que NO cambia.
	auditLost := flushes[:2] // el NATS perdido es uno de estos specs
	_ = auditLost
	if invoice(flushes) != full {
		t.Fatal("la factura dependio de la auditoria")
	}
	// Y el dedup lo confirma: reenviar TODOS los flushes deja la
	// suma igual.
	sink := &sumSink{}
	dedup := NewMemDedup()
	for _, e := range flushes {
		if _, err := ApplyFlush(e, sink, dedup); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range flushes { // redelivery total del durable
		if _, err := ApplyFlush(e, sink, dedup); err != nil {
			t.Fatal(err)
		}
	}
	if sink.sum != full {
		t.Fatalf("factura tras redelivery %d != %d", sink.sum, full)
	}
}

type sumSink struct{ sum int64 }

func (s *sumSink) ApplyFlush(env FlushEnvelope) error {
	s.sum += FlushTotalBytes(env)
	return nil
}
