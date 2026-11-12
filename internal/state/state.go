package state

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Presupuesto del estado persistido en /data/botd (T-1301 AC1):
// `wc -c ticket.bin` + `node_id.bin` <= 1 KiB. Todo Put que lo supere es
// degradacion controlada (ErrBudget), no un fallo de flash.
const BudgetBytes = 1024

// ErrBudget: el Put no cabe en el presupuesto; el fichero previo queda intacto.
var ErrBudget = errors.New("state: presupuesto de 1 KiB superado")

// Disk es el kv plano de ficheros regulares 0600 bajo un directorio 0700
// (BLUEPRINT §3). La escritura es atomica (tmp + rename): el revival del
// watchdog (EP-05) nunca ve un fichero a medias.
type Disk struct{ Dir string }

// NewDisk crea/adopta el directorio de estado con 0700.
func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Disk{Dir: dir}, nil
}

func (d *Disk) Path(name string) string { return filepath.Join(d.Dir, name) }

// validKey: una sola componente, sin travesuras de ruta.
func validKey(name string) error {
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return fmt.Errorf("state: clave invalida %q", name)
	}
	return nil
}

// sizeOf mide un fichero; 0 si no existe.
func sizeOf(path string) (int64, error) {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// TotalSize es el "wc -c" del directorio completo: el assert del criterio 1.
func (d *Disk) TotalSize() (int64, error) {
	ents, err := os.ReadDir(d.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		n, err := sizeOf(filepath.Join(d.Dir, e.Name()))
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// Put persista data con 0600 respetando el presupuesto: si la suma tras la
// escritura supera BudgetBytes devuelve ErrBudget sin tocar el fichero
// previo.
func (d *Disk) Put(name string, data []byte) error {
	if err := validKey(name); err != nil {
		return err
	}
	prev, err := sizeOf(d.Path(name))
	if err != nil {
		return err
	}
	total, err := d.TotalSize()
	if err != nil {
		return err
	}
	if total-prev+int64(len(data)) > BudgetBytes {
		return ErrBudget
	}
	tmp, err := os.CreateTemp(d.Dir, name+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), d.Path(name))
}

// Get lee la clave; os.IsNotExist en el error si no esta.
func (d *Disk) Get(name string) ([]byte, error) {
	if err := validKey(name); err != nil {
		return nil, err
	}
	return os.ReadFile(d.Path(name))
}

// Exists dice si la clave esta en disco.
func (d *Disk) Exists(name string) bool {
	if err := validKey(name); err != nil {
		return false
	}
	_, err := os.Stat(d.Path(name))
	return err == nil
}

// Delete borra la clave (bajada/bye: borrar claves sin drama si falta).
func (d *Disk) Delete(name string) error {
	if err := validKey(name); err != nil {
		return err
	}
	err := os.Remove(d.Path(name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// WithinBudget: asercion barata para el arranque y los tests.
func (d *Disk) WithinBudget() (int64, bool, error) {
	total, err := d.TotalSize()
	return total, total <= BudgetBytes, err
}

// --- T-0505: estado unico acotado en /data/botd y spool contable en /tmp ---

const (
	// MaxStateFiles: `find /data/botd -type f | wc -l` ≤ 10 en reposo.
	MaxStateFiles = 10
	// SpoolDirName: el spool contable (clase C: /tmp es tmpfs, no gasta
	// flash) vive en /tmp/botd-spool.
	SpoolDirName = "botd-spool"
	// MaxSpoolBytes: tomo del spool — deja margen bajo la cota de
	// `du -k /tmp/botd-spool` ≤ 64 KB (el directorio suma bloque).
	MaxSpoolBytes = 60 * 1024
	// DailyWriteBudget: pico de escritura diaria del spool (30 KB), sin
	// brote a hora fija (A1-05).
	DailyWriteBudget = 30 * 1024
	// FlushPeriod: periodicidad del flush contable; por sesion es el
	// otro disparador (cierra-session → Flush antes de dormir).
	FlushPeriod = 15 * time.Minute
)

// Inventory es el inventario FIJO del directorio de estado, documentado
// en docs/runbooks/persistence.md. Nueve ficheros: margen de 1 para el
// ticket del probe (EP-02) bajo la cota de 10.
var Inventory = []string{
	"node.json",
	"botd.pid",
	"state/secret.bin",
	"state/rotation.json",
	"state/key.pem",
	"state/cert.pem",
	"state/ca.pem",
	"state/device_token",
	"state/ticket.bin",
}

// CountFiles es `find <root> -type f | wc -l`: asercion de la cota 10.
func CountFiles(root string) (int, error) {
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// Los tmp atomicos de Put no cuentan en reposo: solo persisten
		// si el rename murio a medias.
		if strings.Contains(d.Name(), ".tmp-") {
			return nil
		}
		n++
		return nil
	})
	return n, err
}

// NonPrivatePerms es `find <root> -perm /077 | wc -l`: todo fichero de
// estado es 0600. En Windows los bits no existen (el doble real es el
// `find` en la imagen QEMU/Linux, anotado en el runbook).
func NonPrivatePerms(root string) (int, error) {
	if runtime.GOOS == "windows" {
		return 0, nil
	}
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, serr := d.Info()
		if serr != nil {
			return serr
		}
		if fi.Mode().Perm()&0o077 != 0 {
			n++
		}
		return nil
	})
	return n, err
}

// Spool es el append-log contable en /tmp (clase C: tmpfs). Trim por
// cola: al pasar MaxSpoolBytes se descartan los registros mas viejos —
// perder spool viejo es mejor que escribir en flash a 3 AM.
type Spool struct {
	Dir      string
	MaxBytes int
}

// NewSpool crea /tmp/botd-spool (o el dir dado por tests) con 0700.
func NewSpool(dir string) (*Spool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Spool{Dir: dir, MaxBytes: MaxSpoolBytes}, nil
}

func (s *Spool) path() string { return filepath.Join(s.Dir, "spool.bin") }

// Append añade registros y recorta a la cola si pasa el tope. Devuelve
// los bytes escritados (para auditar el pico diario ≤ 30 KB).
func (s *Spool) Append(b []byte) (int64, error) {
	f, err := os.OpenFile(s.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	nw, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil {
		return int64(nw), werr
	}
	if cerr != nil {
		return int64(nw), cerr
	}
	if err := s.trim(); err != nil {
		return int64(nw), err
	}
	return int64(nw), nil
}

// Size: `du -k` del spool.
func (s *Spool) Size() (int64, error) {
	fi, err := os.Stat(s.path())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// Flush vuelca el spool en w (el uploader contable) y lo vacia. El
// trigger es doble: por sesion (fin de sesion) y periodico a 15 min
// con jitter por nodo — nunca un brote sincronizado a hora fija.
func (s *Spool) Flush(w io.Writer) (int64, error) {
	b, err := os.ReadFile(s.path())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if w != nil {
		if _, err := w.Write(b); err != nil {
			return 0, err
		}
	}
	// rename atomico a vacio: un crash entre truncate y escritura no
	// deja spool a medias.
	tmp := s.path() + ".flush"
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, s.path()); err != nil {
		return 0, err
	}
	return int64(len(b)), nil
}

func (s *Spool) trim() error {
	size, err := s.Size()
	if err != nil || size <= int64(s.MaxBytes) {
		return err
	}
	b, err := os.ReadFile(s.path())
	if err != nil {
		return err
	}
	keep := b[len(b)-s.MaxBytes:]
	tmp := s.path() + ".trim"
	if err := os.WriteFile(tmp, keep, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// FlushTime es el proximo tick del flush periodico con jitter
// determinista por node_id: t ≡ fnv(node_id) mod period. Dos nodos del
// mismo router no despiertan a la vez y la flota no hace brote a hora
// fija (A1-05: pico diario ≤ 30 KB sin avalancha).
func FlushTime(nodeID string, now time.Time, period time.Duration) time.Time {
	if period <= 0 {
		period = FlushPeriod
	}
	offset := time.Duration(fnv32(nodeID)%uint32(period)) * time.Nanosecond
	base := now.UTC().Truncate(period)
	t := base.Add(offset)
	if !t.After(now.UTC()) {
		t = t.Add(period)
	}
	return t
}

func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
