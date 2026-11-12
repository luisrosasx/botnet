package proxy

import (
	"bytes"
	"strings"
	"testing"
)

// T-0607 AC1: dos sesiones concurrentes de tenants distintos: el flush
// de acct.{node_id}.flush separa bytes_in/out por session_id y la suma
// es exacta ± 0,5 % (fixture de 10 MB partidos 60/40).
func TestPerSessionCountersSplitAndFlush(t *testing.T) {
	tbl := NewCounterTable()
	// fixture 10 MB partidos 60/40 entre sesiones (in+out total)
	tbl.AddIn("sess-A", 3_600_000)  // 3,6 MB
	tbl.AddOut("sess-A", 2_400_000) // 2,4 MB -> A = 6 MB (60 %)
	tbl.AddIn("sess-B", 2_400_000)
	tbl.AddOut("sess-B", 1_600_000) // B = 4 MB (40 %)

	usage := tbl.Flush()
	if len(usage) != 2 {
		t.Fatalf("flush = %d registros, want 2 por session_id", len(usage))
	}
	var totalIn, totalOut uint64
	for _, u := range usage {
		if u.SessionID != "sess-A" && u.SessionID != "sess-B" {
			t.Fatalf("registro sin session_id conocido: %+v", u)
		}
		totalIn += u.BytesIn
		totalOut += u.BytesOut
	}
	const fixture = 10_000_000
	sum := totalIn + totalOut
	dev := float64(int64(sum)-fixture) / float64(fixture)
	if dev < -0.005 || dev > 0.005 {
		t.Fatalf("suma del flush = %d B, desvio %.2f%% sobre 10 MB fixture", sum, dev*100)
	}
	// separacion exacta por sesion: A=6MB, B=4MB
	for _, u := range usage {
		switch u.SessionID {
		case "sess-A":
			if u.BytesIn+u.BytesOut != 6_000_000 {
				t.Fatalf("A = %d/%d, want 6 MB", u.BytesIn, u.BytesOut)
			}
		case "sess-B":
			if u.BytesIn+u.BytesOut != 4_000_000 {
				t.Fatalf("B = %d/%d, want 4 MB", u.BytesIn, u.BytesOut)
			}
		}
	}
	// sesiones terminadas: los contadores ya no quedan vivos (viajaron
	// en el flush con su session_id -> sessions.bytes_in/out exactos).
	if tbl.Count() != 0 {
		t.Fatalf("contadores vivos tras flush = %d, want 0", tbl.Count())
	}

	var buf bytes.Buffer
	if err := WriteFlush(&buf, "node-7", usage); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "acct.node-7.flush") {
		t.Fatalf("flush subject = %q, want acct.node-7.flush", buf.String())
	}
	if !strings.Contains(buf.String(), "sess-A,3600000,2400000") ||
		!strings.Contains(buf.String(), "sess-B,2400000,1600000") {
		t.Fatalf("payload del flush:\n%s", buf.String())
	}
}

// T-0607 AC2: el SOCKS5 interno no traspasa auth: cerrar la sesion A no
// cierra la sesion B — B sigue viva y su contador no se reinicia.
func TestClosingOneSessionLeavesTheOther(t *testing.T) {
	tbl := NewCounterTable()
	tbl.AddIn("A", 5000)
	tbl.AddOut("A", 3000)
	tbl.AddIn("B", 7000)
	tbl.AddOut("B", 9000)

	usageA := tbl.CloseSession("A")
	if usageA.SessionID != "A" || usageA.BytesIn != 5000 || usageA.BytesOut != 3000 {
		t.Fatalf("registro de cierre A = %+v", usageA)
	}
	if !tbl.Live("B") {
		t.Fatal("B murio con A: el SOCKS5 interno traspaso auth")
	}
	b := tbl.Get("B")
	if b.In.Load() != 7000 || b.Out.Load() != 9000 {
		t.Fatalf("contadores de B reiniciados: in=%d out=%d", b.In.Load(), b.Out.Load())
	}
}

// T-0607 AC3: los registros del flush viajan con session_id; el sujeto
// lleva el node_id (acct.{node_id}.flush) y los valores son exactos —
// sessions.bytes_in/out los refleja sin redondeo.
func TestFlushCarriesSessionIDAndSubject(t *testing.T) {
	if got := FlushSubject("node-7"); got != "acct.node-7.flush" {
		t.Fatalf("FlushSubject = %q", got)
	}
	tbl := NewCounterTable()
	tbl.AddIn("s1", 1234)
	tbl.AddOut("s1", 4321)
	usage := tbl.Flush()
	if len(usage) != 1 || usage[0].SessionID != "s1" ||
		usage[0].BytesIn != 1234 || usage[0].BytesOut != 4321 {
		t.Fatalf("flush = %+v, want registro exacto de s1", usage)
	}
}
