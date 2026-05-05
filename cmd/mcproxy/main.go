package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ciallo/internal/cache"
	"ciallo/internal/config"
	"ciallo/internal/fail2ban"
	"ciallo/internal/health"
	"ciallo/internal/logging"
	"ciallo/internal/management"
	"ciallo/internal/metrics"
	"ciallo/internal/pool"
	"ciallo/internal/proxy"
)

var version = "v0.0.5"

func main() {
	configPath := flag.String("config", "configs/example.yaml", "path to YAML config")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		os.Stdout.WriteString(version + "\n")
		return
	}

	cfg, err := config.LoadFile(*configPath)
	if err != nil {
		slog.Error("load config failed", "err", err)
		os.Exit(1)
	}

	logger, logCloser, err := logging.New(cfg.Logging)
	if err != nil {
		slog.Error("configure logging failed", "err", err)
		os.Exit(1)
	}
	defer logCloser.Close()
	slog.SetDefault(logger)
	logger.Info("starting ciallo",
		"version", version,
		"listen", cfg.Listen,
		"routes", len(cfg.Routes),
		"default_backend", cfg.DefaultBackend,
		"log_level", cfg.Logging.Level,
		"log_format", cfg.Logging.Format,
		"log_output", cfg.Logging.Output,
		"status_cache_enabled", cfg.StatusCache.Enabled,
		"motd_cache_enabled", cfg.MOTDCache.Enabled,
		"backend_health_enabled", cfg.BackendHealth.Enabled,
		"backend_health_interval", cfg.BackendHealth.Interval.Duration,
		"backend_health_failure_threshold", cfg.BackendHealth.FailureThreshold,
		"backend_health_circuit_breaker_ttl", cfg.BackendHealth.CircuitBreakerTTL.Duration,
		"fail2ban_enabled", cfg.Fail2Ban.Enabled,
		"management_enabled", cfg.Management.Enabled,
		"pool_enabled", cfg.Pool.Enabled,
	)

	routes := cfg.RouteBackends()
	defaultBackend := cfg.DefaultBackendConfig()
	router := proxy.NewStaticRouter(routes, defaultBackend)
	statusCache := cache.NewStatusCache(time.Now)
	metricsRecorder := metrics.New()
	healthChecker := health.New(health.Options{
		Enabled:           cfg.BackendHealth.Enabled,
		Interval:          cfg.BackendHealth.Interval.Duration,
		Timeout:           cfg.BackendHealth.Timeout.Duration,
		FailureThreshold:  cfg.BackendHealth.FailureThreshold,
		SuccessThreshold:  cfg.BackendHealth.SuccessThreshold,
		ProbeProtocol:     cfg.BackendHealth.ProbeProtocol,
		ProbeHost:         cfg.BackendHealth.ProbeHost,
		CircuitBreakerTTL: cfg.BackendHealth.CircuitBreakerTTL.Duration,
	}, health.BuildTargets(routes, defaultBackend, cfg.BackendHealth.ProbeHost), nil, logger)
	metricsRecorder.SetHealthSource(healthChecker)

	var connPool *pool.Pool
	if cfg.Pool.Enabled {
		connPool = pool.New(pool.Options{
			MaxIdlePerBackend: cfg.Pool.MaxIdlePerBackend,
			IdleTimeout:       cfg.Pool.IdleTimeout.Duration,
		})
		defer connPool.Close()
	}
	dialer := proxy.NewNetDialerWithLogger(cfg.Timeouts.BackendDial.Duration, connPool, logger)
	if connPool != nil {
		dialer.Warm(context.Background(), cfg.Backends())
	}
	guard := fail2ban.New(fail2ban.Options{
		Enabled:         cfg.Fail2Ban.Enabled,
		MaxFailures:     cfg.Fail2Ban.MaxFailures,
		Window:          cfg.Fail2Ban.Window.Duration,
		BanDuration:     cfg.Fail2Ban.BanDuration.Duration,
		EarlyDisconnect: cfg.Fail2Ban.EarlyDisconnect.Duration,
	}, time.Now)
	server := proxy.NewServerWithGuard(proxy.Options{
		ListenAddr:                  cfg.Listen,
		HandshakeTimeout:            cfg.Timeouts.Handshake.Duration,
		BackendDialTimeout:          cfg.Timeouts.BackendDial.Duration,
		IdleTimeout:                 cfg.Timeouts.Idle.Duration,
		ShutdownTimeout:             cfg.Timeouts.Shutdown.Duration,
		MaxHandshakeSize:            cfg.MaxHandshakeSize,
		StatusCacheEnabled:          cfg.StatusCache.Enabled,
		StatusCacheTTL:              cfg.StatusCache.TTL.Duration,
		MOTDCacheEnabled:            cfg.MOTDCache.Enabled,
		MOTDFallbackTTL:             cfg.MOTDCache.FallbackTTL.Duration,
		Metrics:                     metricsRecorder,
		Health:                      healthChecker,
		StatusFallbackWhenUnhealthy: cfg.BackendHealth.StatusFallbackWhenUnhealthy,
	}, router, dialer, statusCache, guard, logger)

	mgmt := management.NewWithHealth(management.Options{
		Enabled: cfg.Management.Enabled,
		Address: cfg.Management.Address,
		Version: version,
	}, guard, metricsRecorder, server, healthChecker, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	go healthChecker.Run(ctx)
	if cfg.Management.Enabled {
		go func() {
			if err := mgmt.ListenAndServe(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("management server failed", "err", err)
				stop()
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Timeouts.Shutdown.Duration)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown failed", "err", err)
			os.Exit(1)
		}
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}
}
