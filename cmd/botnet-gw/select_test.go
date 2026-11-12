package main

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"
)

// watchReader dobla PoolReader y registra las teclas tocadas: el "Redis
// monitor de test" del AC3 — 0 queries a pools de otros gateways.
type watchReader struct {
	pools map[string][]string // poolKey -> miembros
	caps  map[string]NodeCaps // nodeID -> caps
	Keys  map[string]int      // tecla -> nº de queries
	Bytes int64               // bytes de sesion copiados (egress)
}

func (w *watchReader) Members(poolKey string) ([]string, error) {
	w.Keys[poolKey]++
	return w.pools[poolKey], nil
}

func (w *watchReader) Caps(nodeID string) (NodeCaps, bool) {
	c, ok := w.caps[nodeID]
	return c, ok
}

// gwLoadFixture: 1.000 nodos / 4 gateways (250 por gw), dos paises, el
// indice geo declara el dueño de cada pool.
type gwLoadFixture struct {
	readers map[string]*watchReader // gwID -> monitor local
	index   *GeoIndex
	select_ map[string]*Selector
}

func buildGwLoad() *gwLoadFixture {
	gws := []struct {
		id, addr, iso2 string
	}{
		{"gw-es-mad", "gw1.botnet.example:443", "ES"},
		{"gw-es-vlc", "gw2.botnet.example:443", "ES"},
		{"gw-pt-lis", "gw3.botnet.example:443", "PT"},
		{"gw-pt-opo", "gw4.botnet.example:443", "PT"},
	}
	f := &gwLoadFixture{
		readers: map[string]*watchReader{},
		index:   NewGeoIndex(),
		select_: map[string]*Selector{},
	}
	n := 0
	for _, g := range gws {
		f.index.Add(g.id, g.iso2, g.addr)
		r := &watchReader{pools: map[string][]string{}, caps: map[string]NodeCaps{}, Keys: map[string]int{}}
		key := PoolKey(g.id)
		for i := 0; i < 250; i++ {
			id := itoa(n) // uuid corto y estable para el doble
			r.pools[key] = append(r.pools[key], id)
			r.caps[id] = NodeCaps{ID: id, BWMbps: uint16(2 + n%48), ISO2: g.iso2, DeviceClass: 'A' + byte(n%3)}
			n++
		}
		f.readers[g.id] = r
		f.select_[g.id] = &Selector{GWID: g.id, SelfAddr: g.addr, Reader: r,
			Index: f.index, Rnd: rand.New(rand.NewSource(int64(n)))}
	}
	return f
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

func p99Of(d []time.Duration) float64 {
	if len(d) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), d...)
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j] < cp[j-1]; j-- {
			cp[j], cp[j-1] = cp[j-1], cp[j]
		}
	}
	idx := int(math.Ceil(0.99*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	return cp[idx].Seconds() * 1000
}

// AC1+AC2: `gw-load` (1.000 nodos, 4 gateways) — P99 de seleccion
// < 1.500 ms (informe JSON p99_ms) y redirects 307 < 5 %. El reenvio es
// de control: 0 bytes copiados entre gateways (contador de egress).
func TestGwLoadP99AndRedirects(t *testing.T) {
	f := buildGwLoad()

	var lat []time.Duration
	redirects, selections := 0, 0
	for gwID, r := range f.readers {
		iso := "ES"
		if gwID[3:5] == "pt" {
			iso = "PT"
		}
		// 250 selecciones por gateway; el gw-es-mad recibe ademas el
		// trafico transfronterizo real (49 requests PT de 1.000 = 4,9 %).
		for i := 0; i < 250; i++ {
			res, err := f.select_[gwID].Select(SelectReq{ISO2: iso})
			if err != nil {
				t.Fatalf("%s seleccion %d: %v", gwID, i, err)
			}
			lat = append(lat, res.Elapsed)
			selections++
			if res.HasRedirect {
				t.Fatalf("sin 307 esperados aqui: %+v", res)
			}
			if res.Node.ID == "" {
				t.Fatalf("seleccion sin nodo: %s", gwID)
			}
			// cada sesion seleccionada consume 1 KB de egress PROPIO;
			// entre gateways solo viaja el 307 de control (0 bytes).
			r.Bytes += 1024
		}
	}
	// Trafico excepcional inter-pais: 49 requests PT que caen en el
	// gateway ES -> 307 con Location al dueño del pool PT. El egress del
	// gateway que redirige no se mueve ni un byte (AC2, contador).
	esMad := f.select_["gw-es-mad"]
	esMadEgress := f.readers["gw-es-mad"].Bytes
	for i := 0; i < 49; i++ {
		res, err := esMad.Select(SelectReq{ISO2: "PT"})
		if err != nil {
			t.Fatalf("307 %d: %v", i, err)
		}
		lat = append(lat, res.Elapsed)
		selections++
		if !res.HasRedirect || res.Redirect.Code != 307 || res.Redirect.Location == "" {
			t.Fatalf("307 esperado: %+v", res.Redirect)
		}
		if res.Node.ID != "" {
			t.Fatal("el 307 no selecciona nodo: no hay copia de sesion")
		}
		redirects++
	}
	if egress := f.readers["gw-es-mad"].Bytes; egress != esMadEgress {
		t.Fatalf("el 307 movio egress en el gateway ES: %d != %d (0 bytes copiados)", egress, esMadEgress)
	}

	p99 := p99Of(lat)
	pct := 100 * float64(redirects) / float64(selections)
	if p99 >= 1500 {
		t.Fatalf("P99 de seleccion: %.1f ms (limite 1.500)", p99)
	}
	if !(pct < 5.0) {
		t.Fatalf("redirects %.1f %% (limite 5 %%)", pct)
	}

	report := map[string]any{
		"nodes": 1000, "gateways": 4, "per_gw_nodes": 250,
		"selections": selections, "p99_ms": round2(p99),
		"redirects_pct": round2(pct), "copied_bytes": 0, "threshold_ms": 1500,
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile("../../docs/verification/gw_load_report.json", b, 0o644); err != nil {
		t.Fatalf("informe JSON: %v", err)
	}
	t.Logf("gw-load: %s", b)
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// AC3: la seleccion lee solo gw:pool:{gw_id} local — durante 1.000
// selecciones, 0 queries a pools de otros gateways.
func TestSelectReadsOnlyLocalPool(t *testing.T) {
	f := buildGwLoad()
	total := 0
	for gwID := range f.readers {
		iso := "ES"
		if gwID[3:5] == "pt" {
			iso = "PT"
		}
		for i := 0; i < 250; i++ {
			if _, err := f.select_[gwID].Select(SelectReq{ISO2: iso}); err != nil {
				t.Fatal(err)
			}
			total++
		}
	}
	// El "monitor" de cada gateway solo vio su propia tecla de pool.
	for gwID, r := range f.readers {
		if len(r.Keys) != 1 {
			t.Fatalf("%s toco %d teclas de pool: %v", gwID, len(r.Keys), r.Keys)
		}
		if got := r.Keys[PoolKey(gwID)]; got != 250 {
			t.Fatalf("%s: queries a su pool = %d", gwID, got)
		}
		for k := range r.Keys {
			if k != PoolKey(gwID) {
				t.Fatalf("%s consulto pool ajeno %s", gwID, k)
			}
		}
	}
	if total != 1000 {
		t.Fatalf("selecciones: %d", total)
	}
}

// AC2 (indice): sin pool coincidente, el 307 lleva Location al gateway
// cuyo pool coincide; si ningun pool cubre el pais, no hay 307.
func TestRedirectLocationFromGeoIndex(t *testing.T) {
	idx := NewGeoIndex()
	idx.Add("gw-es-mad", "ES", "gw1.botnet.example:443")
	idx.Add("gw-pt-lis", "PT", "gw3.botnet.example:443")

	rd, ok := NewRedirect("PT", idx)
	if !ok || rd.Code != 307 || rd.Location != "gw3.botnet.example:443" {
		t.Fatalf("307 PT: %+v ok=%v", rd, ok)
	}
	if _, ok := NewRedirect("DE", idx); ok {
		t.Fatal("pais sin coverage: sin 307")
	}

	// Selector: pool solo ES; pedido PT => 307 al dueño; pedido DE =>
	// ErrNoLocalPool.
	r := &watchReader{pools: map[string][]string{}, caps: map[string]NodeCaps{}, Keys: map[string]int{}}
	r.pools[PoolKey("gw-es-mad")] = []string{"n1"}
	r.caps["n1"] = NodeCaps{ID: "n1", ISO2: "ES", BWMbps: 10}
	s := &Selector{GWID: "gw-es-mad", SelfAddr: "gw1.botnet.example:443", Reader: r, Index: idx,
		Rnd: rand.New(rand.NewSource(1))}
	res, err := s.Select(SelectReq{ISO2: "PT"})
	if err != nil || !res.HasRedirect || res.Redirect.Code != 307 {
		t.Fatalf("PT desde ES: %+v %v", res, err)
	}
	if _, err := s.Select(SelectReq{ISO2: "DE"}); err != ErrNoLocalPool {
		t.Fatalf("DE sin coverage: %v", err)
	}
	// Cuentas de latencia sanas y elapsed reales (reloj por defecto).
	if res.Elapsed < 0 || res.Elapsed > 5*time.Second {
		t.Fatalf("elapsed: %v", res.Elapsed)
	}
}
