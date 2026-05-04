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
	"ciallo/internal/logging"
	"ciallo/internal/management"
	"ciallo/internal/pool"
	"ciallo/internal/proxy"
)

var version = "v0.0.2"

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

	logger := logging.New(cfg.Logging.Level)
	slog.SetDefault(logger)

	routes := cfg.RouteBackends()
	router := proxy.NewStaticRouter(routes, cfg.DefaultBackendConfig())
	statusCache := cache.NewStatusCache(time.Now)

	var connPool *pool.Pool
	if cfg.Pool.Enabled {
		connPool = pool.New(pool.Options{
			MaxIdlePerBackend: cfg.Pool.MaxIdlePerBackend,
			IdleTimeout:       cfg.Pool.IdleTimeout.Duration,
		})
		defer connPool.Close()
	}
	dialer := proxy.NewNetDialer(cfg.Timeouts.BackendDial.Duration, connPool)
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
	mgmt := management.New(management.Options{
		Enabled: cfg.Management.Enabled,
		Address: cfg.Management.Address,
	}, guard, logger)

	server := proxy.NewServerWithGuard(proxy.Options{
		ListenAddr:         cfg.Listen,
		HandshakeTimeout:   cfg.Timeouts.Handshake.Duration,
		BackendDialTimeout: cfg.Timeouts.BackendDial.Duration,
		IdleTimeout:        cfg.Timeouts.Idle.Duration,
		ShutdownTimeout:    cfg.Timeouts.Shutdown.Duration,
		MaxHandshakeSize:   cfg.MaxHandshakeSize,
		StatusCacheEnabled: cfg.StatusCache.Enabled,
		StatusCacheTTL:     cfg.StatusCache.TTL.Duration,
		MOTDCacheEnabled:   cfg.MOTDCache.Enabled,
		MOTDFallbackTTL:    cfg.MOTDCache.FallbackTTL.Duration,
	}, router, dialer, statusCache, guard, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
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
