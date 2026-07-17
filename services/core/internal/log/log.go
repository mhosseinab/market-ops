// Package log builds the structured slog JSON logger used across the core
// binary. JSON on stdout is the collection contract for the OpenTelemetry/Loki
// stack (dk-p0-monorepo.md §8, docs/14): every line carries correlating fields
// added by callers via logger.With.
package log

import (
	"io"
	"log/slog"
	"strings"
)

// New returns a slog.Logger emitting JSON at the given level to w.
func New(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}

// ParseLevel maps a textual level ("debug", "info", "warn", "error") to a
// slog.Level, defaulting to Info for empty or unknown values.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
