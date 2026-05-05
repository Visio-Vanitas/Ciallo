package probe

import (
	"context"
	"net"
	"testing"
	"time"

	"ciallo/internal/mcproto"
)

func TestParseStatus(t *testing.T) {
	got, err := parseStatus(`{"version":{"name":"1.21.1","protocol":772},"players":{"max":20,"online":2},"description":{"text":"hello ","extra":[{"text":"world"}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "hello world" {
		t.Fatalf("description = %q", got.Description)
	}
	if got.Version["name"] != "1.21.1" {
		t.Fatalf("version = %#v", got.Version)
	}
}

func TestParseStatusRejectsBadJSON(t *testing.T) {
	if _, err := parseStatus(`{`); err == nil {
		t.Fatal("expected bad json error")
	}
}

func TestRunProbesMockStatusBackend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = mcproto.ReadPacket(conn, mcproto.MaxPacketLength)
		_, _ = mcproto.ReadPacket(conn, mcproto.MaxPacketLength)
		_ = mcproto.WritePacket(conn, mcproto.BuildStatusResponse(`{"version":{"name":"mock","protocol":772},"players":{"max":20,"online":3},"description":{"text":"mock motd"}}`))
	}()

	got, err := Run(context.Background(), Options{
		Addr:    ln.Addr().String(),
		Host:    "mock.example.com",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != "mock motd" {
		t.Fatalf("description = %q", got.Description)
	}
	if got.Version["name"] != "mock" {
		t.Fatalf("version = %#v", got.Version)
	}
}
