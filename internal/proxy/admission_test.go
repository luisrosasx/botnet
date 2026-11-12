package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// T-0606 AC1: con 9 tenants concurrentes sobre un nodo, los 8 primeros
// admitidos y el 9.º rechazado en admision (tope desacoplado de los 150
// streams tecnicos).
func TestEightTenantsAdmittedNinthRejected(t *testing.T) {
	a := NewAdmission(0)
	for i := 1; i <= 8; i++ {
		dec := a.Admit(tenantName(i), 20, i-1)
		if !dec.Admitted {
			t.Fatalf("tenant %d rechazado: %s", i, dec.Reason)
		}
	}
	dec := a.Admit(tenantName(9), 20, 8)
	if dec.Admitted {
		t.Fatal("el 9.º tenant fue admitido con 8 activos")
	}
	if dec.Reason != "tenant_cap" {
		t.Fatalf("motivo = %q, want tenant_cap", dec.Reason)
	}
	if a.TenantCount() != 8 {
		t.Fatalf("botnet_tenant_count = %d, want 8", a.TenantCount())
	}
	// un tenant vivo puede seguir creciendo dentro de su cuota
	if dec := a.Admit(tenantName(3), 20, 8); !dec.Admitted {
		t.Fatalf("tenant existente rechazado: %s", dec.Reason)
	}
}

// T-0606 AC2: admision = min(150 streams nominales, cuota concurrente,
// 8 tenants). Fixture con tenant de cuota 300 en nodo con 8 tenants: el
// nodo no admite el 9.º aunque queden ~90 streams nominales (el min
// manda).
func TestMinRuleTenantCapBeatsStreamSlack(t *testing.T) {
	a := NewAdmission(150)
	for i := 1; i <= 8; i++ {
		if dec := a.Admit(tenantName(i), 300, i-1); !dec.Admitted {
			t.Fatalf("siembra tenant %d: %s", i, dec.Reason)
		}
	}
	// quedan 142 streams nominales (150-8): aun asi el 9.º no entra.
	dec := a.Admit("tenant-9", 300, 8)
	if dec.Admitted || dec.Reason != "tenant_cap" {
		t.Fatalf("9.º con 142 streams libres: %+v, want rechazo tenant_cap", dec)
	}
	// la cuota concurrente tambien manda por su cuenta: tenant con
	// cuota 2 al 3.º lo rechaza su cuota, no el cap de tenants.
	b := NewAdmission(150)
	for i := 1; i <= 2; i++ {
		if dec := b.Admit("solo", 2, i-1); !dec.Admitted {
			t.Fatalf("cuota 2 sesion %d: %s", i, dec.Reason)
		}
	}
	if dec := b.Admit("solo", 2, 2); dec.Admitted || dec.Reason != "tenant_quota" {
		t.Fatalf("cuota concurrente no goberno: %+v", dec)
	}
	// y el tope tecnico: 150 streams abiertos cierran la puerta
	if dec := b.Admit("nuevo", 20, 150); dec.Admitted || dec.Reason != "stream_cap" {
		t.Fatalf("tope tecnico no goberno: %+v", dec)
	}
}

// T-0606 AC3: la metrica botnet_tenant_count se publica y se ve en
// /metrics (densidad medida del destino, no inferida).
func TestTenantCountVisibleInMetrics(t *testing.T) {
	a := NewAdmission(0)
	for i := 1; i <= 8; i++ {
		a.Admit(tenantName(i), 20, i-1)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", a.MetricsHandler())
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics = %d", resp.StatusCode)
	}
	want := "botnet_tenant_count 8"
	if !strings.Contains(string(body), want) {
		t.Fatalf("/metrics = %q, want contendr %q", body, want)
	}
	if !strings.Contains(string(body), "# TYPE botnet_tenant_count gauge") {
		t.Fatalf("/metrics sin TYPE gauge: %q", body)
	}
}

func tenantName(i int) string {
	return "tenant-" + itoa(100+i)
}
