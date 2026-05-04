package cache

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"ciallo/internal/mcproto"
)

func TestStatusCacheTTL(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewStatusCache(func() time.Time { return now })
	c.Set("backend|host|765", []byte("status"), time.Second)

	got, ok := c.Get("backend|host|765")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got) != "status" {
		t.Fatalf("got %q", got)
	}

	now = now.Add(2 * time.Second)
	if _, ok := c.Get("backend|host|765"); ok {
		t.Fatal("expected cache miss after ttl")
	}
}

func TestStatusCacheFallbackTTL(t *testing.T) {
	now := time.Unix(100, 0)
	c := NewStatusCache(func() time.Time { return now })
	response := mcproto.BuildStatusResponse(`{"version":{"name":"test","protocol":765},"players":{"max":20,"online":1},"description":{"text":"cached motd"}}`)
	c.SetWithFallback("backend|host|765", response.Raw, time.Second, 10*time.Second)

	now = now.Add(2 * time.Second)
	fallback, ok := c.GetFallback("backend|host|765", 765)
	if !ok {
		t.Fatal("expected fallback hit")
	}
	packet, err := mcproto.ReadPacket(bytes.NewReader(fallback), mcproto.MaxPacketLength)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	statusJSON, err := mcproto.ParseStatusJSON(packet)
	if err != nil {
		t.Fatalf("ParseStatusJSON: %v", err)
	}
	if !strings.Contains(statusJSON, "cached motd") {
		t.Fatalf("fallback status does not contain cached motd: %s", statusJSON)
	}

	now = now.Add(11 * time.Second)
	if _, ok := c.GetFallback("backend|host|765", 765); ok {
		t.Fatal("expected fallback miss after stale ttl")
	}
}
