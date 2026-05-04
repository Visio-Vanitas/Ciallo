package fail2ban

import (
	"testing"
	"time"
)

func TestGuardBanAndUnban(t *testing.T) {
	now := time.Unix(100, 0)
	guard := New(Options{
		Enabled:     true,
		MaxFailures: 2,
		Window:      time.Minute,
		BanDuration: time.Minute,
	}, func() time.Time { return now })

	if guard.IsBanned("route-a", "ip", "127.0.0.1") {
		t.Fatal("unexpected initial ban")
	}
	if guard.RecordFailure("route-a", "ip", "127.0.0.1") {
		t.Fatal("first failure should not ban")
	}
	if !guard.RecordFailure("route-a", "ip", "127.0.0.1") {
		t.Fatal("second failure should ban")
	}
	if !guard.IsBanned("route-a", "ip", "127.0.0.1") {
		t.Fatal("expected ban")
	}
	if len(guard.Snapshot()) != 1 {
		t.Fatal("expected one active ban")
	}
	if !guard.Unban("route-a", "ip", "127.0.0.1") {
		t.Fatal("expected unban to remove entry")
	}
	if guard.IsBanned("route-a", "ip", "127.0.0.1") {
		t.Fatal("ban should be removed")
	}
}

func TestGuardWindowAndRouteIsolation(t *testing.T) {
	now := time.Unix(100, 0)
	guard := New(Options{
		Enabled:     true,
		MaxFailures: 2,
		Window:      time.Second,
		BanDuration: time.Minute,
	}, func() time.Time { return now })

	guard.RecordFailure("route-a", "player", "Steve")
	now = now.Add(2 * time.Second)
	if guard.RecordFailure("route-a", "player", "Steve") {
		t.Fatal("window should have reset")
	}
	if guard.IsBanned("route-b", "player", "Steve") {
		t.Fatal("route-b should be isolated")
	}
}

func TestGuardDisabled(t *testing.T) {
	guard := New(Options{Enabled: false, MaxFailures: 1}, time.Now)
	if guard.RecordFailure("route-a", "ip", "127.0.0.1") {
		t.Fatal("disabled guard should not ban")
	}
	if guard.IsBanned("route-a", "ip", "127.0.0.1") {
		t.Fatal("disabled guard should not report ban")
	}
}
