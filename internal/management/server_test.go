package management

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"ciallo/internal/fail2ban"
)

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

	server := New(Options{Enabled: true, Address: addr}, guard, nil)
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
