package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackendHealthDefaults(t *testing.T) {
	cfg := Default()
	if !cfg.BackendHealth.Enabled {
		t.Fatal("backend health should default enabled")
	}
	if cfg.BackendHealth.Interval.Duration != 10*time.Second || cfg.BackendHealth.Timeout.Duration != 3*time.Second {
		t.Fatalf("bad backend health durations: %+v", cfg.BackendHealth)
	}
	if cfg.BackendHealth.FailureThreshold != 2 || cfg.BackendHealth.SuccessThreshold != 1 {
		t.Fatalf("bad thresholds: %+v", cfg.BackendHealth)
	}
	if cfg.BackendHealth.ProbeProtocol != 772 || !cfg.BackendHealth.StatusFallbackWhenUnhealthy {
		t.Fatalf("bad probe/fallback defaults: %+v", cfg.BackendHealth)
	}
	if cfg.MaxStatusResponseSize != 256*1024 {
		t.Fatalf("max status response size = %d", cfg.MaxStatusResponseSize)
	}
	if cfg.StatusFallback.VersionName != "ciallo fallback" || cfg.StatusFallback.PlayersMax != 0 {
		t.Fatalf("bad status fallback defaults: %+v", cfg.StatusFallback)
	}
}

func TestLoggingDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Logging.Level != "info" {
		t.Fatalf("level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Fatalf("format = %q", cfg.Logging.Format)
	}
	if cfg.Logging.Output != "stdout" {
		t.Fatalf("output = %q", cfg.Logging.Output)
	}
	if cfg.Logging.File.MaxSizeMB != 100 || cfg.Logging.File.MaxBackups != 7 || cfg.Logging.File.MaxAgeDays != 14 {
		t.Fatalf("bad file defaults: %+v", cfg.Logging.File)
	}
	if !cfg.Logging.File.Compress {
		t.Fatal("file compression should default to true")
	}
}

func TestLoadFileLoggingValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
listen: ":25565"
default_backend: "127.0.0.1:25566"
logging:
  level: "debug"
  format: "json"
  output: "file"
  file:
    path: "/tmp/ciallo.log"
    max_size_mb: 12
    max_backups: 3
    max_age_days: 4
    compress: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "json" || cfg.Logging.Output != "file" {
		t.Fatalf("logging config not loaded: %+v", cfg.Logging)
	}
	if cfg.Logging.File.Path != "/tmp/ciallo.log" || cfg.Logging.File.MaxSizeMB != 12 || cfg.Logging.File.MaxBackups != 3 || cfg.Logging.File.MaxAgeDays != 4 {
		t.Fatalf("file config not loaded: %+v", cfg.Logging.File)
	}
}

func TestLoadFileRejectsFileLoggingWithoutPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
listen: ":25565"
default_backend: "127.0.0.1:25566"
logging:
  output: "file"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile should reject file logging without path")
	}
}

func TestLoadFileStatusFallbackConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
listen: ":25565"
max_status_response_size: 131072
default_backend: "127.0.0.1:25566"
status_fallback:
  version_name: "cached status"
  players_max: 99
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStatusResponseSize != 131072 {
		t.Fatalf("max status response size = %d", cfg.MaxStatusResponseSize)
	}
	if cfg.StatusFallback.VersionName != "cached status" || cfg.StatusFallback.PlayersMax != 99 {
		t.Fatalf("status fallback = %+v", cfg.StatusFallback)
	}
}

func TestLoadFileRejectsInvalidStatusHardening(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`
listen: ":25565"
max_status_response_size: -1
default_backend: "127.0.0.1:25566"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile should reject negative max_status_response_size")
	}

	if err := os.WriteFile(path, []byte(`
listen: ":25565"
default_backend: "127.0.0.1:25566"
status_fallback:
  players_max: -1
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("LoadFile should reject negative status_fallback.players_max")
	}
}
