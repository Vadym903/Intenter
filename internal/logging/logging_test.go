package logging

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":    slog.LevelDebug,
		"DEBUG":    slog.LevelDebug,
		"info":     slog.LevelInfo,
		"warn":     slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"error":    slog.LevelError,
		"":         slog.LevelInfo,
		"nonsense": slog.LevelInfo,
	}
	for input, want := range tests {
		if got := ParseLevel(input); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestFileLoggerWritesJSONToTheLogDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")

	logger, closer, err := FileLogger(dir, DaemonLogFile, "info")
	if err != nil {
		t.Fatalf("FileLogger: %v", err)
	}
	logger.Info("daemon started", "endpoint", "/tmp/intenter.sock")
	logger.Debug("this must be filtered out")
	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, DaemonLogFile))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("log lines = %d, want 1 (debug must be filtered at info level): %s", len(lines), raw)
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("log line is not JSON: %v (%s)", err, lines[0])
	}
	if entry["msg"] != "daemon started" {
		t.Errorf("msg = %v", entry["msg"])
	}
	if entry["endpoint"] != "/tmp/intenter.sock" {
		t.Errorf("endpoint = %v", entry["endpoint"])
	}
	if _, ok := entry["time"]; !ok {
		t.Error("file logs must carry a timestamp")
	}
}

func TestFileLoggerCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs")
	_, closer, err := FileLogger(dir, HookLogFile, "debug")
	if err != nil {
		t.Fatalf("FileLogger: %v", err)
	}
	defer closer.Close()

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("log directory not created: %v", err)
	}
}

func TestDiscardLoggerWritesNothing(t *testing.T) {
	logger := Discard()
	logger.Error("must not panic or write")
}

func TestNopCloser(t *testing.T) {
	if err := NopCloser().Close(); err != nil {
		t.Errorf("NopCloser().Close() = %v", err)
	}
}
