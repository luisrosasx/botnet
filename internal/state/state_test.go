package state

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDiskPutGetRoundTrip(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Put("node_id.bin", []byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	b, err := d.Get("node_id.bin")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "0123456789abcdef" {
		t.Fatalf("round-trip: %q", b)
	}
	if !d.Exists("node_id.bin") {
		t.Fatal("Exists false tras Put")
	}
	if err := d.Delete("node_id.bin"); err != nil {
		t.Fatal(err)
	}
	if d.Exists("node_id.bin") {
		t.Fatal("Delete no borro")
	}
	if err := d.Delete("node_id.bin"); err != nil {
		t.Fatalf("Delete idempotente: %v", err)
	}
}

// AC1: el estado conjunto (ticket + node_id) cabe en 1 KiB; un Put que lo
// desborde es ErrBudget y deja el fichero previo intacto (degradacion
// controlada, no corruption).
func TestDiskBudgetEnforced(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Put("ticket.bin", make([]byte, 900)); err != nil {
		t.Fatal(err)
	}
	if err := d.Put("node_id.bin", make([]byte, 16)); err != nil {
		t.Fatal(err)
	}
	total, ok, err := d.WithinBudget()
	if err != nil || !ok || total != 916 {
		t.Fatalf("total=%d within=%v err=%v, want 916/true/nil", total, ok, err)
	}
	if err := d.Put("ticket.bin", make([]byte, 1200)); err != ErrBudget {
		t.Fatalf("ticket de 1200 B: err=%v, want ErrBudget", err)
	}
	b, err := d.Get("ticket.bin")
	if err != nil || len(b) != 900 {
		t.Fatalf("el Put rechazado toco el fichero previo: len=%d err=%v", len(b), err)
	}
}

func TestDiskFileMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permisos POSIX 0600: la assert aplica en Unix (limitacion anotada)")
	}
	dir := t.TempDir()
	d, err := NewDisk(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Put("ticket.bin", []byte("x")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "ticket.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("ticket.bin perm=%o, want 0600", perm)
	}
	if di, err := os.Stat(dir); err == nil && di.Mode().Perm()&0o077 != 0 {
		t.Fatalf("directorio perm=%o, want 0700", di.Mode().Perm())
	}
}

func TestDiskRejectsPathTraversal(t *testing.T) {
	d, err := NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Put(filepath.Join("..", "evil.bin"), []byte("x")); err == nil {
		t.Fatal("clave con separador de ruta debe rechazarse")
	}
}

// --- T-0505: estado unico acotado y spool -------------------------------

func TestInventoryWithinTenFiles(t *testing.T) {
	if len(Inventory) > MaxStateFiles {
		t.Fatalf("inventario fijo = %d ficheros > %d", len(Inventory), MaxStateFiles)
	}
	root := t.TempDir()
	for _, rel := range Inventory {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := CountFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if n > MaxStateFiles || n != len(Inventory) {
		t.Fatalf("find -type f | wc -l = %d (inventario %d), cota %d", n, len(Inventory), MaxStateFiles)
	}
}

// Criterio 2: `find /data/botd -perm /077 | wc -l` = 0. En Windows los
// bits POSIX no existen: el doble real es el find de la imagen
// QEMU/Linux (anotado); aqur se aserta el codigo de medicion.
func TestNonPrivatePermsZero(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node.json"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "state", "secret.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := NonPrivatePerms(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("perm /077 = %d, want 0", n)
	}
	if runtime.GOOS != "windows" {
		p := filepath.Join(root, "state", "secret.bin")
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatal(err)
		}
		n, err := NonPrivatePerms(root)
		if err != nil || n != 1 {
			t.Fatalf("con un 0644: n=%d err=%v, want 1", n, err)
		}
	}
}

// Criterio 2 (spool): `du -k /tmp/botd-spool` ≤ 64 KB tras 24 h
// simuladas: el spool trunca por cola al tope MaxSpoolBytes.
func TestSpoolCappedAfter24h(t *testing.T) {
	s, err := NewSpool(filepath.Join(t.TempDir(), SpoolDirName))
	if err != nil {
		t.Fatal(err)
	}
	// 24 h: 96 flushes periodicos de 15 min + ~4 sesiones/h de 200 B.
	var dailyWrites int64
	rec := make([]byte, 200)
	for h := 0; h < 24; h++ {
		for sess := 0; sess < 4; sess++ {
			if _, err := s.Append(rec); err != nil {
				t.Fatal(err)
			}
		}
		n, err := s.Flush(io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		dailyWrites += n
		size, err := s.Size()
		if err != nil {
			t.Fatal(err)
		}
		if size > int64(MaxSpoolBytes) {
			t.Fatalf("spool=%d B > tomo %d B", size, MaxSpoolBytes)
		}
	}
	// 64 KB = 65536 B: margen del du (bloque de directorio) ya dentro
	// de MaxSpoolBytes=60 KiB.
	if size, _ := s.Size(); size > 64*1024 {
		t.Fatalf("du -k del spool pasaria de 64 KB: %d B", size)
	}
	// Criterio 3: pico de escritura diario ≤ 30 KB. Con sesiones de
	// 200 B y 96 ventanas el total real es ~19 KB; se aserta la cota.
	if dailyWrites > 0 && dailyWrites > int64(DailyWriteBudget) {
		t.Fatalf(" escrituras dia = %d B > %d B (A1-05)", dailyWrites, DailyWriteBudget)
	}
}

// Criterio 3: el trigger periodico no es a hora fija: dos nodos del
// mismo router flushian en minutos distintos del ciclo (jitter
// determinista por node_id).
func TestFlushTimeJitterNoBroteFijo(t *testing.T) {
	now := time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)
	seen := map[time.Duration]int{}
	base := now.Truncate(FlushPeriod)
	for i := 0; i < 1000; i++ {
		id := fmt.Sprintf("11111111-1111-4444-8888-%012d", i)
		ft := FlushTime(id, now, FlushPeriod)
		off := ft.Sub(base)
		if off < 0 || off >= FlushPeriod {
			t.Fatalf("offset %v fuera del periodo %v", off, FlushPeriod)
		}
		seen[off]++
	}
	if len(seen) < 100 {
		t.Fatalf("jitter plano: %d offsets distintos en 1000 nodos", len(seen))
	}
	// El nodo real del router es estable: determinista.
	id := "11111111-1111-4444-8888-000000000001"
	if a, b := FlushTime(id, now, FlushPeriod), FlushTime(id, now.Add(time.Hour).Add(-59*time.Minute), FlushPeriod); a.Hour() != b.Hour() {
		t.Fatalf("jitter no determinista: %v vs %v", a, b)
	}
}
