package mcproto

import (
	"bytes"
	"testing"
)

func TestParseHandshake(t *testing.T) {
	packet := BuildHandshake(765, "Survival.Example.Com.", 25565, NextStateLogin)
	got, err := ParseHandshake(packet)
	if err != nil {
		t.Fatalf("ParseHandshake: %v", err)
	}
	if got.ProtocolVersion != 765 {
		t.Fatalf("protocol version = %d", got.ProtocolVersion)
	}
	if got.ServerAddress != "Survival.Example.Com." {
		t.Fatalf("server address = %q", got.ServerAddress)
	}
	if got.ServerPort != 25565 {
		t.Fatalf("server port = %d", got.ServerPort)
	}
	if got.NextState != NextStateLogin {
		t.Fatalf("next state = %d", got.NextState)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"Survival.Example.Com.":        "survival.example.com",
		"survival.example.com:25565":   "survival.example.com",
		"survival.example.com\x00tag":  "survival.example.com",
		"Forge.Example.COM\x00FML\x00": "forge.example.com",
		"  MC.Example.Com  ":           "mc.example.com",
		"[2001:db8::1]":                "2001:db8::1",
		"[2001:db8::1]:25565":          "2001:db8::1",
	}
	for input, want := range cases {
		if got := NormalizeHost(input); got != want {
			t.Fatalf("NormalizeHost(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReadPacketPreservesRaw(t *testing.T) {
	packet := BuildHandshake(765, "mc.example.com", 25565, NextStateStatus)
	got, err := ReadPacket(bytes.NewReader(packet.Raw), 64*1024)
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if !bytes.Equal(got.Raw, packet.Raw) {
		t.Fatal("raw packet was not preserved")
	}
}
