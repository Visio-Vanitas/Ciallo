package fail2ban

import (
	"strings"
	"sync"
	"time"
)

type Options struct {
	Enabled         bool
	MaxFailures     int
	Window          time.Duration
	BanDuration     time.Duration
	EarlyDisconnect time.Duration
}

type Guard struct {
	mu      sync.Mutex
	now     func() time.Time
	options Options
	entries map[key]entry
}

type key struct {
	route string
	kind  string
	value string
}

type entry struct {
	failures    int
	windowStart time.Time
	bannedUntil time.Time
}

func New(options Options, now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	if options.MaxFailures <= 0 {
		options.MaxFailures = 5
	}
	if options.Window <= 0 {
		options.Window = time.Minute
	}
	if options.BanDuration <= 0 {
		options.BanDuration = 10 * time.Minute
	}
	if options.EarlyDisconnect <= 0 {
		options.EarlyDisconnect = 5 * time.Second
	}
	return &Guard{
		now:     now,
		options: options,
		entries: make(map[key]entry),
	}
}

func (g *Guard) Enabled() bool {
	return g != nil && g.options.Enabled
}

func (g *Guard) EarlyDisconnect() time.Duration {
	if g == nil {
		return 0
	}
	return g.options.EarlyDisconnect
}

func (g *Guard) IsBanned(route, kind, value string) bool {
	if !g.Enabled() || normalize(value) == "" {
		return false
	}
	k := key{route: route, kind: kind, value: normalize(value)}
	g.mu.Lock()
	defer g.mu.Unlock()
	item, ok := g.entries[k]
	if !ok {
		return false
	}
	if g.now().Before(item.bannedUntil) {
		return true
	}
	if !item.bannedUntil.IsZero() {
		delete(g.entries, k)
	}
	return false
}

func (g *Guard) RecordFailure(route, kind, value string) bool {
	if !g.Enabled() || normalize(value) == "" {
		return false
	}
	k := key{route: route, kind: kind, value: normalize(value)}
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	item := g.entries[k]
	if item.windowStart.IsZero() || now.Sub(item.windowStart) > g.options.Window {
		item.windowStart = now
		item.failures = 0
	}
	item.failures++
	if item.failures >= g.options.MaxFailures {
		item.bannedUntil = now.Add(g.options.BanDuration)
	}
	g.entries[k] = item
	return !item.bannedUntil.IsZero() && now.Before(item.bannedUntil)
}

func (g *Guard) RecordSuccess(route, kind, value string) {
	if !g.Enabled() || normalize(value) == "" {
		return
	}
	g.mu.Lock()
	delete(g.entries, key{route: route, kind: kind, value: normalize(value)})
	g.mu.Unlock()
}

func (g *Guard) Unban(route, kind, value string) bool {
	if g == nil || normalize(value) == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	k := key{route: route, kind: kind, value: normalize(value)}
	_, ok := g.entries[k]
	delete(g.entries, k)
	return ok
}

type Ban struct {
	Route       string
	Kind        string
	Value       string
	Failures    int
	BannedUntil time.Time
}

func (g *Guard) Snapshot() []Ban {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	out := make([]Ban, 0, len(g.entries))
	for k, item := range g.entries {
		if item.bannedUntil.IsZero() || !now.Before(item.bannedUntil) {
			continue
		}
		out = append(out, Ban{
			Route:       k.route,
			Kind:        k.kind,
			Value:       k.value,
			Failures:    item.failures,
			BannedUntil: item.bannedUntil,
		})
	}
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
