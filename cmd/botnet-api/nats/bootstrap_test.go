// Topologia de eventos (T-0807): streams con nombre, durable de
// billing que sobrevive a un consumer muerto 5 min (sin perdidas) y
// quota.exhausted consumido por el gateway en <1 s. Requiere
// nats-server con JetStream (testenv lo arranca portable; skip si no).
package nats

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/botnet/botnet/internal/backend/testenv"
)

const testNode = "550e8400-1111-4000-8000-000000000001"

func TestBootstrapTopology(t *testing.T) {
	_, js := testenv.NATS(t)
	// arranque limpio: si quedara topologia de otro run, se rehace.
	js.DeleteStream(StreamNodeState)
	js.DeleteStream(StreamAcctFlush)

	if err := Bootstrap(js); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	// Bootstrap idempotente.
	if err := Bootstrap(js); err != nil {
		t.Fatalf("Bootstrap 2: %v", err)
	}
	// AC1: nats stream ls → NODE_STATE sobre node.{id}.state.
	si, err := js.StreamInfo(StreamNodeState)
	if err != nil {
		t.Fatalf("stream NODE_STATE: %v", err)
	}
	if len(si.Config.Subjects) == 0 || si.Config.Subjects[0] != "node.*.state" {
		t.Errorf("NODE_STATE subjects = %v, quiero node.*.state", si.Config.Subjects)
	}
	// AC1: consumer ls acct_flush durable sobre acct.{node_id}.flush.
	ci, err := js.ConsumerInfo(StreamAcctFlush, DurableAcct)
	if err != nil {
		t.Fatalf("durable %s: %v", DurableAcct, err)
	}
	if ci.Config.Durable != DurableAcct || ci.Config.AckPolicy != nats.AckExplicitPolicy {
		t.Errorf("consumer = %+v, quiero durable acct_flush con AckExplicit", ci.Config)
	}
	if ci.Config.FilterSubject != "" && ci.Config.FilterSubject != "acct.*.flush" {
		t.Errorf("filtro durable = %q, quiero acct.*.flush", ci.Config.FilterSubject)
	}
}

// AC2: consumer de billing matado 5 min → mensajes pendientes > 0 y
// luego redelivered hasta pending=0; el total facturado es invariante
// (cada flush, una sola vez).
func TestDurableAcctFlushNoLoss(t *testing.T) {
	nc, js := testenv.NATS(t)
	js.DeleteStream(StreamAcctFlush)
	if err := Bootstrap(js); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	flushBytes := []uint64{11, 22, 33, 44, 55, 66, 77, 88, 99, 110}
	var want uint64
	for _, b := range flushBytes {
		payload := make([]byte, 8)
		binary.BigEndian.PutUint64(payload, b)
		want += b
		if _, err := js.Publish(AcctFlushSubject(testNode), payload); err != nil {
			t.Fatalf("publish flush: %v", err)
		}
	}

	// primera vida del consumer: ackea la mitad y "muere" (5 min fuera)
	var got uint64 // bytes de usage facturados en ambas vidas del durable
	_, sub, err := ConsumerAcctFlush(js)
	if err != nil {
		t.Fatalf("ConsumerAcctFlush: %v", err)
	}
	// solo se confirma la mitad: el resto quedara pendiente con el
	// consumer muerto (los AckSync sin "no responders" cuentan como
	// confirmados; los que no, se redeliveran — ninguna es perdida).
	safe := 0
	msgs, err := sub.Fetch(5, nats.MaxWait(2*time.Second))
	if err != nil && !errorsIsTimeout(err) {
		t.Fatalf("fetch primera vida: %v", err)
	}
	for _, m := range msgs {
		if err := m.AckSync(); err != nil && strings.Contains(err.Error(), "nats: timeout") {
			continue // sin confirmar: se redelivera con el durable
		}
		got += binary.BigEndian.Uint64(m.Data) // facturado en la primera vida
		safe++
	}
	if safe >= len(flushBytes) {
		t.Fatalf("se confirmaron los %d flushes con el consumer vivo; quiero al menos uno pendiente", len(flushBytes))
	}
	sub.Unsubscribe()

	// 5 min despues (simulados): llegan 2 flushes mas con el consumer
	// muerto.
	for _, b := range []uint64{121, 132} {
		payload := make([]byte, 8)
		binary.BigEndian.PutUint64(payload, b)
		want += b
		if _, err := js.Publish(AcctFlushSubject(testNode), payload); err != nil {
			t.Fatal(err)
		}
	}
	ci, err := js.ConsumerInfo(StreamAcctFlush, DurableAcct)
	if err != nil {
		t.Fatalf("ConsumerInfo tras kill: %v", err)
	}
	if int(ci.NumPending)+ci.NumWaiting == 0 {
		t.Errorf("con el consumer muerto quiero pendientes > 0, tengo pending=%d waiting=%d (safe=%d/%d)", ci.NumPending, ci.NumWaiting, safe, len(flushBytes))
	}

	// reapertura durable: los pendientes se redeliveran hasta pending=0
	_, sub2, err := ConsumerAcctFlush(js)
	if err != nil {
		t.Fatalf("reabrir durable: %v", err)
	}
	defer sub2.Unsubscribe()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := sub2.Fetch(10, nats.MaxWait(2*time.Second))
		if err != nil && !errorsIsTimeout(err) {
			t.Fatalf("fetch redelivered: %v", err)
		}
		for _, m := range msgs {
			got += binary.BigEndian.Uint64(m.Data)
			if err := m.AckSync(); err != nil && strings.Contains(err.Error(), "nats: timeout") {
				continue // redelivered; no es perdida
			}
		}
		ci, err := js.ConsumerInfo(StreamAcctFlush, DurableAcct)
		if err != nil {
			t.Fatal(err)
		}
		if ci.NumPending == 0 && ci.NumWaiting == 0 {
			break
		}
	}
	if ci, err := js.ConsumerInfo(StreamAcctFlush, DurableAcct); err == nil {
		if ci.NumPending != 0 {
			t.Errorf("pending final = %d, quiero 0", ci.NumPending)
		}
	}
	if got != want {
		t.Errorf("bytes de usage procesados = %d, quiero %d (invariante contable del durable)", got, want)
	}
	_ = nc
}

// AC3 (ligazon T-0605): quota.exhausted publicado por el backend llega
// al gateway en <1 s (plano core, cache de 30 s invalidado).
func TestQuotaExhaustedUnder1s(t *testing.T) {
	nc, js := testenv.NATS(t)
	js.DeleteStream(StreamAcctFlush)
	if err := Bootstrap(js); err != nil {
		t.Fatal(err)
	}
	sub, err := nc.SubscribeSync(SubjectQuota)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()
	pub := &Publisher{NC: nc, JS: js}
	start := time.Now()
	pub.PublishQuotaExhausted("tenant-quota-test")
	m, err := sub.NextMsg(1 * time.Second)
	if err != nil {
		t.Fatalf("quota.exhausted no llego en <1 s: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("entrega de quota.exhausted en %v, quiero <1 s", elapsed)
	}
	if string(m.Data) != "tenant-quota-test" {
		t.Errorf("payload = %q", m.Data)
	}
}

func errorsIsTimeout(err error) bool {
	return err == nats.ErrTimeout || strings.Contains(err.Error(), "nats: timeout")
}
