package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/lmittmann/tint"
)

// Logger is an alias for slog.Logger.
type Logger = slog.Logger

// Init configures the global slog logger with colored terminal output.
func Init(level string) {
	l := parseLevel(level)

	handler := tint.NewHandler(os.Stderr, &tint.Options{
		Level:      l,
		TimeFormat: "15:04:05.000",
		NoColor:    !isTerminal(),
	})

	slog.SetDefault(slog.New(handler))
}

// New returns a logger with a component attribute, derived from the global default.
func New(component string) *Logger {
	if component == "" {
		return slog.Default()
	}
	return slog.Default().With("component", component)
}

// Default returns the global logger.
func Default() *Logger { return slog.Default() }

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}

func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
