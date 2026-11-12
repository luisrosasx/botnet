package accounting

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// WebhookSignatureHeader es el encabezado del esquema Stripe:
// `Stripe-Signature: t=<unix>,v1=<hex hmac-sha256>`. La simulacion
// local (doble HTTP sin red externa) firma y verifica con ESTE mismo
// codigo: el dia que llegue el Stripe real no cambia el contrato.
const WebhookSignatureHeader = "Stripe-Signature"

// WebhookSkewMax es la antiguedad maxima del timestamp firmado:
// protege del replay sin relojes NTP perfectos.
const WebhookSkewMax = 5 * time.Minute

// SignWebhook produce el valor del header para (secret, ts, body):
// la firma es HMAC-SHA256 de "<t>.<body>" — identica al esquema de
// Stripe, asi que verificador y firmante nunca se separan.
func SignWebhook(secret string, ts time.Time, body []byte) string {
	t := ts.UTC().Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", t, hex.EncodeToString(mac.Sum(nil)))
}

// Errores tipados del webhook (EP-11 T-1104): el handler traduce
// 400 = POST ilegible/sin firma, 403 = firma forjada o replay — un
// POST forjado con TLS valido ya no regala cuota: la firma manda.
var (
	ErrWebhookHeaderMalformed = errors.New("stripe: header de firma ausente o incompleto")
	ErrWebhookSignature       = errors.New("stripe: firma invalida")
	ErrWebhookReplay          = errors.New("stripe: timestamp fuera de ventana")
)

// VerifyWebhook valida el header contra el body con reloj inyectable.
// Devuelve el timestamp firmado; error tipado si la firma no cuadra
// o el t esta fuera de la ventana de replay.
func VerifyWebhook(secret, header string, body []byte, now time.Time) (time.Time, error) {
	if header == "" {
		return time.Time{}, fmt.Errorf("%w: ausente", ErrWebhookHeaderMalformed)
	}
	var ts int64
	var v1 string
	for _, part := range strings.Split(header, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("%w: t invalido", ErrWebhookHeaderMalformed)
			}
			ts = n
		case "v1":
			v1 = val
		}
	}
	if ts == 0 || v1 == "" {
		return time.Time{}, fmt.Errorf("%w: incompleto", ErrWebhookHeaderMalformed)
	}
	signed := time.Unix(ts, 0).UTC()
	if now.Sub(signed) > WebhookSkewMax || signed.Sub(now) > WebhookSkewMax {
		return signed, fmt.Errorf("%w: t=%d", ErrWebhookReplay, ts)
	}
	raw, err := hex.DecodeString(v1)
	if err != nil {
		return signed, fmt.Errorf("%w: v1 no hexadecimal", ErrWebhookHeaderMalformed)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	if !hmac.Equal(mac.Sum(nil), raw) {
		return signed, fmt.Errorf("%w: v1=%s", ErrWebhookSignature, v1)
	}
	return signed, nil
}

// InvoiceEvent es el payload `invoice.paid` del webhook. `gb` es lo
// facturado por el periodo del dia; `node_ids` permite marcar
// usage.reconciled sin consultar el grafo tenants→nodes.
type InvoiceEvent struct {
	Type      string   `json:"type"`
	Day       string   `json:"day"`
	GB        float64  `json:"gb"`
	NodeIDs   []string `json:"node_ids"`
	InvoiceID string   `json:"invoice_id"`
	// EventID es el id del evento para la idempotencia de
	// re-entregas (T-1104 AC3): re-entregado el mismo no mueve la
	// cuota dos veces.
	EventID string `json:"event_id"`
}

// DecodeInvoiceEvent parsea y tipa; Type debe ser invoice.paid para
// aplicarse (otros eventos se acusan de recibido sin efecto).
func DecodeInvoiceEvent(body []byte) (InvoiceEvent, error) {
	var ev InvoiceEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return ev, err
	}
	if ev.Day == "" {
		return ev, fmt.Errorf("stripe: invoice sin day")
	}
	return ev, nil
}

// StripeDayLegger es la pata Stripe de la conciliacion diaria: GB
// facturado por dia (la hoja de la factura, no el contador local).
type StripeDayLegger struct {
	// BilledGB devuelve el GB que el (falso o real) Stripe cobro por
	// el dia; el doble HTTP local lo sirve desde su propio fixture.
	BilledGB func(day time.Time) (float64, error)
}

// GBFor es el adaptador que consume jobs.RunBillingDay.
func (s StripeDayLegger) GBFor(day time.Time) (float64, error) {
	return s.BilledGB(day)
}
