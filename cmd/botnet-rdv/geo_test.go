package main

import (
	"strings"
	"testing"
)

// Fixture de la epica (T-0902, cierra A2-01): 4 gateways — dos por pais,
// uno por ciudad capital — y 1.000 nodos ES/PT con ciudad declarada.
func fixtureGateways() []GatewayLoc {
	return []GatewayLoc{
		{ID: "gw-es-mad", Addr: "gw-es-mad.botnet.example:443", Loc: GeoLoc{ISO2: "ES", City: "Madrid"}},
		{ID: "gw-es-vlc", Addr: "gw-es-vlc.botnet.example:443", Loc: GeoLoc{ISO2: "ES", City: "Valencia"}},
		{ID: "gw-pt-lis", Addr: "gw-pt-lis.botnet.example:443", Loc: GeoLoc{ISO2: "PT", City: "Lisboa"}},
		{ID: "gw-pt-opo", Addr: "gw-pt-opo.botnet.example:443", Loc: GeoLoc{ISO2: "PT", City: "Porto"}},
	}
}

func fixtureMatcher() *GeoMatcher {
	m := NewGeoMatcher(fixtureGateways()...)
	// Distancias inter-city del inventario (orden de magnitud real).
	m.AddDistance("Madrid", "Valencia", 350)
	m.AddDistance("Barcelona", "Valencia", 350)
	m.AddDistance("Madrid", "Barcelona", 500)
	m.AddDistance("Sevilla", "Madrid", 530)
	m.AddDistance("Sevilla", "Valencia", 500)
	m.AddDistance("Lisboa", "Porto", 310)
	m.AddDistance("Braga", "Porto", 20)
	m.AddDistance("Braga", "Lisboa", 340)
	m.AddDistance("Faro", "Lisboa", 280)
	m.AddDistance("Faro", "Porto", 450)
	return m
}

type fixtureNode struct {
	Loc  GeoLoc
	Name string
}

// 1.000 nodos deterministas: iso2 ES/PT alternado por LCG y ciudad de un
// catalogo por pais — la mitad exacta en la ciudad de un gateway.
func fixtureNodes() []fixtureNode {
	esCities := []string{"Madrid", "Valencia", "Barcelona", "Sevilla"}
	ptCities := []string{"Lisboa", "Porto", "Braga", "Faro"}
	out := make([]fixtureNode, 0, 1000)
	x := uint32(20261114)
	for i := 0; i < 1000; i++ {
		x = x*1664525 + 1013904223
		es := (i>>1)%2 == 0
		cities := esCities
		iso := "ES"
		if !es {
			cities = ptCities
			iso = "PT"
		}
		city := cities[int(x>>16)%len(cities)]
		out = append(out, fixtureNode{Loc: GeoLoc{ISO2: iso, City: city}, Name: itoa(i)})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// AC1+AC2: el 100 % de los nodos casa con el gateway de su pais; cuando
// pais+ciudad coinciden, ciudad manda. Madrid↔Lisboa mal casado = fallo.
func TestGeoMatcherCountryAndCityPriority(t *testing.T) {
	m := fixtureMatcher()
	nodes := fixtureNodes()
	if len(nodes) != 1000 {
		t.Fatalf("fixture: %d nodos", len(nodes))
	}
	byID := map[string]GatewayLoc{}
	for _, gw := range fixtureGateways() {
		byID[gw.ID] = gw
	}
	crossBorder, cityHits, cityMisses := 0, 0, 0
	for _, n := range nodes {
		gw, ok := m.Pick(n.Loc)
		if !ok {
			t.Fatalf("nodo %v sin gateway en su pais", n.Loc)
		}
		if gw.Loc.ISO2 != n.Loc.ISO2 {
			crossBorder++ // Madrid↔Lisboa: transfrontera = fallo (AC2: 0)
		}
		if !strings.EqualFold(n.Loc.City, gw.Loc.City) {
			cityMisses++
			continue
		}
		cityHits++
		if !strings.EqualFold(n.Loc.City, byID[gw.ID].Loc.City) {
			t.Fatalf("ciudad manda: nodo %v casado con %s", n.Loc, gw.ID)
		}
	}
	if crossBorder != 0 {
		t.Fatalf("asignaciones transfronterizas: %d (0 exigido, A2-01)", crossBorder)
	}
	if cityHits < 400 {
		t.Fatalf("la mitad de la fixture vive en ciudad de gateway: hits=%d", cityHits)
	}
	t.Logf("1.000 nodos: 0 transfronterizas, city hits=%d misses=%d", cityHits, cityMisses)
}

// AC1 (asercion de asignacion): un nodo en la ciudad de un gateway casa
// con ese gateway, no con el vecino de pais — Madrid gana a Valencia.
func TestCityBeatsCountryNeighbour(t *testing.T) {
	m := fixtureMatcher()
	for _, c := range []struct{ city, want string }{
		{"Madrid", "gw-es-mad"}, {"Valencia", "gw-es-vlc"},
		{"Lisboa", "gw-pt-lis"}, {"Porto", "gw-pt-opo"},
	} {
		gw, ok := m.Pick(GeoLoc{ISO2: isoOf(c.want), City: c.city})
		if !ok || gw.ID != c.want {
			t.Fatalf("%s -> %s (queremos %s)", c.city, gw.ID, c.want)
		}
	}
}

func isoOf(gwID string) string {
	if strings.HasPrefix(gwID, "gw-es") {
		return "ES"
	}
	return "PT"
}

// AC3: muerto el gateway asignado, el rdv re-asigna al siguiente gateway
// mas cercano del mismo pais — y lo hace dentro del presupuesto de 5 min
// sin reentregar el binario (herencia A1-10).
func TestFailoverNextClosestSameCountry(t *testing.T) {
	m := fixtureMatcher()
	dead := []string{"gw-es-mad"}
	for _, c := range []struct {
		city string
		want string
	}{
		{"Madrid", "gw-es-vlc"},    // siguiente mas cercano del mismo pais
		{"Barcelona", "gw-es-vlc"}, /* 350 km < 500 km a Madrid (muerto) */
	} {
		gw, ok := m.Failover(GeoLoc{ISO2: "ES", City: c.city}, dead)
		if !ok || gw.ID != c.want {
			t.Fatalf("failover %s: got %s want %s", c.city, gw.ID, c.want)
		}
	}
	// Lisboa muerto: un nodo de Lisboa cae a Porto, jamas a un ES.
	gw, ok := m.Failover(GeoLoc{ISO2: "PT", City: "Lisboa"}, []string{"gw-pt-lis"})
	if !ok || gw.Loc.ISO2 != "PT" || gw.ID != "gw-pt-opo" {
		t.Fatalf("failover PT: %+v ok=%v", gw, ok)
	}
	// Presupuesto < 5 min y sin reentrega: reasignacion es un assign rdv.
	if !FailoverWithinBudget() {
		t.Fatalf("presupuesto de reasignacion fuera de los 5 min: %d s", HandshakeFailoverBudgetS)
	}
	// Sin gateways vivos del pais: ok=false (el gateway llamante resuelve
	// con 307/vecino, T-0903), nunca una asignacion transfronteriza muda.
	_, ok = m.Failover(GeoLoc{ISO2: "ES", City: "Madrid"}, []string{"gw-es-mad", "gw-es-vlc"})
	if ok {
		t.Fatal("sin pool ES no debe casar en ES")
	}
}
