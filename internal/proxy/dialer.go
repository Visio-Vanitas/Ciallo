package proxy

import (
	"context"
	"log/slog"
	"net"
	"time"

	"ciallo/internal/pool"
)

type NetDialer struct {
	timeout time.Duration
	pool    *pool.Pool
	logger  *slog.Logger
}

func NewNetDialer(timeout time.Duration, pool *pool.Pool) *NetDialer {
	return NewNetDialerWithLogger(timeout, pool, nil)
}

func NewNetDialerWithLogger(timeout time.Duration, pool *pool.Pool, logger *slog.Logger) *NetDialer {
	if logger == nil {
		logger = slog.Default()
	}
	return &NetDialer{
		timeout: timeout,
		pool:    pool,
		logger:  logger,
	}
}

func (d *NetDialer) Dial(ctx context.Context, backend Backend) (net.Conn, error) {
	return d.dialFresh(ctx, backend)
}

func (d *NetDialer) DialStatus(ctx context.Context, backend Backend) (net.Conn, error) {
	if d.pool != nil {
		if conn, ok := d.pool.Get(ctx, backend.Addr); ok {
			d.logger.Debug("status pool hit", "backend", backend.Addr)
			go d.Refill(context.Background(), backend)
			return conn, nil
		}
		d.logger.Debug("status pool miss", "backend", backend.Addr)
		defer d.Refill(context.Background(), backend)
	}
	return d.dialFresh(ctx, backend)
}

func (d *NetDialer) Warm(ctx context.Context, backends []Backend) {
	if d.pool == nil {
		return
	}
	for _, backend := range backends {
		d.Refill(ctx, backend)
	}
}

func (d *NetDialer) Refill(ctx context.Context, backend Backend) {
	if d.pool == nil || !d.pool.NeedsConn(backend.Addr) {
		return
	}
	dialCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	conn, err := d.dialFresh(dialCtx, backend)
	if err != nil {
		d.logger.Debug("status pool refill failed", "backend", backend.Addr, "err", err)
		return
	}
	d.pool.Put(backend.Addr, conn)
	d.logger.Debug("status pool refilled", "backend", backend.Addr)
}

func (d *NetDialer) dialFresh(ctx context.Context, backend Backend) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout:   d.timeout,
		KeepAlive: 30 * time.Second,
	}
	return dialer.DialContext(ctx, "tcp", backend.Addr)
}
