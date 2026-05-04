package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ciallo/internal/config"
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	tests := []config.LoggingConfig{
		{Level: "loud", Format: "text", Output: "stdout"},
		{Level: "info", Format: "xml", Output: "stdout"},
		{Level: "info", Format: "text", Output: "socket"},
		{Level: "info", Format: "text", Output: "file"},
	}
	for _, cfg := range tests {
		if logger, closer, err := New(cfg); err == nil {
			_ = closer.Close()
			t.Fatalf("New(%+v) returned logger %v and nil error", cfg, logger)
		}
	}
}

func TestNewWritesJSONFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ciallo.log")
	logger, closer, err := New(config.LoggingConfig{
		Level:  "debug",
		Format: "json",
		Output: "file",
		File: config.LoggingFileConfig{
			Path:       path,
			MaxSizeMB:  1,
			MaxBackups: 1,
			MaxAgeDays: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hello", "event", "test")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"msg":"hello"`) || !strings.Contains(text, `"event":"test"`) {
		t.Fatalf("log file missing JSON fields: %s", text)
	}
}
