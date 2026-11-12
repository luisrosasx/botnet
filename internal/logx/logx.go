// Package logx centraliza slog: los binarios del backend y del gateway
// loguean JSON con spool (BLUEPRINT §12); el agente añade después su ring
// buffer en pkg/logger.
package logx

import (
	"log/slog"
	"os"
	"strings"
)

// New construye un logger JSON a stderr; un nivel no parseable cae a info
// sin paniquear en el arranque de un router de 32 MB.
func New(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
