//go:build unix

package watchdog

import (
	"errors"
	"os"
	"syscall"
)

var errWouldBlock = syscall.EWOULDBLOCK

func flockExclusiveNB(f *os.File) error {
	return syscall.Flock(flockFD(f), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlock(fd int) {
	_ = syscall.Flock(fd, syscall.LOCK_UN)
}

func flockFD(f *os.File) int { return int(f.Fd()) }

// PIDAlive: senal 0 — el proceso existe sin molestarlo (permiso
// denegado tambien cuenta como vivo: existe).
func PIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
