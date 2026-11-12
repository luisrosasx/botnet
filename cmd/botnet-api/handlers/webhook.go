package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/botnet/botnet/internal/accounting"
)

// billingWebhook — POST /v1/billing/webhook (T-1004, A3-X5): recibe
// `invoice.paid` del plano Stripe — en EP-10, el doble HTTP local que
// firma con el MISMO esquema HMAC-SHA256 (accounting.SignWebhook).
// Payload sin firma o malformado: 400; firma forjada o replay: 403
// (T-1104 AC1), siempre sin aplicar nada. Webhook validado: marca
// usage.reconciled del dia y acusa 200. El cobro jamas se bloquea
// aqui: la conciliacion es alarmado, no portero.
// webhookEventTTL: la ventana de idempotencia por event.id cubre las
// re-entregas de Stripe (horas) con margen de dias.
const webhookEventTTL = 7 * 24 * time.Hour

func billingWebhook(d *Deps) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if d.StripeSecret == "" || d.Repo == nil {
			return jsonError(c, http.StatusServiceUnavailable, "plano Stripe apagado")
		}
		body := c.Body()
		signedAt, err := accounting.VerifyWebhook(d.StripeSecret, c.Get(accounting.WebhookSignatureHeader), body, time.Now().UTC())
		if err != nil {
			// T-1104 AC1: sin firma/malformado 400; firma forjada o
			// replay 403 — el POST forjado con TLS valido no regala cuota.
			switch {
			case errors.Is(err, accounting.ErrWebhookSignature), errors.Is(err, accounting.ErrWebhookReplay):
				return jsonError(c, http.StatusForbidden, err.Error())
			default:
				return jsonError(c, http.StatusBadRequest, err.Error())
			}
		}
		ev, err := accounting.DecodeInvoiceEvent(body)
		if err != nil {
			return jsonError(c, http.StatusBadRequest, err.Error())
		}
		if ev.Type != "invoice.paid" {
			return c.Status(http.StatusOK).JSON(fiber.Map{"received": true, "ignored": ev.Type})
		}
		// AC3: re-entrega del MISMO event.id es idempotente — SETNX en
		// Redis sella el evento antes de tocar nada.
		if ev.EventID != "" && d.RDB != nil {
			ok, serr := d.RDB.SetNX(c.Context(), "stripe:evt:"+ev.EventID, "1", webhookEventTTL).Result()
			if serr == nil && !ok {
				return c.Status(http.StatusOK).JSON(fiber.Map{
					"received": true, "duplicate": ev.EventID, "marked": 0,
				})
			}
		}
		marked, err := d.Repo.MarkUsageReconciled(c.Context(), ev.Day, ev.NodeIDs)
		if err != nil {
			return jsonError(c, http.StatusInternalServerError, err.Error())
		}
		return c.Status(http.StatusOK).JSON(fiber.Map{
			"received":    true,
			"invoice_id":  ev.InvoiceID,
			"event_id":    ev.EventID,
			"day":         ev.Day,
			"marked":      marked,
			"signed_at":   signedAt.Format(time.RFC3339),
			"gb_invoiced": ev.GB,
		})
	}
}
