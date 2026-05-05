package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"ciallo/internal/proxy"
)

func TestCheckerThresholdsAndRecovery(t *testing.T) {
	now := time.Unix(100, 0)
	checker := New(Options{
		Enabled:           true,
		FailureThreshold:  2,
		SuccessThreshold:  1,
		CircuitBreakerTTL: time.Minute,
	}, []BackendTarget{{Backend: proxy.Backend{Addr: "127.0.0.1:25565"}, ProbeHost: "route.example.com"}}, nil, nil)
	checker.now = func() time.Time { return now }

	backend := proxy.Backend{Addr: "127.0.0.1:25565"}
	checker.RecordFailure(backend.Addr, errors.New("first"))
	if checker.Unhealthy(backend) {
		t.Fatal("backend should stay healthy before threshold")
	}
	checker.RecordFailure(backend.Addr, errors.New("second"))
	if !checker.Unhealthy(backend) {
		t.Fatal("backend should be unhealthy after threshold")
	}
	status := checker.BackendStatus(backend)
	if !status.CircuitOpen || status.CircuitTrips != 1 {
		t.Fatalf("status = %+v", status)
	}

	checker.RecordSuccess(backend.Addr)
	if checker.Unhealthy(backend) {
		t.Fatal("backend should recover after success threshold")
	}
}

func TestBuildTargetsProbeHostPrecedence(t *testing.T) {
	targets := BuildTargets([]proxy.Route{
		{Hosts: []string{"Route.Example.COM"}, Backend: proxy.Backend{Addr: "127.0.0.1:25565"}},
		{Hosts: []string{"other.example.com"}, Backend: proxy.Backend{Addr: "127.0.0.1:25565"}},
	}, &proxy.Backend{Addr: "127.0.0.1:25566"}, "configured.example.com")
	if len(targets) != 2 {
		t.Fatalf("targets len = %d", len(targets))
	}
	if targets[0].ProbeHost != "route.example.com" {
		t.Fatalf("route probe host = %q", targets[0].ProbeHost)
	}
	if targets[1].ProbeHost != "configured.example.com" {
		t.Fatalf("default probe host = %q", targets[1].ProbeHost)
	}
}

func TestCheckerCheckOnceRecordsResults(t *testing.T) {
	errCheck := errors.New("down")
	checker := New(Options{
		Enabled:           true,
		FailureThreshold:  1,
		SuccessThreshold:  1,
		CircuitBreakerTTL: time.Minute,
	}, []BackendTarget{{Backend: proxy.Backend{Addr: "127.0.0.1:25565"}, ProbeHost: "route.example.com"}}, func(context.Context, BackendTarget, Options) error {
		return errCheck
	}, nil)
	checker.CheckOnce(context.Background())
	if !checker.Unhealthy(proxy.Backend{Addr: "127.0.0.1:25565"}) {
		t.Fatal("expected unhealthy after failed check")
	}
}
