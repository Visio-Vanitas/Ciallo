package pool

import (
	"context"
	"net"
	"sync"
	"time"
)

type Options struct {
	MaxIdlePerBackend int
	IdleTimeout       time.Duration
}

type Pool struct {
	mu      sync.Mutex
	options Options
	idle    map[string][]pooledConn
	closed  bool
}

type pooledConn struct {
	conn    net.Conn
	addedAt time.Time
}

func New(options Options) *Pool {
	if options.MaxIdlePerBackend <= 0 {
		options.MaxIdlePerBackend = 1
	}
	if options.IdleTimeout <= 0 {
		options.IdleTimeout = 30 * time.Second
	}
	return &Pool{
		options: options,
		idle:    make(map[string][]pooledConn),
	}
}

func (p *Pool) Get(ctx context.Context, backend string) (net.Conn, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, false
	}
	now := time.Now()
	conns := p.idle[backend]
	for len(conns) > 0 {
		idx := len(conns) - 1
		item := conns[idx]
		conns = conns[:idx]
		p.idle[backend] = conns
		if now.Sub(item.addedAt) <= p.options.IdleTimeout {
			return item.conn, true
		}
		_ = item.conn.Close()
	}
	if err := ctx.Err(); err != nil {
		return nil, false
	}
	return nil, false
}

func (p *Pool) NeedsConn(backend string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	now := time.Now()
	conns := p.idle[backend]
	kept := conns[:0]
	for _, item := range conns {
		if now.Sub(item.addedAt) <= p.options.IdleTimeout {
			kept = append(kept, item)
		} else {
			_ = item.conn.Close()
		}
	}
	p.idle[backend] = kept
	return len(kept) < p.options.MaxIdlePerBackend
}

func (p *Pool) Put(backend string, conn net.Conn) {
	if conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		_ = conn.Close()
		return
	}
	conns := p.idle[backend]
	if len(conns) >= p.options.MaxIdlePerBackend {
		_ = conn.Close()
		return
	}
	p.idle[backend] = append(conns, pooledConn{
		conn:    conn,
		addedAt: time.Now(),
	})
}

func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for backend, conns := range p.idle {
		for _, item := range conns {
			_ = item.conn.Close()
		}
		delete(p.idle, backend)
	}
}
