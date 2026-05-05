package metrics

import (
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Recorder struct {
	mu sync.Mutex

	activeConnections int64
	statusTotal       map[statusKey]int64
	loginTotal        map[loginKey]int64
	backendDialErrors map[backendKey]int64
	fail2banBlocks    map[fail2banKey]int64
	statusBreakers    map[string]int64
	health            HealthSource
}

type statusKey struct {
	Route        string
	Backend      string
	CacheResult  string
	FallbackUsed string
}

type loginKey struct {
	Route          string
	Backend        string
	Fail2BanAction string
}

type backendKey struct {
	Route   string
	Backend string
}

type fail2banKey struct {
	Route string
	Kind  string
}

type HealthSource interface {
	MetricsSnapshot() HealthSnapshot
}

type HealthSnapshot struct {
	Enabled   bool
	Total     int
	Healthy   int
	Unhealthy int
	Backends  []HealthBackend
}

type HealthBackend struct {
	Backend        string
	Healthy        bool
	CircuitOpen    bool
	CheckSuccesses int64
	CheckFailures  int64
	CircuitTrips   int64
}

func New() *Recorder {
	return &Recorder{
		statusTotal:       make(map[statusKey]int64),
		loginTotal:        make(map[loginKey]int64),
		backendDialErrors: make(map[backendKey]int64),
		fail2banBlocks:    make(map[fail2banKey]int64),
		statusBreakers:    make(map[string]int64),
	}
}

func (r *Recorder) SetHealthSource(source HealthSource) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.health = source
	r.mu.Unlock()
}

func (r *Recorder) IncActiveConnections() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.activeConnections++
	r.mu.Unlock()
}

func (r *Recorder) DecActiveConnections() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.activeConnections > 0 {
		r.activeConnections--
	}
	r.mu.Unlock()
}

func (r *Recorder) RecordStatus(route, backend, cacheResult string, fallbackUsed bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.statusTotal[statusKey{
		Route:        route,
		Backend:      backend,
		CacheResult:  valueOrUnknown(cacheResult),
		FallbackUsed: strconv.FormatBool(fallbackUsed),
	}]++
	r.mu.Unlock()
}

func (r *Recorder) RecordLogin(route, backend, fail2banAction string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.loginTotal[loginKey{
		Route:          route,
		Backend:        backend,
		Fail2BanAction: valueOrUnknown(fail2banAction),
	}]++
	r.mu.Unlock()
}

func (r *Recorder) RecordBackendDialError(route, backend string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.backendDialErrors[backendKey{Route: route, Backend: backend}]++
	r.mu.Unlock()
}

func (r *Recorder) RecordFail2BanBlock(route, kind string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.fail2banBlocks[fail2banKey{Route: route, Kind: valueOrUnknown(kind)}]++
	r.mu.Unlock()
}

func (r *Recorder) RecordStatusCircuitBreaker(backend string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.statusBreakers[valueOrUnknown(backend)]++
	r.mu.Unlock()
}

func (r *Recorder) WritePrometheus(w io.Writer) error {
	if r == nil {
		r = New()
	}
	snapshot := r.snapshot()
	lines := []string{
		"# HELP ciallo_active_connections Current active client connections.",
		"# TYPE ciallo_active_connections gauge",
		fmt.Sprintf("ciallo_active_connections %d", snapshot.ActiveConnections),
		"# HELP ciallo_status_requests_total Total status requests handled by the proxy.",
		"# TYPE ciallo_status_requests_total counter",
	}
	for _, sample := range snapshot.Status {
		lines = append(lines, fmt.Sprintf(
			"ciallo_status_requests_total{route=%q,backend=%q,cache_result=%q,fallback_used=%q} %d",
			sanitizeLabelValue(sample.Route),
			sanitizeLabelValue(sample.Backend),
			sanitizeLabelValue(sample.CacheResult),
			sanitizeLabelValue(sample.FallbackUsed),
			sample.Value,
		))
	}
	lines = append(lines,
		"# HELP ciallo_login_requests_total Total login connections handled by the proxy.",
		"# TYPE ciallo_login_requests_total counter",
	)
	for _, sample := range snapshot.Login {
		lines = append(lines, fmt.Sprintf(
			"ciallo_login_requests_total{route=%q,backend=%q,fail2ban_action=%q} %d",
			sanitizeLabelValue(sample.Route),
			sanitizeLabelValue(sample.Backend),
			sanitizeLabelValue(sample.Fail2BanAction),
			sample.Value,
		))
	}
	lines = append(lines,
		"# HELP ciallo_backend_dial_errors_total Total backend dial failures.",
		"# TYPE ciallo_backend_dial_errors_total counter",
	)
	for _, sample := range snapshot.BackendDialErrors {
		lines = append(lines, fmt.Sprintf(
			"ciallo_backend_dial_errors_total{route=%q,backend=%q} %d",
			sanitizeLabelValue(sample.Route),
			sanitizeLabelValue(sample.Backend),
			sample.Value,
		))
	}
	lines = append(lines,
		"# HELP ciallo_fail2ban_blocks_total Total fail2ban blocks.",
		"# TYPE ciallo_fail2ban_blocks_total counter",
	)
	for _, sample := range snapshot.Fail2BanBlocks {
		lines = append(lines, fmt.Sprintf(
			"ciallo_fail2ban_blocks_total{route=%q,kind=%q} %d",
			sanitizeLabelValue(sample.Route),
			sanitizeLabelValue(sample.Kind),
			sample.Value,
		))
	}
	lines = append(lines,
		"# HELP ciallo_status_circuit_breaker_total Total status requests short-circuited because a backend is unhealthy.",
		"# TYPE ciallo_status_circuit_breaker_total counter",
	)
	for _, sample := range snapshot.StatusBreakers {
		lines = append(lines, fmt.Sprintf(
			"ciallo_status_circuit_breaker_total{backend=%q} %d",
			sanitizeLabelValue(sample.Backend),
			sample.Value,
		))
	}
	lines = append(lines,
		"# HELP ciallo_backend_health Backend health state, 1 for current state.",
		"# TYPE ciallo_backend_health gauge",
	)
	for _, sample := range snapshot.Health.Backends {
		state := "healthy"
		if !sample.Healthy || sample.CircuitOpen {
			state = "unhealthy"
		}
		lines = append(lines, fmt.Sprintf(
			"ciallo_backend_health{backend=%q,state=%q} 1",
			sanitizeLabelValue(sample.Backend),
			state,
		))
	}
	lines = append(lines,
		"# HELP ciallo_backend_health_checks_total Total backend health checks.",
		"# TYPE ciallo_backend_health_checks_total counter",
	)
	for _, sample := range snapshot.Health.Backends {
		lines = append(lines, fmt.Sprintf(
			"ciallo_backend_health_checks_total{backend=%q,result=%q} %d",
			sanitizeLabelValue(sample.Backend),
			"success",
			sample.CheckSuccesses,
		))
		lines = append(lines, fmt.Sprintf(
			"ciallo_backend_health_checks_total{backend=%q,result=%q} %d",
			sanitizeLabelValue(sample.Backend),
			"failure",
			sample.CheckFailures,
		))
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}

type Snapshot struct {
	ActiveConnections int64
	Status            []StatusSample
	Login             []LoginSample
	BackendDialErrors []BackendSample
	Fail2BanBlocks    []Fail2BanSample
	StatusBreakers    []BreakerSample
	Health            HealthSnapshot
}

type StatusSample struct {
	Route        string
	Backend      string
	CacheResult  string
	FallbackUsed string
	Value        int64
}

type LoginSample struct {
	Route          string
	Backend        string
	Fail2BanAction string
	Value          int64
}

type BackendSample struct {
	Route   string
	Backend string
	Value   int64
}

type Fail2BanSample struct {
	Route string
	Kind  string
	Value int64
}

type BreakerSample struct {
	Backend string
	Value   int64
}

func (r *Recorder) snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Snapshot{ActiveConnections: r.activeConnections}
	if r.health != nil {
		s.Health = r.health.MetricsSnapshot()
	}
	for key, value := range r.statusTotal {
		s.Status = append(s.Status, StatusSample{
			Route:        key.Route,
			Backend:      key.Backend,
			CacheResult:  key.CacheResult,
			FallbackUsed: key.FallbackUsed,
			Value:        value,
		})
	}
	sort.Slice(s.Status, func(i, j int) bool {
		return s.Status[i].Route+s.Status[i].Backend+s.Status[i].CacheResult+s.Status[i].FallbackUsed <
			s.Status[j].Route+s.Status[j].Backend+s.Status[j].CacheResult+s.Status[j].FallbackUsed
	})
	for key, value := range r.loginTotal {
		s.Login = append(s.Login, LoginSample{
			Route:          key.Route,
			Backend:        key.Backend,
			Fail2BanAction: key.Fail2BanAction,
			Value:          value,
		})
	}
	sort.Slice(s.Login, func(i, j int) bool {
		return s.Login[i].Route+s.Login[i].Backend+s.Login[i].Fail2BanAction <
			s.Login[j].Route+s.Login[j].Backend+s.Login[j].Fail2BanAction
	})
	for key, value := range r.backendDialErrors {
		s.BackendDialErrors = append(s.BackendDialErrors, BackendSample{
			Route:   key.Route,
			Backend: key.Backend,
			Value:   value,
		})
	}
	sort.Slice(s.BackendDialErrors, func(i, j int) bool {
		return s.BackendDialErrors[i].Route+s.BackendDialErrors[i].Backend <
			s.BackendDialErrors[j].Route+s.BackendDialErrors[j].Backend
	})
	for key, value := range r.fail2banBlocks {
		s.Fail2BanBlocks = append(s.Fail2BanBlocks, Fail2BanSample{
			Route: key.Route,
			Kind:  key.Kind,
			Value: value,
		})
	}
	sort.Slice(s.Fail2BanBlocks, func(i, j int) bool {
		return s.Fail2BanBlocks[i].Route+s.Fail2BanBlocks[i].Kind <
			s.Fail2BanBlocks[j].Route+s.Fail2BanBlocks[j].Kind
	})
	for backend, value := range r.statusBreakers {
		s.StatusBreakers = append(s.StatusBreakers, BreakerSample{Backend: backend, Value: value})
	}
	sort.Slice(s.StatusBreakers, func(i, j int) bool {
		return s.StatusBreakers[i].Backend < s.StatusBreakers[j].Backend
	})
	return s
}

var labelCleaner = regexp.MustCompile(`[^A-Za-z0-9_.:/@-]+`)

func sanitizeLabelValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	value = labelCleaner.ReplaceAllString(value, "_")
	if len(value) > 160 {
		value = value[:160]
	}
	return value
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
