package health

import (
	"context"
	"log/slog"
	"net"
	"sort"
	"sync"
	"time"

	"ciallo/internal/metrics"
	"ciallo/internal/probe"
	"ciallo/internal/proxy"
)

type Options struct {
	Enabled           bool
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	SuccessThreshold  int
	ProbeProtocol     int32
	ProbeHost         string
	CircuitBreakerTTL time.Duration
}

type BackendTarget struct {
	Backend   proxy.Backend
	ProbeHost string
}

type CheckFunc func(ctx context.Context, target BackendTarget, options Options) error

type Checker struct {
	options Options
	check   CheckFunc
	logger  *slog.Logger
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	Target           BackendTarget
	Healthy          bool
	ConsecutiveFails int
	ConsecutiveOKs   int
	CircuitOpenUntil time.Time
	LastChecked      time.Time
	LastError        string
	CheckSuccesses   int64
	CheckFailures    int64
	CircuitTrips     int64
}

type BackendStatus struct {
	Backend          string    `json:"backend"`
	ProbeHost        string    `json:"probe_host"`
	Healthy          bool      `json:"healthy"`
	CircuitOpen      bool      `json:"circuit_open"`
	CircuitOpenUntil time.Time `json:"circuit_open_until,omitempty"`
	LastChecked      time.Time `json:"last_checked,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	ConsecutiveFails int       `json:"consecutive_failures"`
	ConsecutiveOKs   int       `json:"consecutive_successes"`
	CheckSuccesses   int64     `json:"check_successes"`
	CheckFailures    int64     `json:"check_failures"`
	CircuitTrips     int64     `json:"circuit_trips"`
}

type Snapshot struct {
	Enabled   bool            `json:"enabled"`
	Total     int             `json:"total"`
	Healthy   int             `json:"healthy"`
	Unhealthy int             `json:"unhealthy"`
	Backends  []BackendStatus `json:"backends"`
}

func New(options Options, targets []BackendTarget, check CheckFunc, logger *slog.Logger) *Checker {
	if options.Interval == 0 {
		options.Interval = 10 * time.Second
	}
	if options.Timeout == 0 {
		options.Timeout = 3 * time.Second
	}
	if options.FailureThreshold == 0 {
		options.FailureThreshold = 2
	}
	if options.SuccessThreshold == 0 {
		options.SuccessThreshold = 1
	}
	if options.ProbeProtocol == 0 {
		options.ProbeProtocol = probe.DefaultProtocolVersion
	}
	if options.CircuitBreakerTTL == 0 {
		options.CircuitBreakerTTL = 30 * time.Second
	}
	if check == nil {
		check = ProbeCheck
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := &Checker{
		options: options,
		check:   check,
		logger:  logger,
		now:     time.Now,
		entries: make(map[string]*entry),
	}
	for _, target := range targets {
		if target.Backend.Addr == "" {
			continue
		}
		if target.ProbeHost == "" {
			target.ProbeHost = fallbackProbeHost(options.ProbeHost, target.Backend.Addr)
		}
		if _, ok := c.entries[target.Backend.Addr]; ok {
			continue
		}
		c.entries[target.Backend.Addr] = &entry{
			Target:  target,
			Healthy: true,
		}
	}
	return c
}

func ProbeCheck(ctx context.Context, target BackendTarget, options Options) error {
	_, err := probe.Run(ctx, probe.Options{
		Addr:            target.Backend.Addr,
		Host:            target.ProbeHost,
		Port:            25565,
		ProtocolVersion: options.ProbeProtocol,
		Timeout:         options.Timeout,
	})
	return err
}

func BuildTargets(routes []proxy.Route, def *proxy.Backend, probeHost string) []BackendTarget {
	targets := make([]BackendTarget, 0, len(routes)+1)
	seen := map[string]int{}
	add := func(backend proxy.Backend, host string) {
		if backend.Addr == "" {
			return
		}
		if idx, ok := seen[backend.Addr]; ok {
			if targets[idx].ProbeHost == "" && host != "" {
				targets[idx].ProbeHost = host
			}
			return
		}
		if host == "" {
			host = probeHost
		}
		seen[backend.Addr] = len(targets)
		targets = append(targets, BackendTarget{Backend: backend, ProbeHost: host})
	}
	for _, route := range routes {
		host := ""
		if len(route.Hosts) > 0 {
			host = proxy.NormalizeHost(route.Hosts[0])
		}
		add(route.Backend, host)
	}
	if def != nil {
		add(*def, probeHost)
	}
	return targets
}

func (c *Checker) Run(ctx context.Context) {
	if c == nil || !c.options.Enabled {
		return
	}
	c.CheckOnce(ctx)
	ticker := time.NewTicker(c.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.CheckOnce(ctx)
		}
	}
}

func (c *Checker) CheckOnce(parent context.Context) {
	if c == nil || !c.options.Enabled {
		return
	}
	targets := c.targets()
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(parent, c.options.Timeout)
		err := c.check(ctx, target, c.options)
		cancel()
		if err != nil {
			c.RecordFailure(target.Backend.Addr, err)
			c.logger.Debug("backend health check failed", "backend", target.Backend.Addr, "probe_host", target.ProbeHost, "err", err)
			continue
		}
		c.RecordSuccess(target.Backend.Addr)
		c.logger.Debug("backend health check succeeded", "backend", target.Backend.Addr, "probe_host", target.ProbeHost)
	}
}

func (c *Checker) Status(backend proxy.Backend) proxy.HealthStatus {
	status := c.BackendStatus(backend)
	return proxy.HealthStatus{Healthy: status.Healthy, CircuitOpen: status.CircuitOpen}
}

func (c *Checker) BackendStatus(backend proxy.Backend) BackendStatus {
	if c == nil {
		return BackendStatus{Backend: backend.Addr, Healthy: true}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[backend.Addr]
	if e == nil {
		return BackendStatus{Backend: backend.Addr, Healthy: true}
	}
	return c.statusLocked(e)
}

func (c *Checker) Unhealthy(backend proxy.Backend) bool {
	if c == nil || !c.options.Enabled {
		return false
	}
	status := c.Status(backend)
	return !status.Healthy || status.CircuitOpen
}

func (c *Checker) RecordFailure(backend string, err error) {
	if c == nil || !c.options.Enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[backend]
	if e == nil {
		e = &entry{Target: BackendTarget{Backend: proxy.Backend{Name: backend, Addr: backend}}, Healthy: true}
		c.entries[backend] = e
	}
	e.LastChecked = c.now()
	e.LastError = errString(err)
	e.ConsecutiveFails++
	e.ConsecutiveOKs = 0
	e.CheckFailures++
	if e.ConsecutiveFails >= c.options.FailureThreshold {
		e.Healthy = false
		e.CircuitOpenUntil = c.now().Add(c.options.CircuitBreakerTTL)
		e.CircuitTrips++
	}
}

func (c *Checker) RecordSuccess(backend string) {
	if c == nil || !c.options.Enabled {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[backend]
	if e == nil {
		e = &entry{Target: BackendTarget{Backend: proxy.Backend{Name: backend, Addr: backend}}, Healthy: true}
		c.entries[backend] = e
	}
	e.LastChecked = c.now()
	e.LastError = ""
	e.ConsecutiveOKs++
	e.ConsecutiveFails = 0
	e.CheckSuccesses++
	if e.ConsecutiveOKs >= c.options.SuccessThreshold {
		e.Healthy = true
		e.CircuitOpenUntil = time.Time{}
	}
}

func (c *Checker) Snapshot() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := Snapshot{Enabled: c.options.Enabled}
	for _, e := range c.entries {
		status := c.statusLocked(e)
		s.Backends = append(s.Backends, status)
		s.Total++
		if status.Healthy && !status.CircuitOpen {
			s.Healthy++
		} else {
			s.Unhealthy++
		}
	}
	sort.Slice(s.Backends, func(i, j int) bool {
		return s.Backends[i].Backend < s.Backends[j].Backend
	})
	return s
}

func (c *Checker) MetricsSnapshot() metrics.HealthSnapshot {
	snapshot := c.Snapshot()
	out := metrics.HealthSnapshot{
		Enabled:   snapshot.Enabled,
		Total:     snapshot.Total,
		Healthy:   snapshot.Healthy,
		Unhealthy: snapshot.Unhealthy,
	}
	for _, backend := range snapshot.Backends {
		out.Backends = append(out.Backends, metrics.HealthBackend{
			Backend:        backend.Backend,
			Healthy:        backend.Healthy,
			CircuitOpen:    backend.CircuitOpen,
			CheckSuccesses: backend.CheckSuccesses,
			CheckFailures:  backend.CheckFailures,
			CircuitTrips:   backend.CircuitTrips,
		})
	}
	return out
}

func (c *Checker) targets() []BackendTarget {
	c.mu.Lock()
	defer c.mu.Unlock()
	targets := make([]BackendTarget, 0, len(c.entries))
	for _, e := range c.entries {
		targets = append(targets, e.Target)
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Backend.Addr < targets[j].Backend.Addr
	})
	return targets
}

func (c *Checker) statusLocked(e *entry) BackendStatus {
	now := c.now()
	circuitOpen := !e.CircuitOpenUntil.IsZero() && now.Before(e.CircuitOpenUntil)
	return BackendStatus{
		Backend:          e.Target.Backend.Addr,
		ProbeHost:        e.Target.ProbeHost,
		Healthy:          e.Healthy,
		CircuitOpen:      circuitOpen,
		CircuitOpenUntil: e.CircuitOpenUntil,
		LastChecked:      e.LastChecked,
		LastError:        e.LastError,
		ConsecutiveFails: e.ConsecutiveFails,
		ConsecutiveOKs:   e.ConsecutiveOKs,
		CheckSuccesses:   e.CheckSuccesses,
		CheckFailures:    e.CheckFailures,
		CircuitTrips:     e.CircuitTrips,
	}
}

func fallbackProbeHost(configured, backendAddr string) string {
	if configured != "" {
		return configured
	}
	host, _, err := net.SplitHostPort(backendAddr)
	if err != nil || host == "" {
		return backendAddr
	}
	return host
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
