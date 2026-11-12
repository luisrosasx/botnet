// Package nats: topologia de eventos del backend (BLUEPRINT §9). Los
// streams con nombre y los durables donde la factura lo exige; NATS es
// auditoria, nunca reloj maestro (usage.audit viaja por el plano core y
// jamas se factura, docs/runbooks/events.md).
package nats

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
)

// Nombres fijos del contrato operativo.
const (
	StreamNodeState   = "NODE_STATE" // node.{id}.state (JetStream)
	StreamAcctFlush   = "ACCT_FLUSH" // acct.{node_id}.flush — durable de billing
	DurableAcct       = "acct_flush" // consumer durable del flush
	SubjectQuota      = "quota.exhausted"
	SubjectUsageAudit = "usage.audit" // auditoria; NUNCA facturable
	AcctAckWait       = 60 * time.Second
)

// Bootstrap idempotente: crea streams y el durable de billing si no
// existen. Un backend que arranca sobre un NATS ya montado no toca lo
// que ya esta bien (AddConsumer sobre un durable existente es no-op).
func Bootstrap(js nats.JetStreamContext) error {
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:      StreamNodeState,
		Subjects:  []string{"node.*.state"},
		Storage:   nats.FileStorage,
		MaxAge:    7 * 24 * time.Hour, // el estado viejo no interesa al panel
		Retention: nats.LimitsPolicy,
	}); err != nil && err != nats.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("nats: stream %s: %w", StreamNodeState, err)
	}
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:      StreamAcctFlush,
		Subjects:  []string{"acct.*.flush"},
		Storage:   nats.FileStorage,
		Retention: nats.WorkQueuePolicy, // cada flush se factura una sola vez
		MaxAge:    72 * time.Hour,
	}); err != nil && err != nats.ErrStreamNameAlreadyInUse {
		return fmt.Errorf("nats: stream %s: %w", StreamAcctFlush, err)
	}
	if _, err := js.AddConsumer(StreamAcctFlush, &nats.ConsumerConfig{
		Durable:       DurableAcct,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       AcctAckWait,
		DeliverPolicy: nats.DeliverAllPolicy,
		// MaxWaiting debe coincidir con el PullMaxWaiting del
		// subscription: si no, reabrir el durable tras un kill falla.
		MaxWaiting: 1024,
	}); err != nil && err != nats.ErrConsumerNameAlreadyInUse {
		return fmt.Errorf("nats: durable %s: %w", DurableAcct, err)
	}
	return nil
}

// NodeStateSubject es el sujeto JetStream del estado de un nodo.
func NodeStateSubject(nodeID string) string { return "node." + nodeID + ".state" }

// AcctFlushSubject es el sujeto del flush contable de un nodo.
func AcctFlushSubject(nodeID string) string { return "acct." + nodeID + ".flush" }

// ConsumerAcctFlush reabre el subscription durable del billing (tras
// un kill, los pendientes se redeliveran hasta pending=0).
func ConsumerAcctFlush(js nats.JetStreamContext) (nats.JetStream, *nats.Subscription, error) {
	sub, err := js.PullSubscribe(AcctFlushSubject("*"), DurableAcct,
		nats.PullMaxWaiting(1024), nats.ManualAck())
	return js, sub, err
}

// Publisher es la cara que consumen los handlers: publica el estado del
// nodo por JetStream y el corte de cuota por el plano core (un cache de
// 30 s no necesita persistencia; lo contable si).
type Publisher struct {
	NC *nats.Conn
	JS nats.JetStreamContext
}

func (p *Publisher) PublishNodeState(nodeID string, payload []byte) {
	if p.JS == nil {
		return
	}
	p.JS.Publish(NodeStateSubject(nodeID), payload)
}

func (p *Publisher) PublishQuotaExhausted(tenantID string) {
	p.NC.Publish(SubjectQuota, []byte(tenantID))
}
