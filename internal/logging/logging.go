package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"ciallo/internal/config"

	"gopkg.in/natefinch/lumberjack.v2"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func New(cfg config.LoggingConfig) (*slog.Logger, io.Closer, error) {
	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}
	writer, closer, err := writer(cfg)
	if err != nil {
		return nil, nil, err
	}
	options := &slog.HandlerOptions{
		Level:     level,
		AddSource: cfg.AddSource,
	}
	var handler slog.Handler
	switch normalize(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(writer, options)
	case "text", "":
		handler = slog.NewTextHandler(writer, options)
	default:
		_ = closer.Close()
		return nil, nil, fmt.Errorf("invalid log format %q", cfg.Format)
	}
	return slog.New(handler), closer, nil
}

func parseLevel(raw string) (slog.Level, error) {
	switch normalize(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level %q", raw)
	}
}

func writer(cfg config.LoggingConfig) (io.Writer, io.Closer, error) {
	switch normalize(cfg.Output) {
	case "stdout", "":
		return os.Stdout, nopCloser{}, nil
	case "stderr":
		return os.Stderr, nopCloser{}, nil
	case "file":
		if strings.TrimSpace(cfg.File.Path) == "" {
			return nil, nil, fmt.Errorf("log file path is required")
		}
		l := &lumberjack.Logger{
			Filename:   cfg.File.Path,
			MaxSize:    cfg.File.MaxSizeMB,
			MaxBackups: cfg.File.MaxBackups,
			MaxAge:     cfg.File.MaxAgeDays,
			Compress:   cfg.File.Compress,
		}
		return l, l, nil
	default:
		return nil, nil, fmt.Errorf("invalid log output %q", cfg.Output)
	}
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
