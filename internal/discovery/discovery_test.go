package discovery

import "testing"

func TestValidate(t *testing.T) {
	ok := Candidate{IP: "1.2.3.4", Port: 80, Country: "cn"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid candidate rejected: %v", err)
	}
	for _, bad := range []Candidate{
		{IP: "", Port: 80},
		{IP: "1.2.3.4", Port: 0},
		{IP: "1.2.3.4", Port: 70000},
		{IP: "1.2.3.4", Port: 80, Country: "USA"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

// T-0103.3: los cinco puertos del sweep nominal infieren servicio.
func TestInferServiceSweepPorts(t *testing.T) {
	want := map[int]string{23: "telnet", 80: "http", 8080: "http-proxy", 554: "rtsp", 32210: "redstorm"}
	for port, svc := range want {
		if got, ok := InferService(port); !ok || got != svc {
			t.Errorf("InferService(%d) = %q,%v want %q", port, got, ok, svc)
		}
	}
	if _, ok := InferService(41234); ok {
		t.Error("unknown port should not infer")
	}
}

// SweepRate mantiene el tope de caudal del runbook: <=10 000 pps de
// base (<=5 Mbps con SYN de 44 B) y nunca baja de 1 500 pps.
func TestSweepRate(t *testing.T) {
	for _, cores := range []int{1, 2, 4, 8, 16, 0, -3} {
		r := SweepRate(cores)
		if r > 10_000 || r < 1_500 {
			t.Errorf("SweepRate(%d)=%d outside [1500,10000]", cores, r)
		}
	}
}
