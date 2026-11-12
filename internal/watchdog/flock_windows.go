//go:build windows

package watchdog

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// errWouldBlock: LockFileEx con FAIL_IMMEDIATELY responde
// LOCK_VIOLATION cuando otro handle ya sostiene el tramo.
var errWouldBlock = windows.ERROR_LOCK_VIOLATION

func flockExclusiveNB(f *os.File) error {
	// El tramo se pone lejos del EOF (byte invisible): LockFileEx es
	// mandatorio y un lock sobre el contenido bloquearia la LECTURA del
	// pidfile a otras handles — el guard (ReadPID) y ProbeAlive deben
	// poder leer mientras el padre sostiene el lock.
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0,
		&windows.Overlapped{Offset: 0, OffsetHigh: 0x40000000},
	)
}

func unlock(fd int) {
	_ = windows.UnlockFileEx(windows.Handle(uintptr(fd)), 0, 1, 0,
		&windows.Overlapped{Offset: 0, OffsetHigh: 0x40000000})
}

func flockFD(f *os.File) int { return int(f.Fd()) }

// PIDAlive: abrir el proceso con consulta limitada; ERROR_ACCESS_DENIED
// implica que el pid existe (otro dueno), ERROR_INVALID_PARAMETER vale
// para procesos de consola ya muertos a medias.
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		windows.CloseHandle(h)
		return true
	}
	switch {
	case errors.Is(err, syscall.ERROR_ACCESS_DENIED),
		errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return true
	default:
		return false
	}
}
