package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// Log file names inside <DataDir>/logs (research R-08).
const (
	DaemonLogFile = "daemon.log"
	HookLogFile   = "hook.log"
)

// Rotation settings: 10 MiB per file, 5 files kept.
const (
	maxSizeMB  = 10
	maxBackups = 5
	maxAgeDays = 28
)

// ParseLevel maps a configuration value onto a slog level, defaulting to info.
func ParseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
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

// FileLogger builds a JSON logger writing to a size-rotated file. The returned
// closer flushes and releases the file.
//
// The hook client MUST use this: its stdout is the protocol channel, so nothing
// may be written there (contracts/claude-hooks.md).
func FileLogger(logDir, fileName, level string) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, nil, err
	}
	writer := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, fileName),
		MaxSize:    maxSizeMB,
		MaxBackups: maxBackups,
		MaxAge:     maxAgeDays,
		Compress:   false,
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: ParseLevel(level)})
	return slog.New(handler), writer, nil
}

// StderrLogger builds the plain-text logger CLI commands use.
func StderrLogger(level string) *slog.Logger {
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: ParseLevel(level),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// CLI output stays readable: drop the timestamp, keep the message.
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	return slog.New(handler)
}

// Discard returns a logger that drops everything, for tests and for code paths
// that must never write anywhere.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// nopCloser adapts a writer that needs no closing.
type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// NopCloser is returned by loggers that own no file handle.
func NopCloser() io.Closer { return nopCloser{} }
