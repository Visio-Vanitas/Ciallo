package management

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"ciallo/internal/fail2ban"
	"ciallo/internal/health"
	"ciallo/internal/metrics"
)

type readyFunc func() bool

func (f readyFunc) Ready() bool { return f() }

type healthSource struct {
	snapshot health.Snapshot
}

func (h healthSource) Snapshot() health.Snapshot {
	return h.snapshot
}

func TestManagementListsAndDeletesBans(t *testing.T) {
	guard := fail2ban.New(fail2ban.Options{
		Enabled:     true,
		MaxFailures: 1,
		BanDuration: time.Minute,
	}, time.Now)
	guard.RecordFailure("route-a", "ip", "127.0.0.1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	rec := metrics.New()
	rec.IncActiveConnections()
	server := NewWithHealth(Options{Enabled: true, Address: addr, Version: "v0.0.6"}, guard, rec, readyFunc(func() bool { return true }), healthSource{snapshot: health.Snapshot{Enabled: true, Total: 1, Healthy: 1}}, logger)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("server error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("management server did not stop")
		}
	})

	waitHTTP(t, "http://"+addr+"/healthz")

	readyResp, err := http.Get("http://" + addr + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("ready status = %d", readyResp.StatusCode)
	}
	var readyBody struct {
		Ready   bool   `json:"ready"`
		Version string `json:"version"`
		Health  struct {
			Enabled bool `json:"enabled"`
			Total   int  `json:"total"`
			Healthy int  `json:"healthy"`
		} `json:"health"`
	}
	if err := json.NewDecoder(readyResp.Body).Decode(&readyBody); err != nil {
		t.Fatal(err)
	}
	if !readyBody.Ready || readyBody.Version != "v0.0.6" || !readyBody.Health.Enabled || readyBody.Health.Total != 1 || readyBody.Health.Healthy != 1 {
		t.Fatalf("ready body = %#v", readyBody)
	}

	metricsResp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer metricsResp.Body.Close()
	body, err := io.ReadAll(metricsResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if metricsResp.StatusCode != http.StatusOK || !strings.Contains(string(body), "ciallo_active_connections 1") {
		t.Fatalf("metrics response status=%d body=%s", metricsResp.StatusCode, body)
	}

	resp, err := http.Get("http://" + addr + "/fail2ban/bans")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var listed struct {
		Bans []fail2ban.Ban `json:"bans"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Bans) != 1 {
		t.Fatalf("bans len = %d", len(listed.Bans))
	}

	req, err := http.NewRequest(http.MethodDelete, "http://"+addr+"/fail2ban/bans?route=route-a&kind=ip&value=127.0.0.1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	if guard.IsBanned("route-a", "ip", "127.0.0.1") {
		t.Fatal("ban should be cleared")
	}
	text := logs.String()
	if !strings.Contains(text, "event=management") || !strings.Contains(text, "method=GET") || !strings.Contains(text, "method=DELETE") || !strings.Contains(text, "status=200") {
		t.Fatalf("management access logs missing fields:\n%s", text)
	}
}

func TestReadyzReportsUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	server := NewWithDependencies(Options{Enabled: true, Address: addr}, nil, nil, readyFunc(func() bool { return false }), slog.Default())
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("server error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("management server did not stop")
		}
	})

	waitHTTP(t, "http://"+addr+"/healthz")
	resp, err := http.Get("http://" + addr + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d", resp.StatusCode)
	}
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s", url)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
