package accounting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// S-10: el cliente apunta a una base configurable y convierte céntimos a
// GB con la tarifa del plan, sin tocar el contrato del webhook.
func TestStripeBilledGBContraDobleHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk_test_x" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/v1/invoices" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{map[string]any{
				"id":      "in_1",
				"created": time.Now().Unix(),
				"lines": map[string]any{"data": []any{
					map[string]any{"amount": 100}, // 1 €
					map[string]any{"amount": 50},  // 0,50 €
				}},
			}},
			"has_more": false,
		})
	}))
	defer srv.Close()

	c := &StripeClient{Base: srv.URL, Secret: "sk_test_x", RateGB: 0.5}
	gb, err := c.BilledGB(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if gb != 3 { // 1,50 € a 0,50 €/GB
		t.Fatalf("GB = %v, want 3", gb)
	}
}

func TestStripeBilledGBPagina(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("starting_after") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":     []any{map[string]any{"id": "in_1", "lines": map[string]any{"data": []any{map[string]any{"amount": 100}}}}},
				"has_more": true,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":     []any{map[string]any{"id": "in_2", "lines": map[string]any{"data": []any{map[string]any{"amount": 100}}}}},
			"has_more": false,
		})
	}))
	defer srv.Close()

	c := &StripeClient{Base: srv.URL, Secret: "sk_test_x", RateGB: 0.5}
	if _, err := c.BilledGB(time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("llamadas = %d, want 2 (una por página)", calls)
	}
}

func TestStripeSinSecretoFalla(t *testing.T) {
	c := &StripeClient{}
	if _, err := c.BilledGB(time.Now()); err == nil {
		t.Fatal("sin secreto debe fallar")
	}
}

func TestStripeErrorHTTPSeReporta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"nope"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := &StripeClient{Base: srv.URL, Secret: "sk_test_x"}
	if _, err := c.BilledGB(time.Now()); err == nil {
		t.Fatal("un 401 debe devolver error")
	}
}

func TestStripeGBFromEnvEligeRealODoble(t *testing.T) {
	t.Setenv("STRIPE_SECRET", "")
	if StripeGBFromEnv() != nil {
		t.Fatal("sin secreto debe devolver nil (el llamante cae al doble)")
	}
	t.Setenv("STRIPE_SECRET", "sk_test_x")
	if StripeGBFromEnv() == nil {
		t.Fatal("con secreto debe devolver la pata real")
	}
}
