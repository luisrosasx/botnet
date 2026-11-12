package intrusion

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDestPolicy(t *testing.T) {
	cases := []struct {
		class   string
		freeKB  int64
		wantDir string
		wantErr bool
	}{
		{"A", 20000, "/data/botd", false},
		{"B", 20000, "/data/botd", false},
		{"C", 6000, "/tmp", false},     // clase C declarada -> tmpfs-only
		{"A", 6000, "/tmp", false},     // overlay < 8 MB obliga /tmp (T-0206 criterio 1)
		{"A", -1, "/data/botd", false}, // overlay sin medir: clase manda
		{"C", -1, "/tmp", false},       // clase C manda sin medir
		{"C", 5000, "/tmp", false},     // libre ≥ 4 MB: copia autorizada
		{"A", 3000, "", true},          // tope de flash: < 4 MB
		{"C", 1000, "", true},          // tope de flash
	}
	for _, c := range cases {
		dir, _, err := DestPolicy(c.class, c.freeKB)
		if c.wantErr {
			if err != ErrFlashCap {
				t.Errorf("class=%s free=%d: quiero ErrFlashCap, tengo %v", c.class, c.freeKB, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("class=%s free=%d: err %v", c.class, c.freeKB, err)
			continue
		}
		if dir != c.wantDir {
			t.Errorf("class=%s free=%d: dir %q, quiero %q", c.class, c.freeKB, dir, c.wantDir)
		}
	}
}

func TestRetryDelayS(t *testing.T) {
	rnd := rand.New(rand.NewPCG(7, 7))
	for attempt := int64(1); attempt <= 3; attempt++ {
		for i := 0; i < 200; i++ {
			d := RetryDelayS(2, attempt, 10, rnd)
			lo, hi := 2*attempt, 2*attempt+10
			if d < lo || d > hi {
				t.Fatalf("attempt=%d delay=%d fuera de [%d,%d]", attempt, d, lo, hi)
			}
		}
	}
	// El jitter debe dispersar: 200 draws no pueden dar un único valor
	// (eso es justo lo que mide el criterio 3 de T-0205).
	seen := map[int64]bool{}
	for i := 0; i < 200; i++ {
		seen[RetryDelayS(2, 1, 10, rnd)] = true
	}
	if len(seen) < 5 {
		t.Fatalf("jitter poco disperso: %d valores distintos", len(seen))
	}
}

func TestSpoolCap24h(t *testing.T) {
	// T-0206 criterio 3: 24 h simuladas con flush por minuto (1440 entradas
	// de ~44 B) y tope duro de 64 KB en /tmp.
	dir := filepath.Join(t.TempDir(), "botd-spool")
	s := &Spool{Dir: dir, MaxKB: SpoolMaxKB}
	var b strings.Builder
	for i := range 1440 {
		b.Reset()
		fmt.Fprintf(&b, "t=%04d flush_id=%04d in=8192 out=4096 seq=%04d", i, i, i) // ~44 B
		if err := s.Append([]string{b.String()}); err != nil {
			t.Fatal(err)
		}
	}
	used, err := s.UsedKB()
	if err != nil {
		t.Fatal(err)
	}
	if used > SpoolMaxKB {
		t.Fatalf("du -k /tmp/botd-spool = %d KB > %d KB tras 24 h simulados", used, SpoolMaxKB)
	}
	last, err := os.ReadFile(filepath.Join(dir, "spool.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(last), "flush_id=1439") {
		t.Error("el recorte debe conservar la cola (última entrada presente)")
	}
	if strings.Contains(string(last), "flush_id=0000 ") {
		t.Error("el recorte debe expulsar la cabecera (entrada más vieja fuera)")
	}
}
