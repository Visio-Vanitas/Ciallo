package proxy

import (
	"context"
	"net"
	"time"
)

type Backend struct {
	Name        string
	Addr        string
	StatusCache *bool
}

type Route struct {
	Hosts   []string
	Backend Backend
}

type Router interface {
	Resolve(host string) (Backend, bool)
}

type Dialer interface {
	Dial(ctx context.Context, backend Backend) (net.Conn, error)
}

type StatusDialer interface {
	DialStatus(ctx context.Context, backend Backend) (net.Conn, error)
}

type StatusCache interface {
	Get(key string) ([]byte, bool)
	GetFallback(key string, protocolVersion int32) ([]byte, bool)
	SetWithFallback(key string, data []byte, ttl, fallbackTTL time.Duration)
}

type FailGuard interface {
	Enabled() bool
	EarlyDisconnect() time.Duration
	IsBanned(route, kind, value string) bool
	RecordFailure(route, kind, value string) bool
	RecordSuccess(route, kind, value string)
	Unban(route, kind, value string) bool
}

type MetricsRecorder interface {
	IncActiveConnections()
	DecActiveConnections()
	RecordStatus(route, backend, cacheResult string, fallbackUsed bool)
	RecordLogin(route, backend, fail2banAction string)
	RecordBackendDialError(route, backend string)
	RecordFail2BanBlock(route, kind string)
	RecordStatusCircuitBreaker(backend string)
}

type HealthStatus struct {
	Healthy     bool
	CircuitOpen bool
}

type HealthChecker interface {
	Unhealthy(backend Backend) bool
	Status(backend Backend) HealthStatus
	RecordFailure(backend string, err error)
	RecordSuccess(backend string)
}

type Options struct {
	ListenAddr                  string
	HandshakeTimeout            time.Duration
	BackendDialTimeout          time.Duration
	IdleTimeout                 time.Duration
	ShutdownTimeout             time.Duration
	MaxHandshakeSize            int
	StatusCacheEnabled          bool
	StatusCacheTTL              time.Duration
	MOTDCacheEnabled            bool
	MOTDFallbackTTL             time.Duration
	Metrics                     MetricsRecorder
	Health                      HealthChecker
	StatusFallbackWhenUnhealthy bool
}
