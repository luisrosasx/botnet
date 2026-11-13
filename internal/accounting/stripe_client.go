package accounting

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Cliente Stripe de la pata de conciliación (S-10).
//
// Hasta aquí la conciliación solo sabía hablar con el doble local: el
// `BilledGB` se inyectaba y nadie llamaba a Stripe de verdad. Este
// cliente cubre lo que la conciliación necesita — GB facturado por día —
// y se apunta a la cuenta de test con STRIPE_API_BASE + STRIPE_SECRET,
// sin tocar el contrato del webhook (que ya verifica HMAC local).
//
// Se usa la API de Stripe tal cual: los importes de las líneas de factura
// del día se suman y se convierten a GB con la tarifa del plan.

// StripeAPIDefault es el host real; los tests y el modo test pasan otro.
const StripeAPIDefault = "https://api.stripe.com"

// StripeClient habla con la API de facturación.
type StripeClient struct {
	Base   string       // vacío = StripeAPIDefault
	Secret string       // sk_test_... (nunca viaja al panel)
	HTTP   *http.Client // nil = cliente con timeout de 15 s
	RateGB float64      // € por GB del plan; 0 = 0.02
	Now    func() time.Time
}

// NewStripeFromEnv arma el cliente desde el entorno. Devuelve nil si no
// hay secreto: sin él, la conciliación se queda con el doble.
func NewStripeFromEnv() *StripeClient {
	secret := os.Getenv("STRIPE_SECRET")
	if secret == "" {
		return nil
	}
	return &StripeClient{
		Base:   strings.TrimRight(os.Getenv("STRIPE_API_BASE"), "/"),
		Secret: secret,
	}
}

func (c *StripeClient) base() string {
	if c.Base != "" {
		return c.Base
	}
	return StripeAPIDefault
}

func (c *StripeClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *StripeClient) rate() float64 {
	if c.RateGB > 0 {
		return c.RateGB
	}
	return 0.02
}

// invoiceList es el subconjunto que se consume de /v1/invoices.
type invoiceList struct {
	Data []struct {
		ID      string `json:"id"`
		Created int64  `json:"created"`
		Lines   struct {
			Data []struct {
				Amount int64 `json:"amount"` // céntimos
			} `json:"data"`
		} `json:"lines"`
	} `json:"data"`
	HasMore bool `json:"has_more"`
}

// BilledGB suma lo facturado en el día (UTC) y lo convierte a GB.
// Pagina por si el día trae más de 100 facturas.
func (c *StripeClient) BilledGB(day time.Time) (float64, error) {
	if c.Secret == "" {
		return 0, fmt.Errorf("stripe: sin STRIPE_SECRET")
	}
	start := day.UTC().Truncate(24 * time.Hour)
	end := start.Add(24 * time.Hour)

	var cents int64
	var starting string
	for page := 0; page < 20; page++ {
		q := url.Values{}
		q.Set("created[gte]", strconv.FormatInt(start.Unix(), 10))
		q.Set("created[lt]", strconv.FormatInt(end.Unix(), 10))
		q.Set("limit", "100")
		q.Set("expand[]", "data.lines")
		if starting != "" {
			q.Set("starting_after", starting)
		}
		req, err := http.NewRequest(http.MethodGet, c.base()+"/v1/invoices?"+q.Encode(), nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Authorization", "Bearer "+c.Secret)
		resp, err := c.httpClient().Do(req)
		if err != nil {
			return 0, fmt.Errorf("stripe: petición: %w", err)
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if rerr != nil {
			return 0, rerr
		}
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("stripe: %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var list invoiceList
		if err := json.Unmarshal(body, &list); err != nil {
			return 0, fmt.Errorf("stripe: respuesta ilegible: %w", err)
		}
		for _, inv := range list.Data {
			for _, ln := range inv.Lines.Data {
				cents += ln.Amount
			}
		}
		if !list.HasMore || len(list.Data) == 0 {
			break
		}
		starting = list.Data[len(list.Data)-1].ID
	}
	gb := float64(cents) / 100 / c.rate()
	return gb, nil
}

// StripeGBFromEnv devuelve la pata Stripe de la conciliación ya cableada:
// cliente real si hay STRIPE_SECRET, nil si no (el llamante decide caer al
// doble). Es el punto único de elección real/simulado.
func StripeGBFromEnv() func(time.Time) (float64, error) {
	c := NewStripeFromEnv()
	if c == nil {
		return nil
	}
	return c.BilledGB
}
