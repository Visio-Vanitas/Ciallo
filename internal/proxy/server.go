package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"ciallo/internal/mcproto"
)

type Server struct {
	options Options
	router  Router
	dialer  Dialer
	cache   StatusCache
	guard   FailGuard
	logger  *slog.Logger

	mu sync.Mutex
	ln net.Listener
}

type StatusResult struct {
	CacheResult  string
	FallbackUsed bool
	PongHandled  bool
}

type LoginResult struct {
	Stats          PipeStats
	Username       string
	Fail2BanAction string
}

func NewServer(options Options, router Router, dialer Dialer, cache StatusCache, logger *slog.Logger) *Server {
	return NewServerWithGuard(options, router, dialer, cache, nil, logger)
}

func NewServerWithGuard(options Options, router Router, dialer Dialer, cache StatusCache, guard FailGuard, logger *slog.Logger) *Server {
	if options.HandshakeTimeout == 0 {
		options.HandshakeTimeout = 3 * time.Second
	}
	if options.BackendDialTimeout == 0 {
		options.BackendDialTimeout = 3 * time.Second
	}
	if options.IdleTimeout == 0 {
		options.IdleTimeout = 10 * time.Minute
	}
	if options.ShutdownTimeout == 0 {
		options.ShutdownTimeout = 10 * time.Second
	}
	if options.MaxHandshakeSize == 0 {
		options.MaxHandshakeSize = 64 * 1024
	}
	if options.StatusCacheTTL == 0 {
		options.StatusCacheTTL = 5 * time.Second
	}
	if options.MOTDFallbackTTL == 0 {
		options.MOTDFallbackTTL = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		options: options,
		router:  router,
		dialer:  dialer,
		cache:   cache,
		guard:   guard,
		logger:  logger,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.options.ListenAddr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, ln)
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = s.Shutdown(context.Background())
	}()

	s.logger.Info("listening", "addr", ln.Addr().String())
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ln != nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if ln == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- ln.Close()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) handleConn(parent context.Context, client net.Conn) {
	start := time.Now()
	remoteAddr := client.RemoteAddr().String()
	logger := s.logger.With("remote_addr", remoteAddr)
	defer client.Close()
	if s.options.Metrics != nil {
		s.options.Metrics.IncActiveConnections()
		defer s.options.Metrics.DecActiveConnections()
	}

	if tcp, ok := client.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
	}

	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	handshakePacket, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		logger.Debug("bad handshake", "err", err)
		return
	}
	handshake, err := mcproto.ParseHandshake(handshakePacket)
	if err != nil {
		logger.Debug("parse handshake failed", "err", err)
		return
	}
	_ = client.SetReadDeadline(time.Time{})

	host := NormalizeHost(handshake.ServerAddress)
	backend, ok := s.router.Resolve(host)
	if !ok {
		logger.Warn("route not found", "host", host, "protocol_version", handshake.ProtocolVersion)
		return
	}

	logger = logger.With(
		"host", host,
		"backend", backend.Addr,
		"protocol_version", handshake.ProtocolVersion,
		"state", int32(handshake.NextState),
	)

	switch handshake.NextState {
	case mcproto.NextStateStatus:
		result, err := s.handleStatus(parent, client, backend, handshake, handshakePacket, logger)
		if s.options.Metrics != nil {
			s.options.Metrics.RecordStatus(host, backend.Addr, result.CacheResult, result.FallbackUsed)
		}
		logger.Info("access",
			"event", "status",
			"duration_ms", time.Since(start).Milliseconds(),
			"cache_result", result.CacheResult,
			"fallback_used", result.FallbackUsed,
			"pong_handled", result.PongHandled,
			"err", errString(err),
		)
		if err != nil {
			logger.Debug("status failed", "err", err, "duration_ms", time.Since(start).Milliseconds())
		}
	case mcproto.NextStateLogin:
		result, err := s.handleLogin(parent, client, backend, host, handshake, handshakePacket, remoteIP(client))
		if s.options.Metrics != nil {
			s.options.Metrics.RecordLogin(host, backend.Addr, result.Fail2BanAction)
			if result.Fail2BanAction == "ip_banned" {
				s.options.Metrics.RecordFail2BanBlock(host, "ip")
			}
			if result.Fail2BanAction == "player_banned" {
				s.options.Metrics.RecordFail2BanBlock(host, "player")
			}
		}
		logger.Info("access",
			"event", "login",
			"username", result.Username,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes_client_to_backend", result.Stats.ClientToBackend,
			"bytes_backend_to_client", result.Stats.BackendToClient,
			"fail2ban_action", result.Fail2BanAction,
			"err", errString(err),
		)
		if err != nil {
			logger.Debug("login proxy failed", "err", err, "duration_ms", time.Since(start).Milliseconds())
			return
		}
	default:
		logger.Debug("unsupported next state")
	}
}

func (s *Server) handleLogin(parent context.Context, client net.Conn, backend Backend, route string, handshake mcproto.Handshake, handshakePacket mcproto.Packet, ip string) (LoginResult, error) {
	result := LoginResult{Fail2BanAction: "none"}
	if route == "" {
		route = routeKey(backend)
	}
	if s.isBanned(route, "ip", ip) {
		result.Fail2BanAction = "ip_banned"
		return result, fmt.Errorf("fail2ban ip banned")
	}

	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	loginStart, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return result, fmt.Errorf("read login start: %w", err)
	}
	_ = client.SetReadDeadline(time.Time{})

	login, parseErr := mcproto.ParseLoginStart(loginStart, handshake.ProtocolVersion)
	username := login.Username
	result.Username = username
	if parseErr != nil {
		s.logger.Debug("parse login start failed", "err", parseErr, "route", route, "ip", ip)
	}
	if username != "" && s.isBanned(route, "player", username) {
		_ = mcproto.WritePacket(client, mcproto.BuildLoginDisconnect(`{"text":"Temporarily banned by proxy"}`))
		result.Fail2BanAction = "player_banned"
		return result, fmt.Errorf("fail2ban player banned")
	}

	ctx, cancel := context.WithTimeout(parent, s.options.BackendDialTimeout)
	defer cancel()

	loginObserved := time.Now()
	backendConn, err := s.dialer.Dial(ctx, backend)
	if err != nil {
		s.recordBackendDialError(route, backend)
		return result, err
	}
	if _, err := backendConn.Write(handshakePacket.Raw); err != nil {
		s.recordEarlyLoginFailure(loginObserved, route, ip, username)
		_ = backendConn.Close()
		result.Fail2BanAction = "record_failure"
		return result, err
	}
	if _, err := backendConn.Write(loginStart.Raw); err != nil {
		s.recordEarlyLoginFailure(loginObserved, route, ip, username)
		_ = backendConn.Close()
		result.Fail2BanAction = "record_failure"
		return result, err
	}
	start := time.Now()
	stats := ProxyBidirectional(client, backendConn, s.options.IdleTimeout)
	result.Stats = stats
	if s.shouldRecordFailure(start, stats) {
		s.recordLoginFailure(route, ip, username)
		result.Fail2BanAction = "record_failure"
	} else {
		s.recordSuccess(route, "ip", ip)
		if username != "" {
			s.recordSuccess(route, "player", username)
		}
		result.Fail2BanAction = "record_success"
	}
	return result, nil
}

func (s *Server) handleStatus(parent context.Context, client net.Conn, backend Backend, handshake mcproto.Handshake, handshakePacket mcproto.Packet, logger *slog.Logger) (StatusResult, error) {
	result := StatusResult{CacheResult: "disabled"}
	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	statusRequest, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return result, fmt.Errorf("read status request: %w", err)
	}
	_ = client.SetReadDeadline(time.Time{})
	if statusRequest.ID != mcproto.StatusRequestPacketID {
		return result, fmt.Errorf("unexpected status request packet id 0x%x", statusRequest.ID)
	}

	cacheKey := fmt.Sprintf("%s|%s|%d", backend.Addr, NormalizeHost(handshake.ServerAddress), handshake.ProtocolVersion)
	cacheEnabled := s.statusCacheEnabled(backend)
	if cacheEnabled {
		result.CacheResult = "miss"
	}
	if cacheEnabled && s.cache != nil {
		if raw, ok := s.cache.Get(cacheKey); ok {
			result.CacheResult = "hit"
			logger.Debug("status cache hit")
			if _, err := client.Write(raw); err != nil {
				return result, err
			}
			result.PongHandled, err = s.handleCachedPing(client)
			return result, err
		}
	}
	logger.Debug("status cache miss")

	if s.options.StatusFallbackWhenUnhealthy && s.options.Health != nil && s.options.Health.Unhealthy(backend) {
		if s.options.Metrics != nil {
			s.options.Metrics.RecordStatusCircuitBreaker(backend.Addr)
		}
		if fallback, ok := s.statusFallback(cacheKey, handshake.ProtocolVersion, cacheEnabled); ok {
			result.CacheResult = "fallback"
			result.FallbackUsed = true
			logger.Debug("status fallback hit for unhealthy backend")
			if _, writeErr := client.Write(fallback); writeErr != nil {
				return result, writeErr
			}
			result.PongHandled, err = s.handleCachedPing(client)
			return result, err
		}
		return result, fmt.Errorf("backend unhealthy and no status fallback available")
	}

	ctx, cancel := context.WithTimeout(parent, s.options.BackendDialTimeout)
	defer cancel()
	backendConn, err := s.dialStatus(ctx, backend)
	if err != nil {
		s.recordBackendDialError(NormalizeHost(handshake.ServerAddress), backend)
		s.recordBackendHealthFailure(backend, err)
		if fallback, ok := s.statusFallback(cacheKey, handshake.ProtocolVersion, cacheEnabled); ok {
			result.CacheResult = "fallback"
			result.FallbackUsed = true
			logger.Debug("status fallback hit", "err", err)
			if _, writeErr := client.Write(fallback); writeErr != nil {
				return result, writeErr
			}
			result.PongHandled, err = s.handleCachedPing(client)
			return result, err
		}
		return result, err
	}
	defer backendConn.Close()
	s.setShortDeadline(backendConn)
	defer backendConn.SetDeadline(time.Time{})

	if _, err := backendConn.Write(handshakePacket.Raw); err != nil {
		s.recordBackendHealthFailure(backend, err)
		return result, err
	}
	if _, err := backendConn.Write(statusRequest.Raw); err != nil {
		s.recordBackendHealthFailure(backend, err)
		return result, err
	}
	response, err := mcproto.ReadPacket(backendConn, mcproto.MaxPacketLength)
	if err != nil {
		s.recordBackendHealthFailure(backend, err)
		if fallback, ok := s.statusFallback(cacheKey, handshake.ProtocolVersion, cacheEnabled); ok {
			result.CacheResult = "fallback"
			result.FallbackUsed = true
			logger.Debug("status fallback hit", "err", err)
			if _, writeErr := client.Write(fallback); writeErr != nil {
				return result, writeErr
			}
			result.PongHandled, err = s.handleCachedPing(client)
			return result, err
		}
		return result, fmt.Errorf("read status response: %w", err)
	}
	if response.ID != mcproto.StatusResponsePacketID {
		statusErr := fmt.Errorf("unexpected status response packet id 0x%x", response.ID)
		s.recordBackendHealthFailure(backend, statusErr)
		return result, fmt.Errorf("unexpected status response packet id 0x%x", response.ID)
	}
	s.recordBackendHealthSuccess(backend)
	if _, err := client.Write(response.Raw); err != nil {
		return result, err
	}
	if cacheEnabled && s.cache != nil {
		s.cache.SetWithFallback(cacheKey, response.Raw, s.options.StatusCacheTTL, s.motdFallbackTTL())
	}
	result.PongHandled, err = s.proxyStatusPing(client, backendConn)
	return result, err
}

func (s *Server) statusFallback(cacheKey string, protocolVersion int32, cacheEnabled bool) ([]byte, bool) {
	if !cacheEnabled || !s.options.MOTDCacheEnabled || s.cache == nil {
		return nil, false
	}
	return s.cache.GetFallback(cacheKey, protocolVersion)
}

func (s *Server) dialStatus(ctx context.Context, backend Backend) (net.Conn, error) {
	if dialer, ok := s.dialer.(StatusDialer); ok {
		return dialer.DialStatus(ctx, backend)
	}
	return s.dialer.Dial(ctx, backend)
}

func (s *Server) statusCacheEnabled(backend Backend) bool {
	if backend.StatusCache != nil {
		return s.options.StatusCacheEnabled && *backend.StatusCache
	}
	return s.options.StatusCacheEnabled
}

func (s *Server) motdFallbackTTL() time.Duration {
	if !s.options.MOTDCacheEnabled {
		return 0
	}
	return s.options.MOTDFallbackTTL
}

func (s *Server) isBanned(route, kind, value string) bool {
	return s.guard != nil && s.guard.IsBanned(route, kind, value)
}

func (s *Server) recordFailure(route, kind, value string) {
	if s.guard != nil {
		s.guard.RecordFailure(route, kind, value)
	}
}

func (s *Server) recordSuccess(route, kind, value string) {
	if s.guard != nil {
		s.guard.RecordSuccess(route, kind, value)
	}
}

func (s *Server) recordLoginFailure(route, ip, username string) {
	s.recordFailure(route, "ip", ip)
	if username != "" {
		s.recordFailure(route, "player", username)
	}
}

func (s *Server) recordBackendDialError(route string, backend Backend) {
	if s.options.Metrics != nil {
		s.options.Metrics.RecordBackendDialError(route, backend.Addr)
	}
}

func (s *Server) recordBackendHealthFailure(backend Backend, err error) {
	if s.options.Health != nil {
		s.options.Health.RecordFailure(backend.Addr, err)
	}
}

func (s *Server) recordBackendHealthSuccess(backend Backend) {
	if s.options.Health != nil {
		s.options.Health.RecordSuccess(backend.Addr)
	}
}

func (s *Server) recordEarlyLoginFailure(start time.Time, route, ip, username string) {
	if s.guard == nil || !s.guard.Enabled() {
		return
	}
	if time.Since(start) > s.guard.EarlyDisconnect() {
		return
	}
	s.recordLoginFailure(route, ip, username)
}

func (s *Server) shouldRecordFailure(start time.Time, stats PipeStats) bool {
	if s.guard == nil || !s.guard.Enabled() {
		return false
	}
	return time.Since(start) <= s.guard.EarlyDisconnect() && stats.BackendToClient == 0
}

func routeKey(backend Backend) string {
	if backend.Name != "" {
		return backend.Name
	}
	return backend.Addr
}

func remoteIP(conn net.Conn) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return conn.RemoteAddr().String()
	}
	return host
}

func (s *Server) setShortDeadline(conn net.Conn) {
	if s.options.HandshakeTimeout > 0 {
		_ = conn.SetDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
}

func (s *Server) handleCachedPing(client net.Conn) (bool, error) {
	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	ping, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return false, nil
	}
	if ping.ID != mcproto.StatusPingPacketID {
		return false, nil
	}
	pong, err := mcproto.BuildPong(ping.Data)
	if err != nil {
		return false, err
	}
	_, err = client.Write(pong.Raw)
	return err == nil, err
}

func (s *Server) proxyStatusPing(client, backend net.Conn) (bool, error) {
	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	ping, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return false, nil
	}
	if _, err := backend.Write(ping.Raw); err != nil {
		return false, err
	}
	pong, err := mcproto.ReadPacket(backend, s.options.MaxHandshakeSize)
	if err != nil {
		return false, err
	}
	_, err = client.Write(pong.Raw)
	return err == nil, err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
