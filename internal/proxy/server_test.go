package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"ciallo/internal/cache"
	"ciallo/internal/fail2ban"
	"ciallo/internal/mcproto"
	"ciallo/internal/metrics"
	"ciallo/internal/pool"
)

type stubConn struct{}

func (c *stubConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *stubConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *stubConn) Close() error                     { return nil }
func (c *stubConn) LocalAddr() net.Addr              { return stubAddr("local") }
func (c *stubConn) RemoteAddr() net.Addr             { return stubAddr("remote") }
func (c *stubConn) SetDeadline(time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }

type stubAddr string

func (a stubAddr) Network() string { return "stub" }
func (a stubAddr) String() string  { return string(a) }

func TestLoginRoutesByHostAndReplaysHandshake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backendA := startLoginBackend(t, []byte("A"))
	backendB := startLoginBackend(t, []byte("B"))
	server, addr := startProxy(t, ctx, []Route{
		{Hosts: []string{"a.example.com"}, Backend: Backend{Name: "a", Addr: backendA.addr}},
		{Hosts: []string{"b.example.com"}, Backend: Backend{Name: "b", Addr: backendB.addr}},
	}, nil)
	defer server.Shutdown(context.Background())

	gotA := dialLogin(t, addr, "a.example.com", "Steve", []byte("hello-a"))
	if !bytes.Equal(gotA, []byte("A")) {
		t.Fatalf("route A got %q", gotA)
	}
	gotB := dialLogin(t, addr, "b.example.com", "Alex", []byte("hello-b"))
	if !bytes.Equal(gotB, []byte("B")) {
		t.Fatalf("route B got %q", gotB)
	}

	if !bytes.Equal(<-backendA.handshakes, mcproto.BuildHandshake(765, "a.example.com", 25565, mcproto.NextStateLogin).Raw) {
		t.Fatal("backend A did not receive original handshake")
	}
	if !bytes.Equal(<-backendB.handshakes, mcproto.BuildHandshake(765, "b.example.com", 25565, mcproto.NextStateLogin).Raw) {
		t.Fatal("backend B did not receive original handshake")
	}
	if !bytes.Equal(<-backendA.loginStarts, mcproto.BuildLoginStart("Steve", nil).Raw) {
		t.Fatal("backend A did not receive original login start")
	}
	if !bytes.Equal(<-backendB.loginStarts, mcproto.BuildLoginStart("Alex", nil).Raw) {
		t.Fatal("backend B did not receive original login start")
	}
}

func TestStatusCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := startStatusBackend(t, `{"version":{"name":"test","protocol":765},"players":{"max":20,"online":1},"description":{"text":"cached"}}`)
	server, addr := startProxy(t, ctx, []Route{
		{Hosts: []string{"status.example.com"}, Backend: Backend{Name: "status", Addr: backend.addr}},
	}, nil)
	defer server.Shutdown(context.Background())

	first := dialStatus(t, addr, "status.example.com", 10)
	second := dialStatus(t, addr, "status.example.com", 20)

	if first.statusJSON != second.statusJSON {
		t.Fatalf("cached status mismatch: %q vs %q", first.statusJSON, second.statusJSON)
	}
	if first.pongValue != 10 {
		t.Fatalf("first pong = %d", first.pongValue)
	}
	if second.pongValue != 20 {
		t.Fatalf("second cached pong = %d", second.pongValue)
	}
	if got := backend.count(); got != 1 {
		t.Fatalf("backend status count = %d, want 1", got)
	}
}

func TestStatusAccessLogReportsCacheResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	backend := startStatusBackend(t, `{"version":{"name":"test","protocol":765},"players":{"max":20,"online":1},"description":{"text":"cached"}}`)
	server, addr := startProxyWithLogger(t, ctx, []Route{
		{Hosts: []string{"status.example.com"}, Backend: Backend{Name: "status", Addr: backend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), nil, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Minute,
		MOTDCacheEnabled:   true,
		MOTDFallbackTTL:    time.Minute,
	}, logger)
	defer server.Shutdown(context.Background())

	_ = dialStatus(t, addr, "status.example.com", 10)
	_ = dialStatus(t, addr, "status.example.com", 20)

	waitForLog(t, &logs, "event=status", "cache_result=miss", "cache_result=hit", "pong_handled=true")
}

func TestRouteCanDisableStatusCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	disabled := false
	backend := startStatusBackend(t, `{"version":{"name":"test","protocol":765},"players":{"max":20,"online":1},"description":{"text":"uncached"}}`)
	server, addr := startProxy(t, ctx, []Route{
		{Hosts: []string{"status.example.com"}, Backend: Backend{Name: "status", Addr: backend.addr, StatusCache: &disabled}},
	}, nil)
	defer server.Shutdown(context.Background())

	_ = dialStatus(t, addr, "status.example.com", 10)
	_ = dialStatus(t, addr, "status.example.com", 20)

	if got := backend.count(); got != 2 {
		t.Fatalf("backend status count = %d, want 2", got)
	}
}

func TestMetricsRecordsStatusAndLoginEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := metrics.New()
	statusBackend := startStatusBackend(t, `{"version":{"name":"test","protocol":765},"players":{"max":20,"online":1},"description":{"text":"cached"}}`)
	server, statusAddr := startProxyWithOptions(t, ctx, []Route{
		{Hosts: []string{"metrics-status.example.com"}, Backend: Backend{Name: "status", Addr: statusBackend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), nil, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Minute,
		MOTDCacheEnabled:   true,
		MOTDFallbackTTL:    time.Minute,
		Metrics:            rec,
	})
	defer server.Shutdown(context.Background())
	_ = dialStatus(t, statusAddr, "metrics-status.example.com", 10)

	backend := startEarlyCloseBackend(t)
	guard := fail2ban.New(fail2ban.Options{
		Enabled:         true,
		MaxFailures:     1,
		Window:          time.Minute,
		BanDuration:     time.Minute,
		EarlyDisconnect: time.Minute,
	}, time.Now)
	loginServer, loginAddr := startProxyWithOptions(t, ctx, []Route{
		{Hosts: []string{"metrics-login.example.com"}, Backend: Backend{Name: "login", Addr: backend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), guard, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Second,
		Metrics:            rec,
	})
	defer loginServer.Shutdown(context.Background())
	_ = dialLoginAllowEOF(t, loginAddr, "metrics-login.example.com", "Alex", []byte("payload"))
	waitForBan(t, guard, "metrics-login.example.com", "ip", "127.0.0.1")

	waitForMetrics(t, rec,
		`ciallo_status_requests_total{route="metrics-status.example.com"`,
		`cache_result="miss"`,
		`ciallo_login_requests_total{route="metrics-login.example.com"`,
		`fail2ban_action="record_failure"`,
	)
}

func TestStatusUsesPoolButLoginDialsFresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := pool.New(pool.Options{MaxIdlePerBackend: 1, IdleTimeout: time.Minute})
	dialer := NewNetDialer(time.Second, p)
	backend := Backend{Name: "mixed", Addr: "127.0.0.1:0"}

	warmConn := &stubConn{}
	p.Put(backend.Addr, warmConn)

	statusConn, err := dialer.DialStatus(ctx, backend)
	if err != nil {
		t.Fatal(err)
	}
	if statusConn != warmConn {
		t.Fatal("DialStatus did not return pooled connection")
	}

	p.Put(backend.Addr, &stubConn{})
	if _, err := dialer.Dial(ctx, backend); err == nil {
		t.Fatal("Dial should ignore pooled connections and attempt a fresh login connection")
	}
}

func TestStatusMOTDFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	now := time.Unix(100, 0)
	statusCache := cache.NewStatusCache(func() time.Time { return now })
	status := mcproto.BuildStatusResponse(`{"version":{"name":"test","protocol":765},"players":{"max":20,"online":1},"description":{"text":"fallback motd"}}`)
	statusCache.SetWithFallback("127.0.0.1:1|status.example.com|765", status.Raw, time.Second, time.Minute)
	now = now.Add(2 * time.Second)

	server, addr := startProxyWithOptions(t, ctx, []Route{
		{Hosts: []string{"status.example.com"}, Backend: Backend{Name: "127.0.0.1:1", Addr: "127.0.0.1:1"}},
	}, nil, NewNetDialer(10*time.Millisecond, nil), statusCache, nil, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: 10 * time.Millisecond,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Second,
		MOTDCacheEnabled:   true,
		MOTDFallbackTTL:    time.Minute,
	})
	defer server.Shutdown(context.Background())

	result := dialStatus(t, addr, "status.example.com", 42)
	if !strings.Contains(result.statusJSON, "fallback motd") {
		t.Fatalf("fallback response missing motd: %s", result.statusJSON)
	}
	if result.pongValue != 42 {
		t.Fatalf("pong = %d", result.pongValue)
	}
}

func TestFail2BanBansIPAfterEarlyDisconnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := startEarlyCloseBackend(t)
	guard := fail2ban.New(fail2ban.Options{
		Enabled:         true,
		MaxFailures:     1,
		Window:          time.Minute,
		BanDuration:     time.Minute,
		EarlyDisconnect: time.Minute,
	}, time.Now)
	server, addr := startProxyWithOptions(t, ctx, []Route{
		{Hosts: []string{"ban.example.com"}, Backend: Backend{Name: "ban", Addr: backend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), guard, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Second,
	})
	defer server.Shutdown(context.Background())

	_ = dialLoginAllowEOF(t, addr, "ban.example.com", "Steve", []byte("payload"))
	if got := backend.count(); got != 1 {
		t.Fatalf("backend count = %d", got)
	}
	waitForBan(t, guard, "ban.example.com", "ip", "127.0.0.1")
	_ = dialLoginAllowEOF(t, addr, "ban.example.com", "Steve", []byte("payload"))
	if got := backend.count(); got != 1 {
		t.Fatalf("banned second login should not reach backend, count = %d", got)
	}
	if !guard.Unban("ban.example.com", "ip", "127.0.0.1") {
		t.Fatal("expected management unban hook to remove ip ban")
	}
}

func TestFail2BanRecordsBackendReplayFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := startEarlyCloseBackend(t)
	guard := fail2ban.New(fail2ban.Options{
		Enabled:         true,
		MaxFailures:     1,
		Window:          time.Minute,
		BanDuration:     time.Minute,
		EarlyDisconnect: time.Minute,
	}, time.Now)
	server, addr := startProxyWithOptions(t, ctx, []Route{
		{Hosts: []string{"replay.example.com"}, Backend: Backend{Name: "replay", Addr: backend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), guard, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Second,
	})
	defer server.Shutdown(context.Background())

	_ = dialLoginAllowEOF(t, addr, "replay.example.com", "Alex", []byte("payload"))
	waitForBan(t, guard, "replay.example.com", "ip", "127.0.0.1")
	waitForBan(t, guard, "replay.example.com", "player", "Alex")
}

func TestLoginAccessLogReportsFail2BanAction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	backend := startEarlyCloseBackend(t)
	guard := fail2ban.New(fail2ban.Options{
		Enabled:         true,
		MaxFailures:     1,
		Window:          time.Minute,
		BanDuration:     time.Minute,
		EarlyDisconnect: time.Minute,
	}, time.Now)
	server, addr := startProxyWithLogger(t, ctx, []Route{
		{Hosts: []string{"ban.example.com"}, Backend: Backend{Name: "ban", Addr: backend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), guard, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Second,
	}, logger)
	defer server.Shutdown(context.Background())

	_ = dialLoginAllowEOF(t, addr, "ban.example.com", "Steve", []byte("payload"))
	waitForBan(t, guard, "ban.example.com", "ip", "127.0.0.1")

	waitForLog(t, &logs, "event=login", "username=Steve", "fail2ban_action=record_failure")
}

func TestFail2BanIsRouteScopedByHost(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	backend := startEarlyCloseBackend(t)
	guard := fail2ban.New(fail2ban.Options{
		Enabled:         true,
		MaxFailures:     1,
		Window:          time.Minute,
		BanDuration:     time.Minute,
		EarlyDisconnect: time.Minute,
	}, time.Now)
	server, addr := startProxyWithOptions(t, ctx, []Route{
		{Hosts: []string{"a.example.com", "b.example.com"}, Backend: Backend{Name: "shared", Addr: backend.addr}},
	}, nil, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), guard, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Second,
	})
	defer server.Shutdown(context.Background())

	_ = dialLoginAllowEOF(t, addr, "a.example.com", "Steve", []byte("payload"))
	waitForBan(t, guard, "a.example.com", "ip", "127.0.0.1")
	_ = dialLoginAllowEOF(t, addr, "b.example.com", "Steve", []byte("payload"))
	if got := backend.count(); got != 2 {
		t.Fatalf("b.example.com should not be banned by a.example.com, backend count = %d", got)
	}
}

func startProxy(t *testing.T, ctx context.Context, routes []Route, def *Backend) (*Server, string) {
	t.Helper()
	return startProxyWithOptions(t, ctx, routes, def, NewNetDialer(time.Second, nil), cache.NewStatusCache(time.Now), nil, Options{
		HandshakeTimeout:   time.Second,
		BackendDialTimeout: time.Second,
		IdleTimeout:        time.Second,
		MaxHandshakeSize:   64 * 1024,
		StatusCacheEnabled: true,
		StatusCacheTTL:     time.Minute,
		MOTDCacheEnabled:   true,
		MOTDFallbackTTL:    time.Minute,
	})
}

func startProxyWithOptions(t *testing.T, ctx context.Context, routes []Route, def *Backend, dialer Dialer, statusCache StatusCache, guard FailGuard, options Options) (*Server, string) {
	t.Helper()
	return startProxyWithLogger(t, ctx, routes, def, dialer, statusCache, guard, options, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func startProxyWithLogger(t *testing.T, ctx context.Context, routes []Route, def *Backend, dialer Dialer, statusCache StatusCache, guard FailGuard, options Options, logger *slog.Logger) (*Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	router := NewStaticRouter(routes, def)
	options.ListenAddr = "127.0.0.1:0"
	server := NewServerWithGuard(options, router, dialer, statusCache, guard, logger)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx, ln)
	}()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("proxy serve: %v", err)
			}
		case <-cancelCtx.Done():
			t.Fatal("proxy did not shut down")
		}
	})
	return server, ln.Addr().String()
}

type loginBackend struct {
	addr        string
	handshakes  chan []byte
	loginStarts chan []byte
}

func startLoginBackend(t *testing.T, response []byte) loginBackend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	out := loginBackend{
		addr:        ln.Addr().String(),
		handshakes:  make(chan []byte, 8),
		loginStarts: make(chan []byte, 8),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				packet, err := mcproto.ReadPacket(c, 64*1024)
				if err != nil {
					return
				}
				out.handshakes <- packet.Raw
				loginStart, err := mcproto.ReadPacket(c, 64*1024)
				if err != nil {
					return
				}
				out.loginStarts <- loginStart.Raw
				buf := make([]byte, 7)
				_, _ = io.ReadFull(c, buf)
				_, _ = c.Write(response)
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return out
}

func dialLogin(t *testing.T, addr, host, username string, payload []byte) []byte {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	handshake := mcproto.BuildHandshake(765, host, 25565, mcproto.NextStateLogin)
	if _, err := conn.Write(handshake.Raw); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(mcproto.BuildLoginStart(username, nil).Raw); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	return buf
}

func dialLoginAllowEOF(t *testing.T, addr, host, username string, payload []byte) error {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	handshake := mcproto.BuildHandshake(765, host, 25565, mcproto.NextStateLogin)
	if _, err := conn.Write(handshake.Raw); err != nil {
		return err
	}
	if _, err := conn.Write(mcproto.BuildLoginStart(username, nil).Raw); err != nil {
		return err
	}
	if _, err := conn.Write(payload); err != nil {
		return err
	}
	buf := make([]byte, 1)
	_, err = conn.Read(buf)
	return err
}

type earlyCloseBackend struct {
	addr string
	hits chan struct{}
}

func startEarlyCloseBackend(t *testing.T) earlyCloseBackend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	out := earlyCloseBackend{
		addr: ln.Addr().String(),
		hits: make(chan struct{}, 16),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			out.hits <- struct{}{}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return out
}

func (b earlyCloseBackend) count() int {
	return len(b.hits)
}

func waitForBan(t *testing.T, guard *fail2ban.Guard, route, kind, value string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if guard.IsBanned(route, kind, value) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for ban route=%s kind=%s value=%s", route, kind, value)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForLog(t *testing.T, logs *bytes.Buffer, parts ...string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		text := logs.String()
		missing := ""
		for _, part := range parts {
			if !strings.Contains(text, part) {
				missing = part
				break
			}
		}
		if missing == "" {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("log missing %q:\n%s", missing, text)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForMetrics(t *testing.T, rec *metrics.Recorder, parts ...string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		var out strings.Builder
		if err := rec.WritePrometheus(&out); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		missing := ""
		for _, part := range parts {
			if !strings.Contains(text, part) {
				missing = part
				break
			}
		}
		if missing == "" {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("metrics missing %q:\n%s", missing, text)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type statusBackend struct {
	addr string
	hits chan struct{}
}

func startStatusBackend(t *testing.T, statusJSON string) statusBackend {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	out := statusBackend{
		addr: ln.Addr().String(),
		hits: make(chan struct{}, 16),
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if _, err := mcproto.ReadPacket(c, 64*1024); err != nil {
					return
				}
				if _, err := mcproto.ReadPacket(c, 64*1024); err != nil {
					return
				}
				out.hits <- struct{}{}
				response := mcproto.BuildStatusResponse(statusJSON)
				if _, err := c.Write(response.Raw); err != nil {
					return
				}
				ping, err := mcproto.ReadPacket(c, 64*1024)
				if err != nil {
					return
				}
				pong, err := mcproto.BuildPong(ping.Data)
				if err != nil {
					return
				}
				_, _ = c.Write(pong.Raw)
			}(conn)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return out
}

func (b statusBackend) count() int {
	count := len(b.hits)
	return count
}

type statusResult struct {
	statusJSON string
	pongValue  int64
}

func dialStatus(t *testing.T, addr, host string, pingValue int64) statusResult {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	handshake := mcproto.BuildHandshake(765, host, 25565, mcproto.NextStateStatus)
	if _, err := conn.Write(handshake.Raw); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(mcproto.NewPacket(mcproto.StatusRequestPacketID, nil).Raw); err != nil {
		t.Fatal(err)
	}
	response, err := mcproto.ReadPacket(conn, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	statusJSON, err := mcproto.ParseStatusJSON(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(mcproto.BuildPing(pingValue).Raw); err != nil {
		t.Fatal(err)
	}
	pong, err := mcproto.ReadPacket(conn, 64*1024)
	if err != nil {
		t.Fatal(err)
	}
	if pong.ID != mcproto.StatusPongPacketID || len(pong.Data) != 8 {
		t.Fatalf("bad pong: id=%d len=%d", pong.ID, len(pong.Data))
	}
	value := int64(uint64(pong.Data[0])<<56 |
		uint64(pong.Data[1])<<48 |
		uint64(pong.Data[2])<<40 |
		uint64(pong.Data[3])<<32 |
		uint64(pong.Data[4])<<24 |
		uint64(pong.Data[5])<<16 |
		uint64(pong.Data[6])<<8 |
		uint64(pong.Data[7]))
	return statusResult{
		statusJSON: statusJSON,
		pongValue:  value,
	}
}
