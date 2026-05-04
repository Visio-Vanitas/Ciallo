package proxy

import (
	"context"
	"net"
	"time"

	"ciallo/internal/pool"
)

type NetDialer struct {
	timeout time.Duration
	pool    *pool.Pool
}

func NewNetDialer(timeout time.Duration, pool *pool.Pool) *NetDialer {
	return &NetDialer{
		timeout: timeout,
		pool:    pool,
	}
}

func (d *NetDialer) Dial(ctx context.Context, backend Backend) (net.Conn, error) {
	return d.dialFresh(ctx, backend)
}

func (d *NetDialer) DialStatus(ctx context.Context, backend Backend) (net.Conn, error) {
	if d.pool != nil {
		if conn, ok := d.pool.Get(ctx, backend.Addr); ok {
			go d.Refill(context.Background(), backend)
			return conn, nil
		}
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
		return
	}
	d.pool.Put(backend.Addr, conn)
}

func (d *NetDialer) dialFresh(ctx context.Context, backend Backend) (net.Conn, error) {
	dialer := net.Dialer{
		Timeout:   d.timeout,
		KeepAlive: 30 * time.Second,
	}
	return dialer.DialContext(ctx, "tcp", backend.Addr)
}
