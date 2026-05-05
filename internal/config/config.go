package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"ciallo/internal/proxy"
)

const defaultMaxHandshakeSize = 64 * 1024

type Config struct {
	Listen           string            `yaml:"listen"`
	Timeouts         Timeouts          `yaml:"timeouts"`
	StatusCache      StatusCacheConfig `yaml:"status_cache"`
	MOTDCache        MOTDCacheConfig   `yaml:"motd_cache"`
	BackendHealth    BackendHealth     `yaml:"backend_health"`
	Fail2Ban         Fail2BanConfig    `yaml:"fail2ban"`
	Management       ManagementConfig  `yaml:"management"`
	Pool             PoolConfig        `yaml:"pool"`
	Routes           []RouteConfig     `yaml:"routes"`
	DefaultBackend   string            `yaml:"default_backend"`
	Logging          LoggingConfig     `yaml:"logging"`
	MaxHandshakeSize int               `yaml:"max_handshake_size"`
}

type Timeouts struct {
	Handshake   Duration `yaml:"handshake"`
	BackendDial Duration `yaml:"backend_dial"`
	Idle        Duration `yaml:"idle"`
	Shutdown    Duration `yaml:"shutdown"`
}

type StatusCacheConfig struct {
	Enabled bool     `yaml:"enabled"`
	TTL     Duration `yaml:"ttl"`
}

type MOTDCacheConfig struct {
	Enabled     bool     `yaml:"enabled"`
	FallbackTTL Duration `yaml:"fallback_ttl"`
}

type BackendHealth struct {
	Enabled                     bool     `yaml:"enabled"`
	Interval                    Duration `yaml:"interval"`
	Timeout                     Duration `yaml:"timeout"`
	FailureThreshold            int      `yaml:"failure_threshold"`
	SuccessThreshold            int      `yaml:"success_threshold"`
	ProbeProtocol               int32    `yaml:"probe_protocol"`
	ProbeHost                   string   `yaml:"probe_host"`
	CircuitBreakerTTL           Duration `yaml:"circuit_breaker_ttl"`
	StatusFallbackWhenUnhealthy bool     `yaml:"status_fallback_when_unhealthy"`
}

type Fail2BanConfig struct {
	Enabled         bool     `yaml:"enabled"`
	MaxFailures     int      `yaml:"max_failures"`
	Window          Duration `yaml:"window"`
	BanDuration     Duration `yaml:"ban_duration"`
	EarlyDisconnect Duration `yaml:"early_disconnect"`
}

type ManagementConfig struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
}

type PoolConfig struct {
	Enabled           bool     `yaml:"enabled"`
	MaxIdlePerBackend int      `yaml:"max_idle_per_backend"`
	IdleTimeout       Duration `yaml:"idle_timeout"`
}

type RouteConfig struct {
	Hosts       []string `yaml:"hosts"`
	Backend     string   `yaml:"backend"`
	StatusCache *bool    `yaml:"status_cache"`
}

type LoggingConfig struct {
	Level     string            `yaml:"level"`
	Format    string            `yaml:"format"`
	Output    string            `yaml:"output"`
	File      LoggingFileConfig `yaml:"file"`
	AddSource bool              `yaml:"add_source"`
}

type LoggingFileConfig struct {
	Path       string `yaml:"path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAgeDays int    `yaml:"max_age_days"`
	Compress   bool   `yaml:"compress"`
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return err
		}
		d.Duration = parsed
		return nil
	case int:
		d.Duration = time.Duration(v)
		return nil
	case int64:
		d.Duration = time.Duration(v)
		return nil
	default:
		return fmt.Errorf("unsupported duration value %T", raw)
	}
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

func Default() Config {
	return Config{
		Listen: ":25565",
		Timeouts: Timeouts{
			Handshake:   Duration{3 * time.Second},
			BackendDial: Duration{3 * time.Second},
			Idle:        Duration{10 * time.Minute},
			Shutdown:    Duration{10 * time.Second},
		},
		StatusCache: StatusCacheConfig{
			Enabled: true,
			TTL:     Duration{5 * time.Second},
		},
		MOTDCache: MOTDCacheConfig{
			Enabled:     true,
			FallbackTTL: Duration{5 * time.Minute},
		},
		BackendHealth: BackendHealth{
			Enabled:                     true,
			Interval:                    Duration{10 * time.Second},
			Timeout:                     Duration{3 * time.Second},
			FailureThreshold:            2,
			SuccessThreshold:            1,
			ProbeProtocol:               772,
			CircuitBreakerTTL:           Duration{30 * time.Second},
			StatusFallbackWhenUnhealthy: true,
		},
		Fail2Ban: Fail2BanConfig{
			Enabled:         false,
			MaxFailures:     5,
			Window:          Duration{time.Minute},
			BanDuration:     Duration{10 * time.Minute},
			EarlyDisconnect: Duration{5 * time.Second},
		},
		Management: ManagementConfig{
			Enabled: false,
			Address: "127.0.0.1:25575",
		},
		Pool: PoolConfig{
			Enabled:           false,
			MaxIdlePerBackend: 8,
			IdleTimeout:       Duration{30 * time.Second},
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
			Output: "stdout",
			File: LoggingFileConfig{
				MaxSizeMB:  100,
				MaxBackups: 7,
				MaxAgeDays: 14,
				Compress:   true,
			},
		},
		MaxHandshakeSize: defaultMaxHandshakeSize,
	}
}

func (c *Config) ApplyDefaults() {
	defaults := Default()
	if c.Listen == "" {
		c.Listen = defaults.Listen
	}
	if c.Timeouts.Handshake.Duration == 0 {
		c.Timeouts.Handshake = defaults.Timeouts.Handshake
	}
	if c.Timeouts.BackendDial.Duration == 0 {
		c.Timeouts.BackendDial = defaults.Timeouts.BackendDial
	}
	if c.Timeouts.Idle.Duration == 0 {
		c.Timeouts.Idle = defaults.Timeouts.Idle
	}
	if c.Timeouts.Shutdown.Duration == 0 {
		c.Timeouts.Shutdown = defaults.Timeouts.Shutdown
	}
	if c.StatusCache.TTL.Duration == 0 {
		c.StatusCache.TTL = defaults.StatusCache.TTL
	}
	if c.MOTDCache.FallbackTTL.Duration == 0 {
		c.MOTDCache.FallbackTTL = defaults.MOTDCache.FallbackTTL
	}
	if c.BackendHealth.Interval.Duration == 0 {
		c.BackendHealth.Interval = defaults.BackendHealth.Interval
	}
	if c.BackendHealth.Timeout.Duration == 0 {
		c.BackendHealth.Timeout = defaults.BackendHealth.Timeout
	}
	if c.BackendHealth.FailureThreshold == 0 {
		c.BackendHealth.FailureThreshold = defaults.BackendHealth.FailureThreshold
	}
	if c.BackendHealth.SuccessThreshold == 0 {
		c.BackendHealth.SuccessThreshold = defaults.BackendHealth.SuccessThreshold
	}
	if c.BackendHealth.ProbeProtocol == 0 {
		c.BackendHealth.ProbeProtocol = defaults.BackendHealth.ProbeProtocol
	}
	if c.BackendHealth.CircuitBreakerTTL.Duration == 0 {
		c.BackendHealth.CircuitBreakerTTL = defaults.BackendHealth.CircuitBreakerTTL
	}
	if c.Fail2Ban.MaxFailures == 0 {
		c.Fail2Ban.MaxFailures = defaults.Fail2Ban.MaxFailures
	}
	if c.Fail2Ban.Window.Duration == 0 {
		c.Fail2Ban.Window = defaults.Fail2Ban.Window
	}
	if c.Fail2Ban.BanDuration.Duration == 0 {
		c.Fail2Ban.BanDuration = defaults.Fail2Ban.BanDuration
	}
	if c.Fail2Ban.EarlyDisconnect.Duration == 0 {
		c.Fail2Ban.EarlyDisconnect = defaults.Fail2Ban.EarlyDisconnect
	}
	if c.Management.Address == "" {
		c.Management.Address = defaults.Management.Address
	}
	if c.Pool.MaxIdlePerBackend == 0 {
		c.Pool.MaxIdlePerBackend = defaults.Pool.MaxIdlePerBackend
	}
	if c.Pool.IdleTimeout.Duration == 0 {
		c.Pool.IdleTimeout = defaults.Pool.IdleTimeout
	}
	if c.Logging.Level == "" {
		c.Logging.Level = defaults.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = defaults.Logging.Format
	}
	if c.Logging.Output == "" {
		c.Logging.Output = defaults.Logging.Output
	}
	if c.Logging.File.MaxSizeMB == 0 {
		c.Logging.File.MaxSizeMB = defaults.Logging.File.MaxSizeMB
	}
	if c.Logging.File.MaxBackups == 0 {
		c.Logging.File.MaxBackups = defaults.Logging.File.MaxBackups
	}
	if c.Logging.File.MaxAgeDays == 0 {
		c.Logging.File.MaxAgeDays = defaults.Logging.File.MaxAgeDays
	}
	if c.MaxHandshakeSize == 0 {
		c.MaxHandshakeSize = defaults.MaxHandshakeSize
	}
}

func (c Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen is required")
	}
	if c.DefaultBackend == "" && len(c.Routes) == 0 {
		return errors.New("at least one route or default_backend is required")
	}
	if c.DefaultBackend != "" {
		if err := validateAddr(c.DefaultBackend); err != nil {
			return fmt.Errorf("default_backend: %w", err)
		}
	}
	seen := map[string]struct{}{}
	for i, route := range c.Routes {
		if route.Backend == "" {
			return fmt.Errorf("routes[%d].backend is required", i)
		}
		if err := validateAddr(route.Backend); err != nil {
			return fmt.Errorf("routes[%d].backend: %w", i, err)
		}
		for _, host := range route.Hosts {
			normalized := proxy.NormalizeHost(host)
			if normalized == "" {
				return fmt.Errorf("routes[%d].hosts contains an empty host", i)
			}
			if _, ok := seen[normalized]; ok {
				return fmt.Errorf("duplicate route host %q", normalized)
			}
			seen[normalized] = struct{}{}
		}
	}
	if c.MaxHandshakeSize <= 0 {
		return errors.New("max_handshake_size must be positive")
	}
	if err := validateBackendHealth(c.BackendHealth); err != nil {
		return fmt.Errorf("backend_health: %w", err)
	}
	if err := validateLogging(c.Logging); err != nil {
		return fmt.Errorf("logging: %w", err)
	}
	return nil
}

func validateBackendHealth(health BackendHealth) error {
	if health.Interval.Duration <= 0 {
		return errors.New("interval must be positive")
	}
	if health.Timeout.Duration <= 0 {
		return errors.New("timeout must be positive")
	}
	if health.FailureThreshold <= 0 {
		return errors.New("failure_threshold must be positive")
	}
	if health.SuccessThreshold <= 0 {
		return errors.New("success_threshold must be positive")
	}
	if health.ProbeProtocol <= 0 {
		return errors.New("probe_protocol must be positive")
	}
	if health.CircuitBreakerTTL.Duration <= 0 {
		return errors.New("circuit_breaker_ttl must be positive")
	}
	return nil
}

func validateLogging(logging LoggingConfig) error {
	switch strings.ToLower(strings.TrimSpace(logging.Level)) {
	case "debug", "info", "warn", "warning", "error":
	default:
		return fmt.Errorf("invalid level %q", logging.Level)
	}
	switch strings.ToLower(strings.TrimSpace(logging.Format)) {
	case "text", "json":
	default:
		return fmt.Errorf("invalid format %q", logging.Format)
	}
	switch strings.ToLower(strings.TrimSpace(logging.Output)) {
	case "stdout", "stderr":
	case "file":
		if strings.TrimSpace(logging.File.Path) == "" {
			return errors.New("file.path is required when output is file")
		}
	default:
		return fmt.Errorf("invalid output %q", logging.Output)
	}
	if logging.File.MaxSizeMB <= 0 {
		return errors.New("file.max_size_mb must be positive")
	}
	if logging.File.MaxBackups < 0 {
		return errors.New("file.max_backups cannot be negative")
	}
	if logging.File.MaxAgeDays < 0 {
		return errors.New("file.max_age_days cannot be negative")
	}
	return nil
}

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if strings.TrimSpace(host) == "" {
		return errors.New("host is empty")
	}
	if strings.TrimSpace(port) == "" {
		return errors.New("port is empty")
	}
	return nil
}

func (c Config) RouteBackends() []proxy.Route {
	routes := make([]proxy.Route, 0, len(c.Routes))
	for _, route := range c.Routes {
		backend := proxy.Backend{
			Name:        route.Backend,
			Addr:        route.Backend,
			StatusCache: route.StatusCache,
		}
		routes = append(routes, proxy.Route{
			Hosts:   route.Hosts,
			Backend: backend,
		})
	}
	return routes
}

func (c Config) DefaultBackendConfig() *proxy.Backend {
	if c.DefaultBackend == "" {
		return nil
	}
	return &proxy.Backend{
		Name: c.DefaultBackend,
		Addr: c.DefaultBackend,
	}
}

func (c Config) Backends() []proxy.Backend {
	seen := make(map[string]struct{})
	backends := make([]proxy.Backend, 0, len(c.Routes)+1)
	add := func(backend proxy.Backend) {
		if backend.Addr == "" {
			return
		}
		if _, ok := seen[backend.Addr]; ok {
			return
		}
		seen[backend.Addr] = struct{}{}
		backends = append(backends, backend)
	}
	for _, route := range c.RouteBackends() {
		add(route.Backend)
	}
	if def := c.DefaultBackendConfig(); def != nil {
		add(*def)
	}
	return backends
}
