package proxy

import (
	"fmt"
	"io"
	"net/http"
	"sync"
)

// Admision del plano de datos (§7, A1-17): el nodo ejecuta
//   admitir = min(150 streams nominales, cuota concurrente del tenant,
//                 8 tenants activos).
// La cuota de negocio (cuota concurrente) la manda el backend por
// tenant; el tope de 8 tenants es el maximo de inquilinos simultaneos
// que un destino residencial acepta — datacenter en IP de casa como
// mucho con 8 inquilinos. La densidad se mide: botnet_tenant_count se
// publica en /metrics.

// MaxTenantsPerNode es el tope de tenants concurrentes por nodo (A1-17).
const MaxTenantsPerNode = 8

// tenantState lleva las sesiones vivas de un tenant y su cuota.
type tenantState struct {
	sessions int
	quota    int
}

// Admission decide que sesiones se admiten en el nodo.
type Admission struct {
	mu         sync.Mutex
	tenants    map[string]*tenantState
	maxTenants int
	maxStreams int
}

// NewAdmission crea la admision con los topes del blueprint; maxStreams
// <=0 toma MaxNominalStreams.
func NewAdmission(maxStreams int) *Admission {
	if maxStreams <= 0 {
		maxStreams = MaxNominalStreams
	}
	return &Admission{
		tenants:    make(map[string]*tenantState),
		maxTenants: MaxTenantsPerNode,
		maxStreams: maxStreams,
	}
}

// Decision es el resultado de la admision con la causa del rechazo.
type Decision struct {
	Admitted bool
	Reason   string // "ok", "tenant_cap", "tenant_quota", "stream_cap"
}

// Admit intenta admitir una sesion del tenant con su cuota concurrente.
// streamsOpen es el conteo tecnico actual (streams H2 vivos). El minimo de
// las tres capacidades manda: con 8 tenants no entra un noveno aunque
// queden streams nominales de sobra (T-0606 AC2).
func (a *Admission) Admit(tenantID string, tenantConcurrentQuota, streamsOpen int) Decision {
	a.mu.Lock()
	defer a.mu.Unlock()

	if streamsOpen >= a.maxStreams {
		return Decision{Reason: "stream_cap"}
	}
	st, known := a.tenants[tenantID]
	if !known && len(a.tenants) >= a.maxTenants {
		return Decision{Reason: "tenant_cap"}
	}
	if st == nil {
		st = &tenantState{} // solo se registra si la sesion resulta admitida
	}
	if tenantConcurrentQuota > 0 && st.sessions >= tenantConcurrentQuota {
		return Decision{Reason: "tenant_quota"}
	}
	st.sessions++
	if !known {
		a.tenants[tenantID] = st
	}
	return Decision{Admitted: true, Reason: "ok"}
}

// Leave descuenta la sesion al cerrarse; si el tenant se queda a cero
// deja de contar para el tope de 8.
func (a *Admission) Leave(tenantID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.tenants[tenantID]
	if st == nil {
		return
	}
	if st.sessions > 0 {
		st.sessions--
	}
	if st.sessions == 0 {
		delete(a.tenants, tenantID)
	}
}

// TenantCount devuelve los tenants activos (la densidad medida del
// destino, metrica botnet_tenant_count).
func (a *Admission) TenantCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.tenants)
}

// WriteMetrics emite el gauge botnet_tenant_count en formato Prometheus
// text (el unico endpoint /metrics del nodo, stdlib puro).
func (a *Admission) WriteMetrics(w io.Writer) error {
	n := a.TenantCount()
	_, err := fmt.Fprintf(w, "# TYPE botnet_tenant_count gauge\nbotnet_tenant_count %d\n", n)
	return err
}

// MetricsHandler sirve GET /metrics con el gauge de densidad.
func (a *Admission) MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = a.WriteMetrics(w)
	})
}
