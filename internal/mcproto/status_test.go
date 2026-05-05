package mcproto

import (
	"strings"
	"testing"
)

func TestExtractMOTD(t *testing.T) {
	motd, ok := ExtractMOTD(`{"description":{"text":"Hello"},"players":{"online":1}}`)
	if !ok {
		t.Fatal("expected motd")
	}
	if string(motd) != `{"text":"Hello"}` {
		t.Fatalf("motd = %s", motd)
	}

	motd, ok = ExtractMOTD(`{"description":"Plain"}`)
	if !ok {
		t.Fatal("expected string motd")
	}
	if string(motd) != `"Plain"` {
		t.Fatalf("motd = %s", motd)
	}
}

func TestBuildFallbackStatus(t *testing.T) {
	packet, err := BuildFallbackStatus(765, []byte(`{"text":"Cached"}`))
	if err != nil {
		t.Fatalf("BuildFallbackStatus: %v", err)
	}
	statusJSON, err := ParseStatusJSON(packet)
	if err != nil {
		t.Fatalf("ParseStatusJSON: %v", err)
	}
	if _, ok := ExtractMOTD(statusJSON); !ok {
		t.Fatal("fallback status missing motd")
	}
}

func TestBuildFallbackStatusWithOptions(t *testing.T) {
	packet, err := BuildFallbackStatusWithOptions(765, []byte(`{"text":"Cached"}`), FallbackStatusOptions{
		VersionName: "custom fallback",
		PlayersMax:  99,
	})
	if err != nil {
		t.Fatalf("BuildFallbackStatusWithOptions: %v", err)
	}
	statusJSON, err := ParseStatusJSON(packet)
	if err != nil {
		t.Fatalf("ParseStatusJSON: %v", err)
	}
	if !strings.Contains(statusJSON, `"name":"custom fallback"`) || !strings.Contains(statusJSON, `"max":99`) || !strings.Contains(statusJSON, `"online":0`) {
		t.Fatalf("fallback status missing options: %s", statusJSON)
	}
}
