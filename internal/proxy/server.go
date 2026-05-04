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
		if err := s.handleStatus(parent, client, backend, handshake, handshakePacket, logger); err != nil {
			logger.Debug("status failed", "err", err, "duration_ms", time.Since(start).Milliseconds())
		}
	case mcproto.NextStateLogin:
		stats, err := s.handleLogin(parent, client, backend, host, handshake, handshakePacket, remoteIP(client))
		if err != nil {
			logger.Debug("login proxy failed", "err", err, "duration_ms", time.Since(start).Milliseconds())
			return
		}
		logger.Info("proxy closed",
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes_client_to_backend", stats.ClientToBackend,
			"bytes_backend_to_client", stats.BackendToClient,
		)
	default:
		logger.Debug("unsupported next state")
	}
}

func (s *Server) handleLogin(parent context.Context, client net.Conn, backend Backend, route string, handshake mcproto.Handshake, handshakePacket mcproto.Packet, ip string) (PipeStats, error) {
	if route == "" {
		route = routeKey(backend)
	}
	if s.isBanned(route, "ip", ip) {
		return PipeStats{}, fmt.Errorf("fail2ban ip banned")
	}

	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	loginStart, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return PipeStats{}, fmt.Errorf("read login start: %w", err)
	}
	_ = client.SetReadDeadline(time.Time{})

	login, parseErr := mcproto.ParseLoginStart(loginStart, handshake.ProtocolVersion)
	username := login.Username
	if parseErr != nil {
		s.logger.Debug("parse login start failed", "err", parseErr, "route", route, "ip", ip)
	}
	if username != "" && s.isBanned(route, "player", username) {
		_ = mcproto.WritePacket(client, mcproto.BuildLoginDisconnect(`{"text":"Temporarily banned by proxy"}`))
		return PipeStats{}, fmt.Errorf("fail2ban player banned")
	}

	ctx, cancel := context.WithTimeout(parent, s.options.BackendDialTimeout)
	defer cancel()

	backendConn, err := s.dialer.Dial(ctx, backend)
	if err != nil {
		return PipeStats{}, err
	}
	if _, err := backendConn.Write(handshakePacket.Raw); err != nil {
		_ = backendConn.Close()
		return PipeStats{}, err
	}
	if _, err := backendConn.Write(loginStart.Raw); err != nil {
		_ = backendConn.Close()
		return PipeStats{}, err
	}
	start := time.Now()
	stats := ProxyBidirectional(client, backendConn, s.options.IdleTimeout)
	if s.shouldRecordFailure(start, stats) {
		s.recordFailure(route, "ip", ip)
		if username != "" {
			s.recordFailure(route, "player", username)
		}
	} else {
		s.recordSuccess(route, "ip", ip)
		if username != "" {
			s.recordSuccess(route, "player", username)
		}
	}
	return stats, nil
}

func (s *Server) handleStatus(parent context.Context, client net.Conn, backend Backend, handshake mcproto.Handshake, handshakePacket mcproto.Packet, logger *slog.Logger) error {
	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	statusRequest, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return fmt.Errorf("read status request: %w", err)
	}
	_ = client.SetReadDeadline(time.Time{})
	if statusRequest.ID != mcproto.StatusRequestPacketID {
		return fmt.Errorf("unexpected status request packet id 0x%x", statusRequest.ID)
	}

	cacheKey := fmt.Sprintf("%s|%s|%d", backend.Addr, NormalizeHost(handshake.ServerAddress), handshake.ProtocolVersion)
	cacheEnabled := s.statusCacheEnabled(backend)
	if cacheEnabled && s.cache != nil {
		if raw, ok := s.cache.Get(cacheKey); ok {
			logger.Debug("status cache hit")
			if _, err := client.Write(raw); err != nil {
				return err
			}
			return s.handleCachedPing(client)
		}
	}
	logger.Debug("status cache miss")

	ctx, cancel := context.WithTimeout(parent, s.options.BackendDialTimeout)
	defer cancel()
	backendConn, err := s.dialStatus(ctx, backend)
	if err != nil {
		if fallback, ok := s.statusFallback(cacheKey, handshake.ProtocolVersion, cacheEnabled); ok {
			logger.Debug("status fallback hit", "err", err)
			if _, writeErr := client.Write(fallback); writeErr != nil {
				return writeErr
			}
			return s.handleCachedPing(client)
		}
		return err
	}
	defer backendConn.Close()
	s.setShortDeadline(backendConn)
	defer backendConn.SetDeadline(time.Time{})

	if _, err := backendConn.Write(handshakePacket.Raw); err != nil {
		return err
	}
	if _, err := backendConn.Write(statusRequest.Raw); err != nil {
		return err
	}
	response, err := mcproto.ReadPacket(backendConn, mcproto.MaxPacketLength)
	if err != nil {
		if fallback, ok := s.statusFallback(cacheKey, handshake.ProtocolVersion, cacheEnabled); ok {
			logger.Debug("status fallback hit", "err", err)
			if _, writeErr := client.Write(fallback); writeErr != nil {
				return writeErr
			}
			return s.handleCachedPing(client)
		}
		return fmt.Errorf("read status response: %w", err)
	}
	if response.ID != mcproto.StatusResponsePacketID {
		return fmt.Errorf("unexpected status response packet id 0x%x", response.ID)
	}
	if _, err := client.Write(response.Raw); err != nil {
		return err
	}
	if cacheEnabled && s.cache != nil {
		s.cache.SetWithFallback(cacheKey, response.Raw, s.options.StatusCacheTTL, s.motdFallbackTTL())
	}
	return s.proxyStatusPing(client, backendConn)
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

func (s *Server) handleCachedPing(client net.Conn) error {
	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	ping, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return nil
	}
	if ping.ID != mcproto.StatusPingPacketID {
		return nil
	}
	pong, err := mcproto.BuildPong(ping.Data)
	if err != nil {
		return err
	}
	_, err = client.Write(pong.Raw)
	return err
}

func (s *Server) proxyStatusPing(client, backend net.Conn) error {
	if s.options.HandshakeTimeout > 0 {
		_ = client.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}
	ping, err := mcproto.ReadPacket(client, s.options.MaxHandshakeSize)
	if err != nil {
		return nil
	}
	if _, err := backend.Write(ping.Raw); err != nil {
		return err
	}
	pong, err := mcproto.ReadPacket(backend, s.options.MaxHandshakeSize)
	if err != nil {
		return err
	}
	_, err = client.Write(pong.Raw)
	return err
}
