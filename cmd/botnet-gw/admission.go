package main

// Streams dinamicos por clase (EP-09 T-0905, cierra A2-08): el tope
// tecnico por clase sale de `device_class` — A 150, B 100, C 64 streams
// nominales, tabla versionada — y el negocio multi-tenant deja de topar
// a 8 sesiones por nodo: el 8 canonico sigue mandando como tope de
// tenants ACTIVOS por nodo (§7, mandato A1-17), no como cuota por
// tenant. El minimo manda: admision = min(tope de clase, cuota
// concurrente del tenant) y, para un tenant nuevo, <= 8 activos.

// AdmissionTableVersion: version de la tabla clase→tope. Cambiar topes =
// subir version (el gateway loggea la que aplico; T-1304 la densea).
const AdmissionTableVersion = 1

// MaxActiveTenantsPerNode: el tope de 8 de P2 sigue mandando, pero solo
// como abanico de tenants distintos por nodo (densidad vista por el
// destino), jamas como streams por tenant.
const MaxActiveTenantsPerNode = 8

// classCapTable es la fixture versionada de A2-08.
var classCapTable = map[byte]int{'A': 150, 'B': 100, 'C': 64}

// ClassCap: streams nominales por clase tecnica. ok=false con clase
// desconocida (el gateway no admite lo que no puede topaar).
func ClassCap(class byte) (int, bool) {
	n, ok := classCapTable[class]
	return n, ok
}

// AdmissionReason es el veredicto negativa con nombre: entra en logs,
// en closed_reason y en la metrica de equidad.
type AdmissionReason string

const (
	AdmitOK         AdmissionReason = "ok"
	AdmitClassCap   AdmissionReason = "class_cap"
	AdmitTenantQuota AdmissionReason = "tenant_quota"
	AdmitTenantFan  AdmissionReason = "tenant_fanout"
	AdmitUnknownCls AdmissionReason = "unknown_class"
)

// AdmissionPolicy gobierna la stream `(tenantStreams+1)`-ésima sobre un
// nodo que ya lleva `tenantStreams` de ese tenant y `activeTenants`
// tenants distintos.
type AdmissionPolicy struct{ Version int }

func NewAdmissionPolicy() *AdmissionPolicy { return &AdmissionPolicy{Version: AdmissionTableVersion} }

// Admit: el min manda (AC3). `tenantNew` = el tenant no tiene stream
// activo en este nodo; entonces tambien pesa el tope de 8 tenants.
func (p *AdmissionPolicy) Admit(class byte, tenantStreams int, tenantConcurrentLimit int, activeTenants int, tenantNew bool) (bool, AdmissionReason) {
	cap, ok := ClassCap(class)
	if !ok {
		return false, AdmitUnknownCls
	}
	if tenantNew && activeTenants >= MaxActiveTenantsPerNode {
		return false, AdmitTenantFan
	}
	limit := cap
	source := AdmitClassCap
	if tenantConcurrentLimit < limit {
		limit = tenantConcurrentLimit
		source = AdmitTenantQuota
	}
	if tenantStreams+1 > limit {
		return false, source
	}
	return true, AdmitOK
}

// StarveMinutes es la metrica `pool_starved` del criterio 2: minutos con
// admision cero para un tenant con capacidad libre en el pool.
type StarveMinutes struct {
	Minutes int
	Starved int
}

// FairnessSim: tenants ocupando el pool durante `minutes` con capacidad
// por nodo `nodeCap` y `nodes` nodos; el tenant 4 entra cuando los 3
// grandes (300 sesiones cada uno) ya tienen sitio. Devuelve los minutos
// en los que el pool no pudo admitir al 4.º (pool_starved).
func FairnessSim(nodes, nodeCap, minutes int, bigTenantSessions int, bigTenants int) StarveMinutes {
	out := StarveMinutes{Minutes: minutes}
	capacity := nodes * nodeCap
	bigDemand := bigTenants * bigTenantSessions
	if capacity > bigDemand {
		return out // pool_starved = 0: el 4.º siempre tiene hueco
	}
	// con pool apretado, el 4.º starvea los minutos que faltan hueco.
	out.Starved = minutes - capacity/bigDemand
	if out.Starved < 0 {
		out.Starved = 0
	}
	return out
}
