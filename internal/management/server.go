package management

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"ciallo/internal/fail2ban"
)

type Options struct {
	Enabled bool
	Address string
}

type Server struct {
	options Options
	guard   *fail2ban.Guard
	logger  *slog.Logger
	server  *http.Server
}

func New(options Options, guard *fail2ban.Guard, logger *slog.Logger) *Server {
	if options.Address == "" {
		options.Address = "127.0.0.1:25575"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		options: options,
		guard:   guard,
		logger:  logger,
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if !s.options.Enabled {
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/fail2ban/bans", s.handleBans)

	server := &http.Server{
		Addr:              s.options.Address,
		Handler:           s.logRequests(mux),
		ReadHeaderTimeout: 3 * time.Second,
	}
	s.server = server

	ln, err := net.Listen("tcp", s.options.Address)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	s.logger.Info("management listening", "addr", ln.Addr().String())
	err = server.Serve(ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleBans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, map[string]any{"bans": s.guard.Snapshot()})
	case http.MethodDelete:
		route := r.URL.Query().Get("route")
		kind := r.URL.Query().Get("kind")
		value := r.URL.Query().Get("value")
		if route == "" || kind == "" || value == "" {
			http.Error(w, "route, kind, and value are required", http.StatusBadRequest)
			return
		}
		removed := s.guard.Unban(route, kind, value)
		s.writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Info("management access",
			"event", "management",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
