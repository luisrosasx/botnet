//go:build !unix && !windows

package watchdog

import (
	"errors"
	"os"
)

var errWouldBlock = errors.New("watchdog: flock no soportado en este SO")

func flockExclusiveNB(f *os.File) error { return errWouldBlock }
func unlock(fd int)                     {}
func flockFD(f *os.File) int            { return int(f.Fd()) }
func PIDAlive(pid int) bool             { return pid > 0 }
