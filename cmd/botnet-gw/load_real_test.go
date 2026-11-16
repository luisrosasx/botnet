package main

// Carga parametrizada del gateway para S-16 (docs/state/PROXIMOS-PASOS.md):
// P99 < 1,5 s y streams_saturated < 10 % tanto a 250 nodos/gw como a 1.000
// nodos. Es el mismo patron que TestGwLoadP99AndRedirects (select_test.go)
// pero leyendo LOAD_NODES del entorno, asi que scripts/field/load_250_1000.sh
// puede barrer los dos tramos sin tocar el test de T-0903.
//
// Dos medidas, y cada una con su alcance declarado:
//
//	A) P99 de seleccion — latencia REAL del reloj del gateway sobre el
//	   camino de control (PoolReader local + GeoIndex + pick). No incluye
//	   el alambre: la medida iperf3 en hardware real sigue BLOCKED-HW.
//	B) streams_saturated — tabla de ADMISION real (admission.go):
//	   ClassCap por clase tecnica (A 150, B 100, C 64) recortada por el
//	   concurrent_limit del tenant, con el minimo mandando (T-0905 AC3).
//	   Es saturada toda apertura que el gateway rechazo porque ese
//	   (nodo, tenant) ya estaba a tope de streams. El fan-out de 8 tenants
//	   vivos por nodo se cuenta aparte (tenant_fanout): no es falta de
//	   streams y mezclarlo inflaria la SLO.
//
// El demanda simulada lleva un sesgo de hotspot residencial: el
// LOAD_HOT_PCT del pool absorbe LOAD_HOT_MULT veces la demanda (un nodo
// CGNAT con muchos inquilinos ve mas sesiones que la media, §10).
//
// Variables del entorno (todas opcionales):
//
//	LOAD_NODES             nodos totales del fixture (defecto 1000)
//	LOAD_PER_GW            densidad objetivo por gateway (defecto 250)
//	LOAD_TIMED             selecciones cronometradas por gateway (defecto 250)
//	LOAD_TENANTS           tenants concurrentes por nodo (defecto 8, el
//	                       MaxActiveTenantsPerNode del plano)
//	LOAD_TENANT_LIMIT      streams maximos que un tenant admite en un nodo
//	                       (defecto 250: el techo de streams por conexion H2;
//	                       por encima de los ClassCap, que son los que mandan)
//	LOAD_DEMAND_PER_TENANT streams que pide cada tenant en cada nodo (defecto
//	                       50: el concurrent_limit por defecto de `quotas`)
//	LOAD_HOT_PCT           % del pool con hotspot (defecto 5)
//	LOAD_HOT_MULT          multiplicador de demanda del hotspot (defecto 3)
//	LOAD_JSON_OUT          si esta puesto, escribe el informe JSON ahi
//
// Sin LOAD_JSON_OUT el test no escribe ficheros: `go test ./cmd/botnet-gw`
// no ensucia docs/verification.

import (
	"encoding/json"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"
)

// loadRealCrossPct: trafico excepcional inter-pais cuyo pool local no cubre
// — 307 de control, 0 bytes de sesion copiados.
const loadRealCrossPct = 5

func loadRealEnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q: quiero un entero > 0", key, raw)
	}
	return n
}

// buildGwLoadReal levanta el fixture de N nodos repartidos en
// ceil(N/perGW) gateways. Los paises alternan ES/PT para que el 307 exista
// en cuanto hay mas de un gateway.
func buildGwLoadReal(nodes, perGW, seed int) *gwLoadFixture {
	gws := (nodes + perGW - 1) / perGW
	base := nodes / gws
	extra := nodes % gws

	f := &gwLoadFixture{
		readers: map[string]*watchReader{},
		index:   NewGeoIndex(),
		select_: map[string]*Selector{},
	}
	n := 0
	for g := 0; g < gws; g++ {
		size := base
		if g < extra {
			size++
		}
		id, iso2 := "gw-es-mad", "ES"
		if g%2 == 1 {
			id, iso2 = "gw-pt-lis", "PT"
		}
		id += itoa(g + 1)
		addr := "gw" + itoa(g+1) + ".botnet.example:443"
		f.index.Add(id, iso2, addr)

		r := &watchReader{pools: map[string][]string{}, caps: map[string]NodeCaps{}, Keys: map[string]int{}}
		key := PoolKey(id)
		for i := 0; i < size; i++ {
			nodeID := itoa(n)
			r.pools[key] = append(r.pools[key], nodeID)
			r.caps[nodeID] = NodeCaps{
				ID: nodeID, BWMbps: uint16(2 + n%48), ISO2: iso2,
				DeviceClass: 'A' + byte(n%3),
			}
			n++
		}
		f.readers[id] = r
		f.select_[id] = &Selector{GWID: id, SelfAddr: addr, Reader: r,
			Index: f.index, Rnd: rand.New(rand.NewSource(int64(seed + g)))}
	}
	return f
}

// TestGwLoadReal: P99 de seleccion y streams_saturated para LOAD_NODES.
// Escribe el informe ANTES de asertar los umbrales, para que el script de
// campo lea los numeros crudos incluso cuando la puerta sale en rojo.
func TestGwLoadReal(t *testing.T) {
	nodes := loadRealEnvInt(t, "LOAD_NODES", 1000)
	perGW := loadRealEnvInt(t, "LOAD_PER_GW", 250)
	timed := loadRealEnvInt(t, "LOAD_TIMED", 250)
	tenants := loadRealEnvInt(t, "LOAD_TENANTS", MaxActiveTenantsPerNode)
	tenantLimit := loadRealEnvInt(t, "LOAD_TENANT_LIMIT", 250)
	demand := loadRealEnvInt(t, "LOAD_DEMAND_PER_TENANT", 50)
	hotPct := loadRealEnvInt(t, "LOAD_HOT_PCT", 5)
	hotMult := loadRealEnvInt(t, "LOAD_HOT_MULT", 3)
	const seed = 20260816

	f := buildGwLoadReal(nodes, perGW, seed)
	adm := NewAdmissionPolicy()

	var lat []time.Duration
	selections, redirects := 0, 0

	// A) latencia real del plano de control: LOAD_TIMED selecciones por
	// gateway, con su trafico inter-pais excepcional (307 de control).
	for gwID := range f.readers {
		iso, other := "ES", "PT"
		if gwID[3:5] == "pt" {
			iso, other = "PT", "ES"
		}
		crossOK := len(f.index.owners[other]) > 0
		for i := 0; i < timed; i++ {
			want := iso
			if crossOK && i%100 < loadRealCrossPct {
				want = other
			}
			res, err := f.select_[gwID].Select(SelectReq{ISO2: want})
			if err != nil {
				t.Fatalf("%s seleccion %d: %v", gwID, i, err)
			}
			lat = append(lat, res.Elapsed)
			selections++
			if res.HasRedirect {
				if res.Redirect.Code != 307 || res.Redirect.Location == "" {
					t.Fatalf("%s: 307 mal formado %+v", gwID, res.Redirect)
				}
				redirects++
				continue
			}
			if res.Node.ID == "" {
				t.Fatalf("%s: seleccion sin nodo", gwID)
			}
		}
	}
	p99 := p99Of(lat)

	// B) saturacion de streams: cada nodo del pool recibe la demanda de sus
	// tenants y la admission real decide. El hotPct del pool paga hotMult.
	opens, saturated, fanout := 0, 0, 0
	satReasons := map[AdmissionReason]int{}
	tenantLive := make([]int, tenants)
	for gwID, r := range f.readers {
		key := PoolKey(gwID)
		for pos, nodeID := range r.pools[key] {
			caps, ok := r.Caps(nodeID)
			if !ok {
				t.Fatalf("%s: sin caps del nodo %s", gwID, nodeID)
			}
			want := demand
			if pos%100 < hotPct {
				want *= hotMult
			}
			for i := 0; i < tenants; i++ {
				tenantLive[i] = 0
			}
			active := 0
			for i := 0; i < want*tenants; i++ {
				tenant := i % tenants
				opens++
				ok, reason := adm.Admit(caps.DeviceClass, tenantLive[tenant],
					tenantLimit, active, tenantLive[tenant] == 0)
				if !ok {
					if reason == AdmitTenantFan {
						fanout++
					} else {
						saturated++
						satReasons[reason]++
					}
					continue
				}
				if tenantLive[tenant] == 0 {
					active++
				}
				tenantLive[tenant]++
			}
		}
	}
	satPct := 0.0
	if opens > 0 {
		satPct = 100 * float64(saturated) / float64(opens)
	}
	redPct := 0.0
	if selections > 0 {
		redPct = 100 * float64(redirects) / float64(selections)
	}
	poolReads := 0
	for _, r := range f.readers {
		for _, k := range r.Keys {
			poolReads += k
		}
	}

	report := map[string]any{
		"nodes": nodes, "gateways": len(f.readers),
		"nodes_per_gw":            nodes / len(f.readers),
		"tenants_per_node":        tenants,
		"tenant_concurrent_limit": tenantLimit,
		"demand_per_tenant":       demand,
		"hot_pct":                 hotPct,
		"hot_mult":                hotMult,
		"selections":              selections,
		"pool_reads":              poolReads,
		"p99_ms":                  round2(p99),
		"threshold_ms":            1500,
		"opens":                   opens,
		"streams_saturated":       saturated,
		"streams_saturated_pct":   round2(satPct),
		"threshold_streams_pct":   10,
		"tenant_fanout":           fanout,
		"redirects_pct":           round2(redPct),
		"copied_bytes":            0,
		"admission_table":         AdmissionTableVersion,
		"latency_scope":           "plano de control del gateway (PoolReader+GeoIndex+pick); el alambre iperf3 es BLOCKED-HW",
		"generated_by":            "cmd/botnet-gw/load_real_test.go TestGwLoadReal",
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if out := os.Getenv("LOAD_JSON_OUT"); out != "" {
		if err := os.WriteFile(out, b, 0o644); err != nil {
			t.Fatalf("informe JSON %s: %v", out, err)
		}
	}
	t.Logf("gw-load-real: %s", b)

	if p99 >= 1500 {
		t.Errorf("P99 de seleccion: %.2f ms (limite 1.500) con %d nodos", p99, nodes)
	}
	if !(satPct < 10) {
		t.Errorf("streams_saturated %.2f %% (limite 10) con %d nodos: %v", satPct, nodes, satReasons)
	}
}
